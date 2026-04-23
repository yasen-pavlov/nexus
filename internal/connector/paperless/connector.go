// Package paperless implements a connector for Paperless-ngx document management system.
package paperless

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
)

// authTokenPrefix is the scheme used by the Paperless-ngx API for
// token-based authentication on the Authorization header.
const authTokenPrefix = "Token "

// paperlessCheckpointEvery matches the pipeline's bulk-index cadence —
// we emit a checkpoint every N docs so a crash replays at most N docs
// on the next run.
const paperlessCheckpointEvery = 200

func init() {
	connector.Register("paperless", func() connector.Connector {
		return &Connector{client: &http.Client{Timeout: 30 * time.Second}}
	})
}

// Connector fetches documents from a Paperless-ngx instance.
type Connector struct {
	name      string
	baseURL   string
	token     string
	syncSince time.Time
	client    *http.Client
}

func (c *Connector) Type() string { return "paperless" }
func (c *Connector) Name() string { return c.name }

func (c *Connector) Configure(cfg connector.Config) error {
	name, _ := cfg["name"].(string)
	if name == "" {
		name = "paperless"
	}
	c.name = name

	baseURL, _ := cfg["url"].(string)
	if baseURL == "" {
		return fmt.Errorf("paperless: url is required")
	}
	c.baseURL = strings.TrimRight(baseURL, "/")

	token, _ := cfg["token"].(string)
	if token == "" {
		return fmt.Errorf("paperless: token is required")
	}
	c.token = token
	c.syncSince = connector.ComputeSyncSince(cfg)

	return nil
}

func (c *Connector) Validate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/documents/?page=1&page_size=1", nil)
	if err != nil {
		return fmt.Errorf("paperless: %w", err)
	}
	req.Header.Set("Authorization", authTokenPrefix+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("paperless: cannot connect to %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("paperless: authentication failed (status %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("paperless: unexpected status %d from %s", resp.StatusCode, c.baseURL)
	}

	return nil
}

// Fetch streams paperless docs in two phases: first the full set of
// current document IDs (enumerated via a lightweight `?fields=id` scan
// and sorted lexicographically to match OpenSearch source_id keyword
// sort), then the cursor-filtered docs themselves. The pipeline treats
// enumeration + doc-emission as one stream for merge-diff deletion
// reconciliation — order within the SourceID phase must be globally
// lex-sorted, so we accumulate all IDs before emission rather than
// emitting per-page (page N+1 can contain numeric IDs that lex-sort
// before page N's IDs — e.g. "1001" vs "999").
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

// streamFetch is the goroutine body of Fetch. Returns an error to
// surface via the errs channel or nil on clean completion. All
// emissions are ctx-aware: a cancelled context short-circuits the
// loop and returns ctx.Err(). The work splits into three sequential
// phases — lookup fetch, SourceID enumeration, doc pagination — each
// extracted to its own helper so this function stays readable.
func (c *Connector) streamFetch(ctx context.Context, cursor *model.SyncCursor, items chan<- model.FetchItem) error {
	tags, correspondents, docTypes, err := c.fetchLookupTables(ctx)
	if err != nil {
		return err
	}
	if err := c.emitEnumeration(ctx, items); err != nil {
		return err
	}
	return c.streamPaginatedDocs(ctx, cursor, tags, correspondents, docTypes, items)
}

// fetchLookupTables pulls the three Paperless lookup endpoints the
// toDocument pass needs (tags, correspondents, document_types).
// Any 4xx/5xx on any of them aborts the sync — without these
// lookups we'd emit opaque numeric IDs instead of human labels.
func (c *Connector) fetchLookupTables(ctx context.Context) (tags, correspondents, docTypes map[int]string, err error) {
	tags, err = c.fetchLookup(ctx, "/api/tags/")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("paperless: fetch tags: %w", err)
	}
	correspondents, err = c.fetchLookup(ctx, "/api/correspondents/")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("paperless: fetch correspondents: %w", err)
	}
	docTypes, err = c.fetchLookup(ctx, "/api/document_types/")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("paperless: fetch document types: %w", err)
	}
	return tags, correspondents, docTypes, nil
}

// emitEnumeration streams every indexed Paperless doc ID as a
// SourceID item, sorted lex so the pipeline's merge-diff sees them
// in OpenSearch keyword-sort order. Enumeration failure is NOT
// fatal: we opt out of reconciliation for this run (no SourceIDs,
// no EnumerationComplete) but let the incremental fetch still
// index whatever is new.
func (c *Connector) emitEnumeration(ctx context.Context, items chan<- model.FetchItem) error {
	ids, err := c.enumerateAllIDs(ctx)
	if err != nil {
		c.logEnumerationFailure(err)
		return nil
	}
	sort.Strings(ids)
	for i := range ids {
		sid := ids[i]
		if !emitItem(ctx, items, model.FetchItem{SourceID: &sid}) {
			return ctx.Err()
		}
	}
	if !emitItem(ctx, items, model.FetchItem{EnumerationComplete: true}) {
		return ctx.Err()
	}
	if len(ids) > 0 {
		estimate := int64(len(ids))
		_ = emitItem(ctx, items, model.FetchItem{EstimatedTotal: &estimate})
	}
	return nil
}

