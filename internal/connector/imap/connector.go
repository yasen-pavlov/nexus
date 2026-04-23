// Package imap implements a connector for IMAP email servers.
package imap

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline/extractor"
)

// imapBodyFetchBatch caps the number of UIDs fetched per IMAP FETCH
// round-trip. Smaller batches mean more checkpoints (faster resume
// after a cancel) and earlier visible progress; larger batches
// amortize server round-trips. 100 is a pragmatic middle.
const imapBodyFetchBatch = 100

// Per-folder cursor-data key prefixes. Concatenated with the folder
// name at runtime (e.g. "uid:INBOX"). Kept as constants so a typo
// in one use site can't silently split the cursor map.
const (
	cursorKeyUID         = "uid:"
	cursorKeyUIDValidity = "uidvalidity:"
	cursorKeyModSeq      = "modseq:"
)

// mailboxClient abstracts IMAP operations for testability.
type mailboxClient interface {
	// SelectFolder selects a mailbox. Returned SelectData carries
	// UIDValidity and (when the server supports CONDSTORE and the
	// caller opted in) HighestModSeq — the two values the cursor
	// needs for incremental sync.
	SelectFolder(folder string, condStore bool) (*imap.SelectData, error)
	// SearchUIDs returns UIDs matching the criteria.
	SearchUIDs(criteria *imap.SearchCriteria) ([]imap.UID, error)
	// FetchMessages streams messages by UID set, calling yield
	// once per message in server order. opts may be nil to use
	// the default envelope+full-body shape; pass a populated
	// *imap.FetchOptions to layer ChangedSince/ModSeq for
	// CONDSTORE delta fetches. yield returning false stops
	// iteration early (used for ctx cancellation). Streaming
	// matters for large batches on slow servers: iCloud can
	// take several minutes to return 100 messages, and the
	// pipeline relies on per-message emission to show progress
	// and flush indexing buffers while bytes are still arriving.
	FetchMessages(uids []imap.UID, opts *imap.FetchOptions, yield func(*imapclient.FetchMessageBuffer) bool) error
}

// realMailboxClient wraps an imapclient.Client to satisfy mailboxClient.
// hasCondStore gates whether SELECT sends the `(CONDSTORE)` qualifier —
// set once at connection time from the server capability list so we
// don't retry-on-BAD per folder.
type realMailboxClient struct {
	client       *imapclient.Client
	hasCondStore bool
}

func (r *realMailboxClient) SelectFolder(folder string, condStore bool) (*imap.SelectData, error) {
	// Only ask for CONDSTORE metadata when the server advertises
	// support AND the caller wants it. Servers that don't advertise
	// CONDSTORE commonly reject the qualifier outright with a
	// BAD response (e.g. the dovecot minimal test server), so
	// silently degrade to plain SELECT in that case.
	if condStore && r.hasCondStore {
		return r.client.Select(folder, &imap.SelectOptions{CondStore: true}).Wait()
	}
	return r.client.Select(folder, nil).Wait()
}

func (r *realMailboxClient) SearchUIDs(criteria *imap.SearchCriteria) ([]imap.UID, error) {
	data, err := r.client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}
	return data.AllUIDs(), nil
}

// defaultFetchOptions is the envelope + full body MIME-parse shape
// used when the caller passes nil to FetchMessages. Defined as a
// helper so CONDSTORE callers can start from the same baseline and
// only layer ChangedSince on top.
func defaultFetchOptions() *imap.FetchOptions {
	return &imap.FetchOptions{
		Envelope: true,
		UID:      true,
		BodySection: []*imap.FetchItemBodySection{
			{}, // entire message body for MIME parsing
		},
	}
}

func (r *realMailboxClient) FetchMessages(uids []imap.UID, opts *imap.FetchOptions, yield func(*imapclient.FetchMessageBuffer) bool) error {
	if opts == nil {
		opts = defaultFetchOptions()
	}
	uidSet := imap.UIDSetNum(uids...)
	cmd := r.client.Fetch(uidSet, opts)
	defer cmd.Close() //nolint:errcheck // best-effort close
	for {
		msgData := cmd.Next()
		if msgData == nil {
			break
		}
		buf, err := msgData.Collect()
		if err != nil {
			return err
		}
		if !yield(buf) {
			// Caller requested an early stop (usually ctx
			// cancellation). cmd.Close via the deferred call
			// drains the rest of the wire and lets the
			// connection return to a usable state.
			return nil
		}
	}
	return cmd.Close()
}

