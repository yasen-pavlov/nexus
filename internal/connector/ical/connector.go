// Package ical implements a connector for iCloud (and generic CalDAV)
// calendars. It authenticates with an Apple ID + app-specific password,
// discovers the user's calendars for a selection UI, and incrementally syncs
// events for full-text + semantic search.
//
// Design notes:
//   - Incremental sync uses an ETag-diff manifest (per-calendar {href→ETag})
//     rather than RFC 6578 sync-collection: iCloud invalidates sync-tokens
//     periodically, and go-webdav's SyncCollection is unreleased — whereas
//     Nexus's pipeline already reconciles deletions by enumerating SourceIDs.
//     Each sync lists event hrefs+ETags, multigets only the changed ones, and
//     re-emits the full SourceID set so the pipeline removes orphans.
//   - Recurring events are indexed series-level (one document per series, with
//     the rule and nearest occurrence dates in metadata) rather than expanded
//     into one document per occurrence — a daily standup is one useful RAG
//     document, not 500 near-identical noise-hub vectors. Modified instances
//     (overrides carrying RECURRENCE-ID) are indexed as their own documents.
package ical

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/netguard"
)

// defaultEndpoint is the iCloud CalDAV entry point. Discovery (well-known +
// current-user-principal) redirects from here to the partition host.
const defaultEndpoint = "https://caldav.icloud.com"

// checkpointEvery bounds how many events a crash replays — emit a cursor
// checkpoint after this many documents.
const checkpointEvery = 200

func init() {
	connector.Register("ical", func() connector.Connector {
		// SSRF guard: the endpoint defaults to iCloud but can be overridden, so
		// the guarded client refuses loopback/link-local/metadata targets.
		return &Connector{
			client:   netguard.NewClient(30 * time.Second),
			endpoint: defaultEndpoint,
			now:      time.Now,
		}
	})
}

// Connector syncs events from selected CalDAV calendars.
type Connector struct {
	name      string
	username  string   // Apple ID
	password  string   // app-specific password (encrypted at rest)
	calendars []string // selected calendar paths (from the discovery picker)
	syncSince time.Time
	endpoint  string
	client    *http.Client
	now       func() time.Time // injectable clock for tests
}

func (c *Connector) Type() string { return "ical" }
func (c *Connector) Name() string { return c.name }

func (c *Connector) Configure(cfg connector.Config) error {
	name, _ := cfg["name"].(string)
	if name == "" {
		name = "ical"
	}
	c.name = name

	username := cfg.StringVal("username")
	if username == "" {
		return fmt.Errorf("ical: username (Apple ID) is required")
	}
	c.username = username

	password := cfg.StringVal("password")
	if password == "" {
		return fmt.Errorf("ical: password (app-specific password) is required")
	}
	c.password = password

	if endpoint := cfg.StringVal("endpoint"); endpoint != "" {
		c.endpoint = strings.TrimRight(endpoint, "/")
	}

	c.calendars = readStringSlice(cfg, "calendars")
	c.syncSince = connector.ComputeSyncSince(cfg)
	return nil
}