// streamPaginatedDocs walks the `/api/documents/?modified__gt=...`
// pagination, emitting each doc as a FetchItem and a Checkpoint
// every paperlessCheckpointEvery docs so a cancel loses at most
// that many docs of re-fetch work.
func (c *Connector) streamPaginatedDocs(ctx context.Context, cursor *model.SyncCursor, tags, correspondents, docTypes map[int]string, items chan<- model.FetchItem) error {
	fetchURL := c.initialDocsURL(cursor)
	now := time.Now()
	emitted := 0
	for fetchURL != "" {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		page, nextURL, err := c.fetchPage(ctx, fetchURL)
		if err != nil {
			return fmt.Errorf("paperless: fetch page: %w", err)
		}
		for _, pdoc := range page {
			doc := c.toDocument(pdoc, tags, correspondents, docTypes)
			if !emitItem(ctx, items, model.FetchItem{Doc: &doc}) {
				return ctx.Err()
			}
			emitted++
			if emitted%paperlessCheckpointEvery == 0 {
				if !emitItem(ctx, items, model.FetchItem{Checkpoint: newPaperlessCursor(now)}) {
					return ctx.Err()
				}
			}
		}
		fetchURL = nextURL
	}
	// Final checkpoint so last_sync_time advances even when the
	// delta was empty or didn't land exactly on an every-N
	// boundary.
	_ = emitItem(ctx, items, model.FetchItem{Checkpoint: newPaperlessCursor(now)})
	return nil
}

// initialDocsURL builds the first-page URL for the incremental
// docs fetch, layering in `modified__gt` from either the persisted
// cursor or the connector's syncSince cutoff.
func (c *Connector) initialDocsURL(cursor *model.SyncCursor) string {
	params := url.Values{}
	params.Set("ordering", "modified")
	params.Set("page_size", "100")
	if cursor != nil {
		if ts, ok := cursor.CursorData["last_sync_time"].(string); ok {
			params.Set("modified__gt", ts)
		}
	} else if !c.syncSince.IsZero() {
		params.Set("modified__gt", c.syncSince.Format(time.RFC3339))
	}
	return c.baseURL + "/api/documents/?" + params.Encode()
}

// logEnumerationFailure is extracted to a method so future refactors
// can surface the error into structured logs or metrics without
// touching the main Fetch flow.
func (c *Connector) logEnumerationFailure(_ error) {
	// Connector-level logging isn't wired; failure is already
	// implicit in "no SourceID emissions this run". Pipeline will
	// skip deletion reconciliation, and the next sync gets another
	// shot. Kept as a method so instrumentation can be added later
	// without changing the caller.
}

// emitItem sends an item on items, respecting context cancellation.
// Returns false when the context was cancelled before the send could
// complete. All Fetch goroutines use this helper rather than writing
// to the channel directly so shutdown semantics stay consistent.
func emitItem(ctx context.Context, items chan<- model.FetchItem, item model.FetchItem) bool {
	select {
	case items <- item:
		return true
	case <-ctx.Done():
		return false
	}
}

// newPaperlessCursor builds a cursor payload with last_sync_time set to
// the run's start time. Captured once at the top of Fetch so every
// mid-run checkpoint persists the same value — a later incremental
// sync asks Paperless for `modified__gt={startedAt}` and picks up
// anything touched during or after the previous run.
func newPaperlessCursor(startedAt time.Time) *model.SyncCursor {
	return &model.SyncCursor{
		CursorData: map[string]any{
			"last_sync_time": startedAt.Format(time.RFC3339Nano),
		},
		LastSync:   startedAt,
		LastStatus: "success",
	}
}

// enumerateAllIDs paginates Paperless's `/api/documents/?fields=id`
// to collect every document ID currently present, returned as
// connector source_ids. Pulled in a single unfiltered pass because
// the cursor scopes the main fetch to changed docs only — deletion
// reconciliation needs the unscoped full set.
//
// Any HTTP error or pagination failure aborts and returns the error;
// the caller elides SourceID emissions for this run (opting out of
// deletion reconciliation) rather than risking a partial list.
func (c *Connector) enumerateAllIDs(ctx context.Context) ([]string, error) {
	params := url.Values{}
	params.Set("fields", "id")
	params.Set("page_size", "1000")
	params.Set("ordering", "id")
	fetchURL := c.baseURL + "/api/documents/?" + params.Encode()

	var ids []string
	for fetchURL != "" {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pageIDs, next, err := c.fetchIDPage(ctx, fetchURL)
		if err != nil {
			return nil, err
		}
		ids = append(ids, pageIDs...)
		fetchURL = next
	}
	return ids, nil
}