// dialFunc allows overriding the IMAP connection for testing.
type dialFunc func(address string, options *imapclient.Options) (*imapclient.Client, error)

func init() {
	connector.Register("imap", func() connector.Connector {
		return &Connector{
			port:    993,
			folders: []string{"INBOX"},
			dial:    imapclient.DialTLS,
		}
	})
}

// Connector fetches emails from an IMAP server.
type Connector struct {
	name        string
	server      string
	port        int
	username    string
	password    string
	folders     []string
	syncSince   time.Time
	extractor   *extractor.Registry
	dial        dialFunc
	binaryStore connector.BinaryStoreAPI
	cacheConfig connector.CacheConfig
}

func (c *Connector) Type() string { return "imap" }
func (c *Connector) Name() string { return c.name }

// SetExtractor sets the content extractor for email attachments.
func (c *Connector) SetExtractor(ext *extractor.Registry) {
	c.extractor = ext
}

// SetBinaryStore wires the binary content cache plus the resolved
// per-connector cache policy. Implements connector.BinaryStoreSetter.
// Called once at wire time by ConnectorManager.instantiateConnector.
func (c *Connector) SetBinaryStore(store connector.BinaryStoreAPI, cfg connector.CacheConfig) {
	c.binaryStore = store
	c.cacheConfig = cfg
}

func (c *Connector) Configure(cfg connector.Config) error {
	name, _ := cfg["name"].(string)
	if name == "" {
		name = "imap"
	}
	c.name = name

	server := cfg.StringVal("server")
	if server == "" {
		return fmt.Errorf("imap: server is required")
	}
	c.server = server

	if portStr := cfg.StringVal("port"); portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return fmt.Errorf("imap: invalid port: %w", err)
		}
		c.port = p
	}

	username := cfg.StringVal("username")
	if username == "" {
		return fmt.Errorf("imap: username is required")
	}
	c.username = username

	password := cfg.StringVal("password")
	if password == "" {
		return fmt.Errorf("imap: password is required")
	}
	c.password = password

	if folders := cfg.StringVal("folders"); folders != "" {
		c.folders = nil
		for _, f := range strings.Split(folders, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				c.folders = append(c.folders, f)
			}
		}
	}

	c.syncSince = connector.ComputeSyncSince(cfg)

	return nil
}

func (c *Connector) Validate() error {
	if c.server == "" || c.username == "" || c.password == "" {
		return fmt.Errorf("imap: server, username, and password are required")
	}

	addr := fmt.Sprintf("%s:%d", c.server, c.port)
	client, err := c.dial(addr, &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: c.server},
	})
	if err != nil {
		return fmt.Errorf("imap: cannot connect to %s: %w", addr, err)
	}
	defer client.Close() //nolint:errcheck // best-effort close

	if err := client.Login(c.username, c.password).Wait(); err != nil {
		return fmt.Errorf("imap: authentication failed: %w", err)
	}

	_ = client.Logout().Wait()
	return nil
}

// Fetch streams emails folder-by-folder. For each folder, the connector
// enumerates the current UID list (emitted as lex-sorted SourceID items
// for deletion reconciliation), then fetches the delta bodies in
// batches of imapBodyFetchBatch and emits one Doc per email (plus Docs
// per attachment). A Checkpoint is emitted at the end of every batch
// so a mid-folder cancel loses at most one batch of re-fetch work.
//
// Folders are processed in lexicographic order so the global SourceID
// stream stays monotonic — required for the pipeline's streaming
// merge-diff.
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

// streamFetch is the goroutine body of Fetch. Manages the IMAP session
// lifecycle and dispatches to streamFetchWithClient.
func (c *Connector) streamFetch(ctx context.Context, cursor *model.SyncCursor, items chan<- model.FetchItem) error {
	addr := fmt.Sprintf("%s:%d", c.server, c.port)
	client, err := c.dial(addr, &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: c.server},
	})
	if err != nil {
		return fmt.Errorf("imap: connect: %w", err)
	}
	defer client.Close() //nolint:errcheck // best-effort close

	if err := client.Login(c.username, c.password).Wait(); err != nil {
		return fmt.Errorf("imap: login: %w", err)
	}
	defer func() { _ = client.Logout().Wait() }()

	// Probe CONDSTORE support once per sync — propagates into every
	// realMailboxClient.SelectFolder call so we don't pester servers
	// that don't advertise the capability with unsupported SELECT
	// options (some minimal servers reject the `(CONDSTORE)`
	// qualifier outright with a BAD response).
	mbc := &realMailboxClient{client: client, hasCondStore: client.Caps().Has(imap.CapCondStore)}
	return c.streamFetchWithClient(ctx, mbc, cursor, items)
}