// readStringSlice reads a config value that may arrive as a JSON array
// ([]any after unmarshal), a []string, or a comma-separated string.
func readStringSlice(cfg connector.Config, key string) []string {
	switch v := cfg[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func (c *Connector) caldavClient() (*caldav.Client, error) {
	httpClient := webdav.HTTPClientWithBasicAuth(c.client, c.username, c.password)
	client, err := caldav.NewClient(httpClient, c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("ical: build client: %w", err)
	}
	return client, nil
}

// Validate confirms the credentials authenticate against the CalDAV server.
func (c *Connector) Validate() error {
	if c.username == "" || c.password == "" {
		return fmt.Errorf("ical: username and password are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := c.caldavClient()
	if err != nil {
		return err
	}
	if _, err := client.FindCurrentUserPrincipal(ctx); err != nil {
		if isAuthError(err) {
			return fmt.Errorf("ical: authentication failed: check the Apple ID and app-specific password")
		}
		return fmt.Errorf("ical: cannot reach %s: %w", c.endpoint, err)
	}
	return nil
}

// isAuthError reports whether err looks like an HTTP 401/403.
func isAuthError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "401") || strings.Contains(s, "403") ||
		strings.Contains(s, "unauthorized") || strings.Contains(s, "forbidden")
}

// DiscoverResources lists the account's event calendars for the selection UI.
// It implements connector.ResourceDiscoverer. Reminder (VTODO-only) lists are
// filtered out — this connector indexes events.
func (c *Connector) DiscoverResources(ctx context.Context) ([]connector.DiscoveredResource, error) {
	client, err := c.caldavClient()
	if err != nil {
		return nil, err
	}
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		if isAuthError(err) {
			return nil, fmt.Errorf("ical: authentication failed — check the Apple ID and app-specific password")
		}
		return nil, fmt.Errorf("ical: find principal: %w", err)
	}
	homeSet, err := client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, fmt.Errorf("ical: find calendar-home-set: %w", err)
	}
	cals, err := client.FindCalendars(ctx, homeSet)
	if err != nil {
		return nil, fmt.Errorf("ical: list calendars: %w", err)
	}

	out := make([]connector.DiscoveredResource, 0, len(cals))
	for i := range cals {
		cal := cals[i]
		if !supportsEvents(cal) {
			continue
		}
		out = append(out, connector.DiscoveredResource{
			ID:   cal.Path,
			Name: calendarName(cal),
			Meta: map[string]any{"description": cal.Description},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// supportsEvents reports whether a calendar holds VEVENTs. iCloud advertises
// the supported component set; when it's empty (some servers omit it) we
// assume the calendar holds events.
func supportsEvents(cal caldav.Calendar) bool {
	if len(cal.SupportedComponentSet) == 0 {
		return true
	}
	for _, comp := range cal.SupportedComponentSet {
		if strings.EqualFold(comp, "VEVENT") {
			return true
		}
	}
	return false
}

func calendarName(cal caldav.Calendar) string {
	if cal.Name != "" {
		return cal.Name
	}
	return strings.Trim(cal.Path, "/")
}

// calendarDisplayNames resolves selected calendar paths to their CalDAV
// display names. iCloud calendar URLs end in an opaque UUID, so a path's last
// segment is not human-readable; the display name (what the user sees in the
// Calendar app) lives in the calendar's DAV:displayname. Best-effort: on any
// failure callers fall back to the path's last segment, so a transient
// discovery error never aborts a sync.
func (c *Connector) calendarDisplayNames(ctx context.Context) map[string]string {
	client, err := c.caldavClient()
	if err != nil {
		return nil
	}
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil
	}
	homeSet, err := client.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil
	}
	cals, err := client.FindCalendars(ctx, homeSet)
	if err != nil {
		return nil
	}
	names := make(map[string]string, len(cals))
	for i := range cals {
		if n := cals[i].Name; n != "" {
			names[cals[i].Path] = n
		}
	}
	return names
}

// Fetch streams calendar events with ETag-diff incremental sync. See the
// package doc for the strategy.
func (c *Connector) Fetch(ctx context.Context, cursor *model.SyncCursor) (<-chan model.FetchItem, <-chan error) {
	items := make(chan model.FetchItem)
	errs := make(chan error, 1)

	go func() {
		defer close(items)
		defer close(errs)
		if err := c.streamFetch(ctx, cursor, items); err != nil {
			errs <- err
		}
	}()

	return items, errs
}

func (c *Connector) streamFetch(ctx context.Context, cursor *model.SyncCursor, items chan<- model.FetchItem) error {
	if len(c.calendars) == 0 {
		// Nothing selected — emit an empty authoritative enumeration so the
		// pipeline clears any previously-synced events for this connector.
		if !emitItem(ctx, items, model.FetchItem{EnumerationComplete: true}) {
			return ctx.Err()
		}
		return nil
	}

	prev := loadManifest(cursor)
	// A changed sync_since means previously-filtered events may now qualify;
	// force a full re-derivation by discarding the manifest.
	if cursor != nil && cursorString(cursor, "sync_since") != c.syncSinceKey() {
		prev = manifest{}
	}

	next, allSourceIDs, err := c.syncSelectedCalendars(ctx, prev, items)
	if err != nil {
		return err
	}

	// Truncation guard: if a previous sync knew of many resources and this run's
	// listing came back dramatically smaller, the server likely returned a
	// throttled/partial result. Treating that as authoritative would delete the
	// "missing" events, so opt out of deletion reconciliation this round (still
	// index whatever we did fetch) and keep the prior manifest.
	if suspiciousShrink(prev, allSourceIDs) {
		_ = emitItem(ctx, items, model.FetchItem{Checkpoint: c.newCursor(prev)})
		return nil
	}

	if err := c.emitEnumeration(ctx, items, allSourceIDs); err != nil {
		return err
	}
	_ = emitItem(ctx, items, model.FetchItem{Checkpoint: c.newCursor(next)})
	return nil
}

// syncSelectedCalendars syncs each selected calendar in turn, streaming docs +
// progress through items. It returns the fresh per-calendar ETag manifest and
// the full (unsorted) SourceID enumeration across all calendars.
func (c *Connector) syncSelectedCalendars(ctx context.Context, prev manifest, items chan<- model.FetchItem) (manifest, []string, error) {
	// Resolve human display names for the selected calendars once per sync
	// (best-effort — falls back to the path's last segment per calendar).
	names := c.calendarDisplayNames(ctx)

	next := manifest{}
	var allSourceIDs []string
	emitted := 0

	for _, calPath := range c.calendars {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		calName := names[calPath]
		if calName == "" {
			calName = calShort(calPath)
		}
		if !emitItem(ctx, items, model.FetchItem{Scope: strPtr(calName)}) {
			return nil, nil, ctx.Err()
		}
		calState, sids, err := c.syncCalendar(ctx, calPath, calName, prev[calPath], items, &emitted)
		if err != nil {
			return nil, nil, fmt.Errorf("ical: sync calendar %s: %w", calName, err)
		}
		next[calPath] = calState
		allSourceIDs = append(allSourceIDs, sids...)
		total := int64(len(allSourceIDs))
		_ = emitItem(ctx, items, model.FetchItem{EstimatedTotal: &total})
	}
	return next, allSourceIDs, nil
}

// emitEnumeration streams the authoritative SourceID set (lex sorted to match
// OpenSearch's source_id keyword sort) followed by the enumeration-complete
// marker. A false from emitItem means the context was cancelled.
func (c *Connector) emitEnumeration(ctx context.Context, items chan<- model.FetchItem, allSourceIDs []string) error {
	sort.Strings(allSourceIDs)
	for i := range allSourceIDs {
		sid := allSourceIDs[i]
		if !emitItem(ctx, items, model.FetchItem{SourceID: &sid}) {
			return ctx.Err()
		}
	}
	if !emitItem(ctx, items, model.FetchItem{EnumerationComplete: true}) {
		return ctx.Err()
	}
	return nil
}

// suspiciousShrink reports whether the current listing is implausibly smaller
// than what the previous sync recorded — a signal that the server truncated the
// response (e.g. under throttling) rather than that the events were deleted.
func suspiciousShrink(prev manifest, current []string) bool {
	prevTotal := 0
	for _, hrefs := range prev {
		prevTotal += len(hrefs)
	}
	// Only guard once there's a meaningful baseline; below that, normal churn
	// (an emptied calendar) is indistinguishable from truncation and rare.
	return prevTotal >= 20 && len(current) < prevTotal/2
}

// syncCalendar lists a calendar's event resources (an ETag-only calendar-query
// REPORT), GETs only the resources whose ETag changed since the last sync, and
// returns the new per-href ETag map plus the full set of hrefs.
//
// The returned hrefs are the authoritative SourceID enumeration: every resource
// that EXISTS upstream, taken from the listing — NOT from successfully-fetched
// content. This is what makes deletion reconciliation safe under iCloud
// throttling: a resource that lists but fails to GET this round (rate-limited)
// is still enumerated, so it is preserved rather than reconciled out. Its
// overrides (SourceID href:RECURRENCE-ID) are preserved by the pipeline's
// colon-suffix child rule.
//
// We hand-build the etag-only REPORT rather than use go-webdav's ReadDir or
// QueryCalendar: both request properties (resourcetype, allprop/allcomp
// calendar-data) that iCloud returns a per-property 404 for on calendar
// objects, and the library treats that 404 as fatal. An etag-only
// calendar-query is universally iCloud-compatible; a plain GET fetches the
// raw .ics with no XML negotiation.
func (c *Connector) syncCalendar(ctx context.Context, calPath, calName string, prev map[string]string, items chan<- model.FetchItem, emitted *int) (map[string]string, []string, error) {
	etags, err := c.listCalendarETags(ctx, calPath)
	if err != nil {
		return nil, nil, fmt.Errorf("list calendar: %w", err)
	}

	state := make(map[string]string, len(etags))
	hrefs := make([]string, 0, len(etags))
	for href, etag := range etags {
		hrefs = append(hrefs, href) // authoritative existence — enumerate regardless of GET

		// Unchanged (same ETag) — skip the GET + re-embed, keep the manifest entry.
		if was, ok := prev[href]; ok && etag != "" && was == etag {
			state[href] = etag
			continue
		}
		fetched, err := c.fetchResource(ctx, href, calPath, calName, items, emitted)
		if err != nil {
			return nil, nil, err
		}
		if fetched {
			state[href] = etag // fetched successfully — record so the next sync skips it
		}
		// !fetched: listed but unfetchable this round (a 4xx skip). It's still
		// enumerated above (not deleted); deliberately DON'T record its ETag so
		// the next sync retries the fetch instead of skipping it.
	}
	return state, hrefs, nil
}

// fetchResource GETs one calendar resource and streams its documents. It returns
// fetched=false when the resource listed but couldn't be fetched this round (a
// 4xx skip), so the caller leaves its ETag unrecorded and retries next sync.
func (c *Connector) fetchResource(ctx context.Context, href, calPath, calName string, items chan<- model.FetchItem, emitted *int) (bool, error) {
	cal, err := c.getCalendarData(ctx, href)
	if err != nil {
		return false, fmt.Errorf("get %s: %w", href, err)
	}
	if cal == nil {
		return false, nil
	}
	for _, doc := range c.eventsToDocuments(cal, href, calPath, calName) {
		d := doc
		if !emitItem(ctx, items, model.FetchItem{Doc: &d}) {
			return false, ctx.Err()
		}
		*emitted++
		if *emitted%checkpointEvery == 0 {
			_ = emitItem(ctx, items, model.FetchItem{Checkpoint: c.newCursor(nil)})
		}
	}
	return true, nil
}

// getCalendarData fetches and parses one calendar object via a plain GET.
// resolveURL preserves the href's existing percent-encoding (passing the raw
// href through go-webdav's client double-encodes it).
//
//   - 429 / 503 (iCloud throttling) are retried with exponential backoff,
//     honoring Retry-After; persistent throttling surfaces as an error so the
//     sync fails cleanly (no deletion) and retries next run.
//   - Other 4xx (404 gone, a collection, an odd resource) return (nil, nil) so
//     a single bad resource doesn't abort the whole sync — and because the
//     resource is still enumerated from the listing, it isn't reconciled out.
func (c *Connector) getCalendarData(ctx context.Context, href string) (*ical.Calendar, error) {
	target, err := resolveURL(c.endpoint, href)
	if err != nil {
		return nil, err
	}

	const maxAttempts = 4
	for attempt := 0; ; attempt++ {
		cal, status, retryAfter, err := c.tryGetCalendarData(ctx, target)
		if err != nil {
			return nil, err
		}
		switch {
		case status == http.StatusOK:
			return cal, nil
		case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable:
			if attempt >= maxAttempts-1 {
				return nil, fmt.Errorf("GET throttled (status %d) after %d attempts", status, maxAttempts)
			}
			if !sleepCtx(ctx, throttleDelay(attempt, retryAfter)) {
				return nil, ctx.Err()
			}
		case status >= 400 && status < 500:
			return nil, nil // gone / odd resource — skip (still enumerated)
		default:
			return nil, fmt.Errorf("GET status %d", status)
		}
	}
}

// tryGetCalendarData performs one GET attempt, returning the decoded calendar
// (on 200), the HTTP status, and any Retry-After delay.
func (c *Connector) tryGetCalendarData(ctx context.Context, target string) (*ical.Calendar, int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "text/calendar")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil, resp.StatusCode, retryAfter, nil
	}
	cal, err := ical.NewDecoder(resp.Body).Decode()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode ics: %w", err)
	}
	return cal, http.StatusOK, 0, nil
}