// paperlessIDPage is a trimmed Paperless paginated response containing just
// document IDs — used by enumerateAllIDs for deletion sync.
type paperlessIDPage struct {
	Results []struct {
		ID int `json:"id"`
	} `json:"results"`
	Next *string `json:"next"`
}

// fetchIDPage retrieves a single page of document IDs and returns the IDs plus
// the URL of the next page (empty string when done).
func (c *Connector) fetchIDPage(ctx context.Context, pageURL string) ([]string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", authTokenPrefix+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var page paperlessIDPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, "", err
	}
	ids := make([]string, 0, len(page.Results))
	for _, r := range page.Results {
		ids = append(ids, strconv.Itoa(r.ID))
	}
	next := ""
	if page.Next != nil {
		next = *page.Next
	}
	return ids, next, nil
}

// paperlessDoc represents a document from the Paperless API.
type paperlessDoc struct {
	ID               int       `json:"id"`
	Title            string    `json:"title"`
	Content          string    `json:"content"`
	Correspondent    *int      `json:"correspondent"`
	DocumentType     *int      `json:"document_type"`
	Tags             []int     `json:"tags"`
	OriginalFileName string    `json:"original_file_name"`
	Created          string    `json:"created"` // document date (YYYY-MM-DD), when originally issued
	Added            time.Time `json:"added"`   // import date, when scanned into Paperless
	Modified         time.Time `json:"modified"`
}

type paginatedResponse struct {
	Count    int            `json:"count"`
	Next     *string        `json:"next"`
	Previous *string        `json:"previous"`
	Results  []paperlessDoc `json:"results"`
}

func (c *Connector) fetchPage(ctx context.Context, pageURL string) ([]paperlessDoc, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", authTokenPrefix+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var page paginatedResponse
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, "", fmt.Errorf("decode response: %w", err)
	}

	nextURL := ""
	if page.Next != nil {
		nextURL = *page.Next
	}

	return page.Results, nextURL, nil
}

type lookupItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type lookupResponse struct {
	Results []lookupItem `json:"results"`
	Next    *string      `json:"next"`
}

func (c *Connector) fetchLookup(ctx context.Context, path string) (map[int]string, error) {
	lookup := make(map[int]string)
	fetchURL := c.baseURL + path + "?page_size=1000"

	for fetchURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", authTokenPrefix+c.token)

		resp, err := c.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close() //nolint:errcheck // HTTP response body

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, path)
		}

		var page lookupResponse
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			return nil, err
		}

		for _, item := range page.Results {
			lookup[item.ID] = item.Name
		}

		fetchURL = ""
		if page.Next != nil {
			fetchURL = *page.Next
		}
	}

	return lookup, nil
}

func (c *Connector) toDocument(pdoc paperlessDoc, tags, correspondents, docTypes map[int]string) model.Document {
	// Resolve tag names
	tagNames := make([]string, 0, len(pdoc.Tags))
	for _, tagID := range pdoc.Tags {
		if name, ok := tags[tagID]; ok {
			tagNames = append(tagNames, name)
		}
	}

	metadata := map[string]any{
		"original_file_name": pdoc.OriginalFileName,
		"tags":               tagNames,
		"added":              pdoc.Added.Format(time.RFC3339),
		"modified":           pdoc.Modified.Format(time.RFC3339),
	}

	if pdoc.Correspondent != nil {
		if name, ok := correspondents[*pdoc.Correspondent]; ok {
			metadata["correspondent"] = name
		}
	}
	if pdoc.DocumentType != nil {
		if name, ok := docTypes[*pdoc.DocumentType]; ok {
			metadata["document_type"] = name
		}
	}

	// Use document date (when originally issued), fall back to import date
	docDate := pdoc.Added
	if pdoc.Created != "" {
		if parsed, err := time.Parse("2006-01-02", pdoc.Created); err == nil {
			docDate = parsed
		}
	}

	sourceID := strconv.Itoa(pdoc.ID)
	return model.Document{
		ID:         model.DocumentID("paperless", c.name, sourceID),
		SourceType: "paperless",
		SourceName: c.name,
		SourceID:   sourceID,
		Title:      pdoc.Title,
		Content:    pdoc.Content,
		Metadata:   metadata,
		URL:        fmt.Sprintf("%s/documents/%d/details", c.baseURL, pdoc.ID),
		Visibility: "private",
		CreatedAt:  docDate,
	}
}