// streamFetchWithClient is the per-folder streaming loop against an
// abstracted mailbox client. Extracted from streamFetch so tests can
// drive it with a mock without needing a live IMAP dial/login.
func (c *Connector) streamFetchWithClient(ctx context.Context, mbc mailboxClient, cursor *model.SyncCursor, items chan<- model.FetchItem) error {
	folders := append([]string(nil), c.folders...)
	sort.Strings(folders)

	now := time.Now()
	cursorData := copyCursorData(cursor)
	totalEstimate := int64(0)

	for _, folder := range folders {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Announce the current folder so the UI can show
		// "Syncing INBOX…" instead of a bare counter. The
		// pipeline just stamps the label onto the SyncJob and
		// fires progress.
		scope := folder
		if !emitItem(ctx, items, model.FetchItem{Scope: &scope}) {
			return ctx.Err()
		}
		if _, err := c.streamFolder(ctx, mbc, folder, cursor, cursorData, now, &totalEstimate, items); err != nil {
			return fmt.Errorf("imap: folder %q: %w", folder, err)
		}
	}
	// Clear the scope once all folders are done so late
	// progress frames don't linger on a stale folder name.
	empty := ""
	_ = emitItem(ctx, items, model.FetchItem{Scope: &empty})
	// Only signal authoritative enumeration if every folder's
	// SEARCH ALL succeeded — a partial run would cause false
	// deletions.
	if !emitItem(ctx, items, model.FetchItem{EnumerationComplete: true}) {
		return ctx.Err()
	}
	return nil
}

// copyCursorData returns a mutable copy of the cursor's CursorData
// (or an empty map when the cursor is nil). Mid-sync checkpoints
// mutate this map and hand a snapshot to the pipeline, so the
// connector owns the only mutable instance for the run.
func copyCursorData(cursor *model.SyncCursor) map[string]any {
	if cursor == nil || cursor.CursorData == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(cursor.CursorData))
	for k, v := range cursor.CursorData {
		out[k] = v
	}
	return out
}

// streamFolder selects a folder, emits its sorted SourceID
// enumeration, and streams delta body fetches in batches. When the
// server supports CONDSTORE (SelectData.HighestModSeq > 0) the
// connector takes the delta via SEARCH MODSEQ and fast-skips folders
// whose HighestModSeq hasn't changed since the last sync — making
// recurring syncs O(delta) instead of O(enumeration).
//
// On UIDValidity change the cached CONDSTORE state is discarded and
// the folder falls back to full enumeration (RFC 7162 §5: the
// server has re-keyed UIDs so cached MODSEQ values are meaningless).
func (c *Connector) streamFolder(ctx context.Context, mbc mailboxClient, folder string, cursor *model.SyncCursor, cursorData map[string]any, startedAt time.Time, totalEstimate *int64, items chan<- model.FetchItem) (int, error) {
	sel, err := mbc.SelectFolder(folder, true /* CondStore */)
	if err != nil {
		return 0, fmt.Errorf("select: %w", err)
	}

	st := resolveCondStoreState(cursor, folder, sel)

	if err := c.emitFolderEnumeration(ctx, mbc, folder, items); err != nil {
		return 0, err
	}

	// CONDSTORE fast-skip on the body-fetch path: if HighestModSeq
	// hasn't advanced since last sync, there's nothing new to fetch.
	// Persist the cursor to keep carrying the value forward and
	// move on without running the delta SEARCH or any body FETCH.
	if st.unchanged() {
		writeFolderCursor(cursorData, folder, st, cursorUIDForFolder(cursor, folder))
		if !emitItem(ctx, items, model.FetchItem{Checkpoint: buildCursor(cursorData, startedAt)}) {
			return 0, ctx.Err()
		}
		return 0, nil
	}

	criteria, fetchOpts := c.buildDeltaCriteria(cursor, folder, st.cachedModSeq, st.newHighestModSeq)
	uids, err := mbc.SearchUIDs(criteria)
	if err != nil {
		return 0, fmt.Errorf("search: %w", err)
	}

	if len(uids) > 0 {
		*totalEstimate += int64(len(uids))
		est := *totalEstimate
		if !emitItem(ctx, items, model.FetchItem{EstimatedTotal: &est}) {
			return 0, ctx.Err()
		}
	}

	if len(uids) == 0 {
		writeFolderCursor(cursorData, folder, st, cursorUIDForFolder(cursor, folder))
		if !emitItem(ctx, items, model.FetchItem{Checkpoint: buildCursor(cursorData, startedAt)}) {
			return 0, ctx.Err()
		}
		return 0, nil
	}

	return c.streamFolderBodies(ctx, mbc, folder, cursor, cursorData, startedAt, st, uids, fetchOpts, items)
}