// throttleDelay picks a backoff: the server's Retry-After if given, else
// exponential (1s, 2s, 4s …) capped at 30s.
func throttleDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	d := time.Second << attempt
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// calendarQueryETagBody is an RFC 4791 calendar-query REPORT that asks only for
// getetag on VEVENT resources — the minimal, universally iCloud-supported
// listing request.
const calendarQueryETagBody = `<?xml version="1.0" encoding="utf-8"?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop><D:getetag/></D:prop>
  <C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VEVENT"/></C:comp-filter></C:filter>
</C:calendar-query>`

type davMultistatus struct {
	XMLName   xml.Name      `xml:"multistatus"`
	Responses []davResponse `xml:"response"`
}

type davResponse struct {
	Href      string        `xml:"href"`
	Propstats []davPropstat `xml:"propstat"`
}

type davPropstat struct {
	Status string `xml:"status"`
	ETag   string `xml:"prop>getetag"`
}

// listCalendarETags returns a map of event-resource href → ETag for a calendar,
// via an etag-only calendar-query REPORT issued with the connector's
// authenticated HTTP client.
func (c *Connector) listCalendarETags(ctx context.Context, calPath string) (map[string]string, error) {
	target, err := resolveURL(c.endpoint, calPath)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "REPORT", target, strings.NewReader(calendarQueryETagBody))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "1")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusMultiStatus {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("REPORT status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var ms davMultistatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("decode multistatus: %w", err)
	}

	out := make(map[string]string, len(ms.Responses))
	for i := range ms.Responses {
		r := ms.Responses[i]
		// Skip empty hrefs and the calendar collection itself (which iCloud
		// includes in the response, href ending in "/"); only object resources
		// are fetchable.
		if r.Href == "" || strings.HasSuffix(r.Href, "/") {
			continue
		}
		etag := ""
		for _, ps := range r.Propstats {
			if strings.Contains(ps.Status, "200") && ps.ETag != "" {
				etag = strings.Trim(ps.ETag, `"`)
			}
		}
		out[r.Href] = etag
	}
	return out, nil
}

// resolveURL resolves a (possibly absolute-path) calendar path against the
// endpoint origin.
func resolveURL(endpoint, path string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	ref, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("parse path: %w", err)
	}
	return base.ResolveReference(ref).String(), nil
}