// emitFolderEnumeration runs SEARCH ALL and streams every UID as a
// lex-sorted SourceID item. Always runs — even when CONDSTORE says
// nothing has changed in this folder — so the pipeline's merge-diff
// compares a globally-sorted stream against OpenSearch. Omitting one
// folder's IDs would make every indexed doc from that folder look
// stale the moment any OTHER folder emits SourceIDs.
func (c *Connector) emitFolderEnumeration(ctx context.Context, mbc mailboxClient, folder string, items chan<- model.FetchItem) error {
	allUIDs, err := mbc.SearchUIDs(&imap.SearchCriteria{})
	if err != nil {
		return fmt.Errorf("search all: %w", err)
	}
	sourceIDs := make([]string, len(allUIDs))
	for i, uid := range allUIDs {
		sourceIDs[i] = fmt.Sprintf("%s:%d", folder, uid)
	}
	sort.Strings(sourceIDs)
	for i := range sourceIDs {
		sid := sourceIDs[i]
		if !emitItem(ctx, items, model.FetchItem{SourceID: &sid}) {
			return ctx.Err()
		}
	}
	return nil
}

// streamFolderBodies iterates the delta UIDs in
// imapBodyFetchBatch-sized windows, streaming per-message docs as
// the server returns them and checkpointing after each batch so a
// mid-folder cancel only loses one batch of re-fetch work.
func (c *Connector) streamFolderBodies(ctx context.Context, mbc mailboxClient, folder string, cursor *model.SyncCursor, cursorData map[string]any, startedAt time.Time, st condStoreState, uids []imap.UID, fetchOpts *imap.FetchOptions, items chan<- model.FetchItem) (int, error) {
	processed := 0
	maxUID := cursorUIDForFolder(cursor, folder)
	for i := 0; i < len(uids); i += imapBodyFetchBatch {
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		end := i + imapBodyFetchBatch
		if end > len(uids) {
			end = len(uids)
		}
		batch := uids[i:end]
		// Stream per-message: iCloud and other IMAP servers can
		// take several minutes to return a large batch. Emitting
		// docs as they arrive lets the pipeline's progress bar
		// advance mid-batch instead of looking stuck at 0 for the
		// whole batch duration.
		batchProcessed, err := c.streamFolderBatch(ctx, mbc, folder, batch, fetchOpts, &maxUID, items)
		processed += batchProcessed
		if err != nil {
			return processed, err
		}
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		writeFolderCursor(cursorData, folder, st, maxUID)
		if !emitItem(ctx, items, model.FetchItem{Checkpoint: buildCursor(cursorData, startedAt)}) {
			return processed, ctx.Err()
		}
	}
	return processed, nil
}

// streamFolderBatch drives one FETCH round-trip, yielding per-
// message. Caller advances maxUID via the shared pointer.
func (c *Connector) streamFolderBatch(ctx context.Context, mbc mailboxClient, folder string, uids []imap.UID, fetchOpts *imap.FetchOptions, maxUID *imap.UID, items chan<- model.FetchItem) (int, error) {
	processed := 0
	emitErr := mbc.FetchMessages(uids, fetchOpts, func(msg *imapclient.FetchMessageBuffer) bool {
		if ctx.Err() != nil {
			return false
		}
		if msg.UID > *maxUID {
			*maxUID = msg.UID
		}
		docs := c.messageToDocuments(msg, folder)
		for k := range docs {
			if !emitItem(ctx, items, model.FetchItem{Doc: &docs[k]}) {
				return false
			}
		}
		processed++
		return true
	})
	if emitErr != nil {
		return processed, fmt.Errorf("fetch: %w", emitErr)
	}
	return processed, nil
}

// condStoreState bundles the cached + server-reported CONDSTORE
// values for a folder so we can pass them around as a value and
// keep streamFolder's signature sane.
type condStoreState struct {
	cachedUIDValidity uint32
	cachedModSeq      uint64
	newUIDValidity    uint32
	newHighestModSeq  uint64
}

// unchanged reports whether the folder can be body-fetch-skipped:
// the server advertises CONDSTORE (HighestModSeq > 0), our cached
// HighestModSeq matches, and UIDVALIDITY didn't rotate.
func (s condStoreState) unchanged() bool {
	return s.newHighestModSeq > 0 &&
		s.cachedModSeq == s.newHighestModSeq &&
		s.cachedUIDValidity == s.newUIDValidity
}

// resolveCondStoreState reads the cursor's cached values and
// applies RFC 7162 §5: a UIDVALIDITY change invalidates every
// cached MODSEQ value, so we drop cachedModSeq and force a full
// re-fetch from scratch.
func resolveCondStoreState(cursor *model.SyncCursor, folder string, sel *imap.SelectData) condStoreState {
	s := condStoreState{
		cachedUIDValidity: cursorUIDValidityForFolder(cursor, folder),
		cachedModSeq:      cursorModSeqForFolder(cursor, folder),
		newUIDValidity:    sel.UIDValidity,
		newHighestModSeq:  sel.HighestModSeq,
	}
	if s.cachedUIDValidity != 0 && s.cachedUIDValidity != s.newUIDValidity {
		s.cachedModSeq = 0
	}
	return s
}

// writeFolderCursor centralises the per-folder cursor write so the
// "uid:" / "uidvalidity:" / "modseq:" keys stay in sync. modseq is
// omitted when the server doesn't advertise CONDSTORE (ModSeq == 0)
// so we don't accidentally pin a zero value into the map.
func writeFolderCursor(cursorData map[string]any, folder string, st condStoreState, uid imap.UID) {
	cursorData[cursorKeyUIDValidity+folder] = float64(st.newUIDValidity)
	if st.newHighestModSeq > 0 {
		cursorData[cursorKeyModSeq+folder] = float64(st.newHighestModSeq)
	}
	cursorData[cursorKeyUID+folder] = float64(uid)
}

// buildDeltaCriteria chooses the SEARCH criteria + FETCH options for
// the delta pass. When CONDSTORE is available and we have a cached
// HighestModSeq, we ask the server for "UIDs with MODSEQ > cached"
// — O(delta) regardless of mailbox size. Otherwise we fall back to
// the UID-range heuristic (UID > lastUID) that predates CONDSTORE.
func (c *Connector) buildDeltaCriteria(cursor *model.SyncCursor, folder string, cachedModSeq, newHighestModSeq uint64) (*imap.SearchCriteria, *imap.FetchOptions) {
	opts := defaultFetchOptions()
	if newHighestModSeq > 0 && cachedModSeq > 0 {
		opts.ChangedSince = cachedModSeq
		return &imap.SearchCriteria{
			ModSeq: &imap.SearchCriteriaModSeq{ModSeq: cachedModSeq},
		}, opts
	}
	lastUID := cursorUIDForFolder(cursor, folder)
	return buildFolderSearchCriteria(lastUID, c.syncSince), opts
}

// cursorUIDValidityForFolder reads the persisted UIDVALIDITY for
// folder out of cursor.CursorData. Zero indicates an absent entry;
// callers treat that as "no cached validity yet" rather than a real
// UIDVALIDITY=0 (IMAP requires nonzero UIDVALIDITY).
func cursorUIDValidityForFolder(cursor *model.SyncCursor, folder string) uint32 {
	if cursor == nil {
		return 0
	}
	v, ok := cursor.CursorData[cursorKeyUIDValidity+folder].(float64)
	if !ok {
		return 0
	}
	return uint32(v)
}

// cursorModSeqForFolder reads the persisted HIGHESTMODSEQ for folder
// out of cursor.CursorData. Zero indicates an absent entry or a
// non-CONDSTORE server; callers fall back to UID-range delta in
// that case.
func cursorModSeqForFolder(cursor *model.SyncCursor, folder string) uint64 {
	if cursor == nil {
		return 0
	}
	v, ok := cursor.CursorData[cursorKeyModSeq+folder].(float64)
	if !ok {
		return 0
	}
	return uint64(v)
}

// buildCursor captures the current cursorData snapshot into a
// SyncCursor shaped for persistence. The map is copied so the
// pipeline's persistence path doesn't race with in-progress updates
// from the next folder.
func buildCursor(cursorData map[string]any, startedAt time.Time) *model.SyncCursor {
	snapshot := make(map[string]any, len(cursorData))
	for k, v := range cursorData {
		snapshot[k] = v
	}
	return &model.SyncCursor{
		CursorData: snapshot,
		LastSync:   startedAt,
		LastStatus: "success",
	}
}

// cursorUIDForFolder reads the previously-persisted last-seen UID for folder
// out of a sync cursor, returning 0 for an absent/malformed entry.
func cursorUIDForFolder(cursor *model.SyncCursor, folder string) imap.UID {
	if cursor == nil {
		return 0
	}
	uidVal, ok := cursor.CursorData[cursorKeyUID+folder].(float64)
	if !ok {
		return 0
	}
	return imap.UID(uidVal)
}

// buildFolderSearchCriteria builds the IMAP SEARCH criteria for fetching
// messages strictly newer than lastUID. If there's no cursor yet but the
// connector has a `syncSince` cutoff, falls back to a Since date filter.
func buildFolderSearchCriteria(lastUID imap.UID, syncSince time.Time) *imap.SearchCriteria {
	criteria := &imap.SearchCriteria{}
	if lastUID > 0 {
		uidSet := imap.UIDSet{}
		uidSet.AddRange(lastUID+1, 0) // 0 means * (all)
		criteria.UID = []imap.UIDSet{uidSet}
	} else if !syncSince.IsZero() {
		criteria.Since = syncSince
	}
	return criteria
}

// emitItem sends item on items, respecting context cancellation.
// Returns false when the context was cancelled before the send could
// complete. Shared helper so cancellation semantics stay uniform
// across the connector's streaming paths.
func emitItem(ctx context.Context, items chan<- model.FetchItem, item model.FetchItem) bool {
	select {
	case items <- item:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *Connector) messageToDocuments(msg *imapclient.FetchMessageBuffer, folder string) []model.Document {
	if msg.Envelope == nil {
		return nil
	}

	env := msg.Envelope

	// Get the raw body for MIME parsing
	var bodyData []byte
	for _, section := range msg.BodySection {
		bodyData = section.Bytes
		break
	}

	textContent, attachments := parseEmailBody(bodyData)
	// If MIME parsing fails to extract any plain text we used to fall back to
	// the raw RFC822 source. That's almost guaranteed to contain large base64
	// blobs and other non-text noise — useless for search and prone to blowing
	// past embedding-API token limits. Skip these messages instead.

	metadata := buildEmailMetadata(env, folder, attachments)

	// Build URL using mid: URI scheme
	var msgURL string
	if env.MessageID != "" {
		msgURL = "mid:" + env.MessageID
	}

	createdAt := env.Date
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	emailRelations := buildEmailRelations(env, bodyData)

	// Main email document. Subject comes off the IMAP ENVELOPE raw —
	// RFC 2047 encoded-word subjects (common for non-ASCII European
	// mail) need decoding before they're useful for BM25 or the UI.
	decodedSubject := decodeHeader(env.Subject)
	emailSourceID := fmt.Sprintf("%s:%d", folder, msg.UID)
	emailDocID := model.DocumentID("imap", c.name, emailSourceID)
	docs := []model.Document{{
		ID:            emailDocID,
		SourceType:    "imap",
		SourceName:    c.name,
		SourceID:      emailSourceID,
		Title:         decodedSubject,
		Content:       textContent,
		Metadata:      metadata,
		Relations:     emailRelations,
		IMAPMessageID: strings.Trim(env.MessageID, " <>"),
		URL:           msgURL,
		Visibility:    "private",
		CreatedAt:     createdAt,
	}}

	for i, att := range attachments {
		docs = append(docs, c.attachmentDocument(att, i, folder, msg.UID, decodedSubject, emailSourceID, emailDocID, msgURL, createdAt))
	}

	return docs
}

// buildEmailMetadata assembles the per-email metadata map (folder, message-id,
// date, addresses, and attachment filenames).
func buildEmailMetadata(env *imap.Envelope, folder string, attachments []attachment) map[string]any {
	metadata := map[string]any{
		"folder":     folder,
		"message_id": env.MessageID,
		"date":       env.Date.Format(time.RFC3339),
	}
	if len(env.From) > 0 {
		metadata["from"] = formatAddresses(env.From)
	}
	if len(env.To) > 0 {
		metadata["to"] = formatAddresses(env.To)
	}
	if len(env.Cc) > 0 {
		metadata["cc"] = formatAddresses(env.Cc)
	}
	if len(attachments) > 0 {
		metadata["has_attachments"] = true
		names := make([]string, 0, len(attachments))
		for _, a := range attachments {
			if a.Filename != "" {
				names = append(names, a.Filename)
			}
		}
		if len(names) > 0 {
			metadata["attachment_filenames"] = names
		}
	}
	return metadata
}

// buildEmailRelations derives thread + reply edges from the IMAP envelope and
// the raw References: header. env.InReplyTo is the direct parent; the first
// entry in References is treated as the canonical thread root so every
// message in the thread points at the same target.
func buildEmailRelations(env *imap.Envelope, bodyData []byte) []model.Relation {
	var relations []model.Relation
	inReplyTo := ""
	if len(env.InReplyTo) > 0 {
		inReplyTo = strings.Trim(env.InReplyTo[0], " <>")
	}
	if inReplyTo != "" {
		relations = append(relations, model.Relation{
			Type:           model.RelationReplyTo,
			TargetSourceID: inReplyTo,
		})
	}
	references := parseReferencesHeader(bodyData)
	threadRoot := ""
	switch {
	case len(references) > 0:
		threadRoot = references[0]
	case inReplyTo != "":
		threadRoot = inReplyTo
	}
	if threadRoot != "" {
		relations = append(relations, model.Relation{
			Type:           model.RelationMemberOfThread,
			TargetSourceID: threadRoot,
		})
	}
	return relations
}

// attachmentDocument builds the Document for a single attachment and, when
// configured, eagerly populates the binary cache with the bytes already in
// memory.
func (c *Connector) attachmentDocument(att attachment, index int, folder string, uid imap.UID, parentSubject, emailSourceID string, emailDocID uuid.UUID, msgURL string, createdAt time.Time) model.Document {
	var extracted string
	if c.extractor != nil && c.extractor.CanExtract(att.ContentType) {
		if out, err := c.extractor.Extract(context.Background(), att.ContentType, att.Data); err == nil {
			extracted = out
		}
	}

	attMetadata := map[string]any{
		"parent_subject": parentSubject,
		"content_type":   att.ContentType,
	}
	if att.Filename != "" {
		attMetadata["filename"] = att.Filename
	}

	attSourceID := fmt.Sprintf("%s:%d:attachment:%d", folder, uid, index)
	doc := model.Document{
		ID:         model.DocumentID("imap", c.name, attSourceID),
		SourceType: "imap",
		SourceName: c.name,
		SourceID:   attSourceID,
		Title:      att.Filename,
		Content:    extracted,
		MimeType:   att.ContentType,
		Size:       int64(len(att.Data)),
		Metadata:   attMetadata,
		Relations: []model.Relation{{
			Type:           model.RelationAttachmentOf,
			TargetSourceID: emailSourceID,
			TargetID:       emailDocID.String(),
		}},
		URL:        msgURL,
		Visibility: "private",
		CreatedAt:  createdAt,
	}

	if c.binaryStore != nil && c.cacheConfig.Mode == "eager" {
		_ = c.binaryStore.Put(
			context.Background(),
			"imap", c.name, attSourceID,
			bytes.NewReader(att.Data),
			int64(len(att.Data)),
		)
	}
	return doc
}

func formatAddresses(addrs []imap.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		// Display-name fields are routinely RFC 2047 encoded when the
		// sender's name contains non-ASCII (e.g.
		// `=?UTF-8?Q?J=C3=BCrgen_M=C3=BCller?=`). Decode to UTF-8 so
		// metadata search + UI rendering are readable.
		name := decodeHeader(a.Name)
		if name != "" {
			parts = append(parts, fmt.Sprintf("%s <%s>", name, a.Addr()))
		} else {
			parts = append(parts, a.Addr())
		}
	}
	return strings.Join(parts, ", ")
}
