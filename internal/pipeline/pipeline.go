// Package pipeline orchestrates the ingestion of documents from connectors into the store.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/chunking"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/embedding"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// minEmbeddingContentLen is a cost-saving char-count cap on which chunks get
// an embedding API call. Currently disabled (0) — the real noise filter is
// minEmbeddingAlphabeticTokens below, which is content-aware in a way char
// count never can be. Set non-zero only if you need to save embedding API
// spend on very short chunks.
const minEmbeddingContentLen = 0

// minEmbeddingAlphabeticTokens skips embedding for chunks whose content has
// fewer than this many alphabetic tokens (whitespace-separated tokens with at
// least 2 alphabetic characters). The chunk is still indexed for BM25 search;
// it just doesn't contribute a vector to the kNN side of hybrid search.
//
// Why: low-information chunks produce noisy embeddings that cluster broadly
// in vector space and dominate kNN results for unrelated queries (the
// "noise hub" problem). The fix is to never embed them in the first place.
// 10 tokens is roughly "a meaningful sentence" — short chat acknowledgments
// ("ok", "thanks!"), URL-only chunks, and base64-only fragments will fall
// below this threshold and get skipped.
const minEmbeddingAlphabeticTokens = 10

// indexBatchSize controls bulk-index flushing and coincides with the
// checkpoint cadence. A checkpoint emission in the stream flushes any
// buffered docs first, so cursor advancement always follows durable
// OpenSearch writes — never precedes them.
//
// Kept modest because embeddings are computed per-doc (serial
// Voyage/Cohere round-trips) inside the flush. A 200-doc batch on a
// slow embedder would stall the entire pipeline for minutes while
// the UI sat at 0%. Smaller batches → tighter progress feedback and
// less back-pressure on the connector goroutine.
const indexBatchSize = 25

// checkpointInterval is the time-based fallback cadence for persisting
// cursor state when the connector's own Checkpoint emissions are sparse
// (e.g. slow-streaming connectors that spend minutes between docs).
const checkpointInterval = 30 * time.Second

// EmbedderProvider returns the current embedder (supports hot-reload).
type EmbedderProvider interface {
	Get() embedding.Embedder
}

// SyncReport contains the results of a sync operation.
type SyncReport struct {
	ConnectorName string        `json:"connector_name"`
	ConnectorType string        `json:"connector_type"`
	DocsProcessed int           `json:"docs_processed"`
	DocsDeleted   int           `json:"docs_deleted"`
	Errors        int           `json:"errors"`
	Duration      time.Duration `json:"duration"`
}

// binaryStoreDeleter is the subset of *storage.BinaryStore the pipeline
// needs for deletion-sync cascade. Defined locally as an interface so
// the pipeline doesn't have to drag the storage package into its tests.
type binaryStoreDeleter interface {
	Delete(ctx context.Context, sourceType, sourceName, sourceID string) error
}

// Pipeline orchestrates fetching documents and indexing them.
type Pipeline struct {
	store       *store.Store
	search      *search.Client
	embeddings  EmbedderProvider
	binaryStore binaryStoreDeleter
	log         *zap.Logger
}

// New creates a new Pipeline. embeddings can be nil for BM25-only mode.
func New(store *store.Store, search *search.Client, embeddings EmbedderProvider, log *zap.Logger) *Pipeline {
	return &Pipeline{store: store, search: search, embeddings: embeddings, log: log}
}

// SetBinaryStore wires the binary cache so deletion-sync can purge
// cached blobs alongside the index entries. Optional — pipelines
// without one just skip the cascade. Called once at startup; not
// safe to call concurrently with Run.
func (p *Pipeline) SetBinaryStore(bs binaryStoreDeleter) {
	p.binaryStore = bs
}

// ProgressFunc is called with (total, processed, errors, scope) as
// documents are indexed. Total is a running estimate — connectors
// may emit EstimatedTotal multiple times during a sync, so UI
// consumers should clamp any displayed value so it never regresses.
// Scope is a free-form label describing the connector's current
// sub-unit (IMAP folder name, Telegram chat title, etc.); empty
// string means "no scope".
type ProgressFunc func(total, processed, errors int, scope string)

// runState holds the mutable state threaded through a single
// RunWithProgress call. Kept as a struct rather than individual locals
// so helper methods can manipulate it without long parameter lists.
type runState struct {
	connectorID    uuid.UUID
	connName       string
	connType       string
	ownerID        string
	shared         bool
	progress       ProgressFunc
	start          time.Time
	pendingDocs    []model.Document
	enumSourceIDs  []string
	sawEnumeration bool
	latestCursor   *model.SyncCursor
	total          int
	// hasEstimate flips true the first time a connector emits
	// EstimatedTotal. Until then the pipeline leaves total at 0
	// so the UI renders the indeterminate (gliding bead) state
	// instead of a permanently-full bar where total auto-tracks
	// processed. Filesystem and Telegram don't know their real
	// denominator up front; showing "24/24" would pretend
	// otherwise.
	hasEstimate bool
	processed   int
	errCount    int
	scope       string
	lastFlush   time.Time

	// Async flush coordination. Flushes run on their own
	// goroutines so the consumer loop keeps advancing
	// state.processed (and firing progress) while embeddings +
	// bulk indexing happen in the background. flushSem caps
	// concurrent flushes so a slow embedder can't spawn
	// unbounded goroutines; the blocking acquire is the
	// natural back-pressure. flushWG tracks outstanding
	// flushes so the pipeline can wait for them before
	// persisting checkpoints or finishing the run.
	// reportMu serializes writes to the progress-visible
	// counters (processed, errCount, total) and the progress
	// callback itself, since multiple flush goroutines can
	// roll back concurrently.
	flushSem chan struct{}
	flushWG  sync.WaitGroup
	reportMu sync.Mutex
}

// RunWithProgress fetches documents from a connector, chunks them, generates
// embeddings, and indexes them. connectorID identifies the connector in the
// sync_cursors table; ownerID and shared are written to each indexed chunk
// for search scoping. progress may be nil.
//
// The connector streams FetchItems on one channel and at most one terminal
// error on the other. Docs are buffered and indexed in bulk every
// indexBatchSize items or when the connector emits a Checkpoint (whichever
// comes first), plus a time-based fallback flush every checkpointInterval
// for slow connectors. Deletion reconciliation (for connectors that emit
// SourceID items) runs once the stream closes normally.
func (p *Pipeline) RunWithProgress(ctx context.Context, connectorID uuid.UUID, conn connector.Connector, ownerID string, shared bool, progress ProgressFunc) (*SyncReport, error) {
	state := &runState{
		connectorID: connectorID,
		connName:    conn.Name(),
		connType:    conn.Type(),
		ownerID:     ownerID,
		shared:      shared,
		progress:    progress,
		start:       time.Now(),
		lastFlush:   time.Now(),
		// Cap concurrent flushes at 2: one currently talking
		// to the embedder / OpenSearch, one queued ready to go
		// the moment the first finishes. More concurrency
		// doesn't help because Voyage rate-limits at the
		// account level and OpenSearch bulk writes are fast
		// enough to stay off the hot path.
		flushSem: make(chan struct{}, 2),
	}

	p.log.Info("sync started", zap.String("connector", state.connName), zap.String("type", state.connType))

	if p.store == nil {
		return nil, fmt.Errorf("pipeline: store not configured")
	}

	cursor, err := p.store.GetSyncCursor(ctx, connectorID)
	if err != nil {
		return nil, fmt.Errorf("pipeline: get cursor: %w", err)
	}

	items, errs := conn.Fetch(ctx, cursor)
	streamErr := p.consumeStream(ctx, state, items, errs)

	// Flush whatever is buffered whether or not the stream errored —
	// partial progress is better than none, and the checkpoint we
	// persist reflects only the docs that actually landed in OpenSearch.
	p.flushPending(ctx, state)
	// Wait for every in-flight async flush before persisting the
	// cursor or running deletion reconciliation. Otherwise we could
	// commit a cursor past documents that aren't in OpenSearch yet
	// (defeating the streaming-checkpoint resume invariant) or
	// delete indexed docs that a late flush is about to produce.
	state.flushWG.Wait()
	p.persistLatestCursor(ctx, state)

	deletedCount := 0
	if streamErr == nil && ctx.Err() == nil && state.sawEnumeration {
		deletedCount = p.reconcileDeletions(ctx, state.connType, state.connName, state.enumSourceIDs)
	}

	report := &SyncReport{
		ConnectorName: state.connName,
		ConnectorType: state.connType,
		DocsProcessed: state.processed,
		DocsDeleted:   deletedCount,
		Errors:        state.errCount,
		Duration:      time.Since(state.start),
	}

	if streamErr != nil {
		return report, streamErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return report, ctxErr
	}

	p.log.Info("sync completed",
		zap.String("connector", state.connName),
		zap.Int("docs", report.DocsProcessed),
		zap.Int("deleted", report.DocsDeleted),
		zap.Int("errors", report.Errors),
		zap.Duration("duration", report.Duration),
	)
	return report, nil
}

// consumeStream drives the main event loop, reading items off the
// connector's stream and routing each to the appropriate handler.
// Returns the first terminal error from either channel (or nil on a
// clean close + nil error).
func (p *Pipeline) consumeStream(ctx context.Context, state *runState, items <-chan model.FetchItem, errs <-chan error) error {
	if state.progress != nil {
		state.progress(0, 0, 0, "")
	}

	ticker := time.NewTicker(checkpointInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Drain whatever is already in-flight on the error channel so
			// we return the most specific cause.
			select {
			case e := <-errs:
				if e != nil {
					return e
				}
			default:
			}
			return ctx.Err()

		case <-ticker.C:
			// Time-based checkpoint: connectors that emit docs slowly
			// (Paperless page loads, IMAP body fetches) would otherwise
			// wait for indexBatchSize docs before a flush. Keep the
			// resume cost bounded in wall-clock time too.
			p.flushPending(ctx, state)
			state.flushWG.Wait()
			p.persistLatestCursor(ctx, state)
			state.lastFlush = time.Now()

		case item, ok := <-items:
			if !ok {
				return p.drainErr(errs)
			}
			p.handleItem(ctx, state, item)

		case e := <-errs:
			if e != nil {
				return e
			}
			// errs closed cleanly before items — keep draining items.
		}
	}
}

// handleItem dispatches one FetchItem to the appropriate handler based on
// which field is set. The connector contract guarantees exactly one field
// is non-nil per item; unknown/empty items are simply ignored.
func (p *Pipeline) handleItem(ctx context.Context, state *runState, item model.FetchItem) {
	switch {
	case item.Doc != nil:
		state.pendingDocs = append(state.pendingDocs, *item.Doc)
		// Optimistic per-doc progress: tick the counter the
		// moment the doc is received from the connector, not
		// after it lands in OpenSearch. The async flush
		// (below) means the consumer loop never blocks on
		// embedding/indexing, so the counter keeps climbing
		// smoothly. Failure in the flush goroutine rolls the
		// optimistic bump back under reportMu.
		state.reportMu.Lock()
		state.processed++
		if state.hasEstimate && state.processed > state.total {
			state.total = state.processed
		}
		total, processed, errCount, scope := state.total, state.processed, state.errCount, state.scope
		state.reportMu.Unlock()
		if state.progress != nil {
			state.progress(total, processed, errCount, scope)
		}
		if len(state.pendingDocs) >= indexBatchSize {
			p.flushPending(ctx, state)
			state.lastFlush = time.Now()
		}
	case item.SourceID != nil:
		state.sawEnumeration = true
		state.enumSourceIDs = append(state.enumSourceIDs, *item.SourceID)
	case item.EnumerationComplete:
		state.sawEnumeration = true
	case item.Checkpoint != nil:
		state.latestCursor = item.Checkpoint
		// Flush then wait for every in-flight async flush to
		// land in OpenSearch before persisting the cursor —
		// otherwise a resume-from-cursor could skip docs that
		// never actually indexed.
		p.flushPending(ctx, state)
		state.flushWG.Wait()
		p.persistLatestCursor(ctx, state)
		state.lastFlush = time.Now()
	case item.EstimatedTotal != nil:
		state.reportMu.Lock()
		state.hasEstimate = true
		if *item.EstimatedTotal > int64(state.total) {
			state.total = int(*item.EstimatedTotal)
		}
		total, processed, errCount, scope := state.total, state.processed, state.errCount, state.scope
		state.reportMu.Unlock()
		if state.progress != nil {
			state.progress(total, processed, errCount, scope)
		}
	case item.Scope != nil:
		state.reportMu.Lock()
		state.scope = *item.Scope
		total, processed, errCount, scope := state.total, state.processed, state.errCount, state.scope
		state.reportMu.Unlock()
		if state.progress != nil {
			state.progress(total, processed, errCount, scope)
		}
	}
}

// drainErr reads a terminal error from errs (if present) without blocking
// if the channel is already closed.
func (p *Pipeline) drainErr(errs <-chan error) error {
	for e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// flushPending dispatches the buffered documents to a background
// goroutine that computes embeddings and bulk-indexes them, then
// returns immediately so the consumer loop can keep pumping items
// from the connector. flushSem caps concurrent flushes so a slow
// embedder can't balloon into unbounded goroutines; the blocking
// acquire provides natural back-pressure on the producer.
//
// Callers that need the flushes to be durable before proceeding
// (persistLatestCursor, end-of-run reconciliation) must first call
// state.flushWG.Wait().
func (p *Pipeline) flushPending(ctx context.Context, state *runState) {
	if len(state.pendingDocs) == 0 {
		return
	}
	batch := state.pendingDocs
	state.pendingDocs = nil

	state.flushWG.Add(1)
	go func() {
		defer state.flushWG.Done()
		// Acquire before doing work. The semaphore buffer is
		// the concurrency limit; if it's full we block here
		// until an earlier flush finishes.
		select {
		case state.flushSem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-state.flushSem }()

		chunks := make([]model.Chunk, 0, len(batch))
		for i := range batch {
			docChunks := buildDocumentChunks(&batch[i], state.ownerID, state.shared)
			p.populateChunkEmbeddings(ctx, &batch[i], docChunks)
			chunks = append(chunks, docChunks...)
		}

		if err := p.search.IndexChunks(ctx, chunks); err != nil {
			p.log.Error("failed to bulk-index batch",
				zap.String("connector", state.connName),
				zap.Int("batch", len(batch)),
				zap.Error(err),
			)
			// Roll back the optimistic per-doc bumps and
			// reclassify the batch as errors. reportMu
			// serializes with handleItem and with any
			// other concurrently-running flush goroutine.
			state.reportMu.Lock()
			state.processed -= len(batch)
			state.errCount += len(batch)
			total, processed, errCount, scope := state.total, state.processed, state.errCount, state.scope
			state.reportMu.Unlock()
			if state.progress != nil {
				state.progress(total, processed, errCount, scope)
			}
		}
	}()
}

// persistLatestCursor writes the most recent checkpoint cursor to the
// store if one is pending, clearing it afterward so subsequent
// time-ticker flushes don't re-persist an already-committed cursor.
func (p *Pipeline) persistLatestCursor(ctx context.Context, state *runState) {
	if state.latestCursor == nil {
		return
	}
	state.latestCursor.ConnectorID = state.connectorID
	if err := p.store.UpsertSyncCursor(ctx, state.latestCursor); err != nil {
		p.log.Warn("failed to persist cursor checkpoint",
			zap.String("connector", state.connName),
			zap.Error(err))
		return
	}
	state.latestCursor = nil
}

// buildDocumentChunks splits doc.Content into chunks and fans the document's
// metadata (title, source, visibility, ownership) down onto each one. The
// first chunk additionally carries FullContent for highlight fallbacks.
func buildDocumentChunks(doc *model.Document, ownerID string, shared bool) []model.Chunk {
	parentID := doc.SourceType + ":" + doc.SourceName + ":" + doc.SourceID
	docID := model.DocumentID(doc.SourceType, doc.SourceName, doc.SourceID).String()

	textChunks := chunking.Split(doc.Content, chunking.DefaultMaxTokens, chunking.DefaultOverlapTokens)
	if len(textChunks) == 0 {
		textChunks = []chunking.Chunk{{Index: 0, Text: doc.Content}}
	}

	chunks := make([]model.Chunk, len(textChunks))
	for j, tc := range textChunks {
		chunks[j] = model.Chunk{
			ID:             fmt.Sprintf("%s:%d", parentID, tc.Index),
			ParentID:       parentID,
			DocID:          docID,
			ChunkIndex:     tc.Index,
			Title:          doc.Title,
			Content:        tc.Text,
			SourceType:     doc.SourceType,
			SourceName:     doc.SourceName,
			SourceID:       doc.SourceID,
			MimeType:       doc.MimeType,
			Size:           doc.Size,
			Metadata:       doc.Metadata,
			Relations:      doc.Relations,
			ConversationID: doc.ConversationID,
			IMAPMessageID:  doc.IMAPMessageID,
			Hidden:         doc.Hidden,
			URL:            doc.URL,
			Visibility:     doc.Visibility,
			OwnerID:        ownerID,
			Shared:         shared,
			CreatedAt:      doc.CreatedAt,
		}
		if tc.Index == 0 {
			chunks[j].FullContent = doc.Content
		}
	}
	return chunks
}

// populateChunkEmbeddings fills in Embedding for chunks that pass the noise
// gate. Hidden documents (Telegram per-message canonical records) and
// very-short content skip embedding entirely — their vectors would cluster
// near every query, so they're BM25-only by design.
func (p *Pipeline) populateChunkEmbeddings(ctx context.Context, doc *model.Document, chunks []model.Chunk) {
	embedder := p.getEmbedder()
	if embedder == nil || doc.Hidden || len(doc.Content) < minEmbeddingContentLen {
		return
	}

	var embedTexts []string
	var embedIndices []int
	for j, c := range chunks {
		if countAlphabeticTokens(c.Content) >= minEmbeddingAlphabeticTokens {
			embedTexts = append(embedTexts, c.Content)
			embedIndices = append(embedIndices, j)
		}
	}
	if len(embedTexts) == 0 {
		return
	}

	embeddings, err := embedder.Embed(ctx, embedTexts, embedding.InputTypeDocument)
	if err != nil {
		p.log.Warn("embedding failed, indexing without vectors",
			zap.String("source_id", doc.SourceID),
			zap.Error(err),
		)
		return
	}
	if len(embeddings) != len(embedTexts) {
		return
	}
	for k, idx := range embedIndices {
		chunks[idx].Embedding = embeddings[k]
	}
}

// reconcileDeletions runs a streaming sorted merge-diff between the
// connector's enumerated source IDs (accumulated on state.enumSourceIDs
// in the order the connector emitted them, which is required to match
// OpenSearch `source_id.keyword` ascending sort) and the live
// OpenSearch index. Any ID present in OpenSearch but absent from the
// connector's stream is deleted (alongside its cached binary blob when
// a BinaryStore is wired). Returns the number of IDs deleted.
//
// Two-pointer walk invariants: both streams are ascending-sorted,
// deduplicated (OpenSearch side via `chunk_index:0`, connector side
// by contract), and cover the same connector scope. The colon-suffix
// children rule still applies — an OS id `INBOX:42:attachment:0` is
// preserved whenever its parent `INBOX:42` is in the connector's
// keep-set, even if the parent comes after it in sort order. We
// handle this by queueing provisional deletions and releasing them
// from the queue when a later connector id would cover them as a
// parent.
//
// Failure-mode semantics are deliberately permissive: any error in
// enumeration or deletion is logged and the sync continues. The
// opt-out lives one level up: connectors that never emit a SourceID
// or EnumerationComplete item have sawEnumeration=false and skip this
// function entirely.
func (p *Pipeline) reconcileDeletions(ctx context.Context, sourceType, sourceName string, connectorIDs []string) int {
	osItems, osErrs := p.search.StreamIndexedSourceIDs(ctx, sourceType, sourceName)
	stale := mergeDiffStaleIDs(ctx, connectorIDs, osItems, osErrs)
	if stale == nil {
		// merge-diff errored partway. Don't flush the pending deletes —
		// we can't tell them from legitimate keeps. Next sync retries.
		p.log.Warn("deletion sync: merge-diff failed; skipping",
			zap.String("connector", sourceName))
		return 0
	}
	if len(stale) == 0 {
		return 0
	}

	if err := p.search.DeleteBySourceIDs(ctx, sourceType, sourceName, stale); err != nil {
		p.log.Warn("deletion sync: delete failed; skipping cache cleanup",
			zap.String("connector", sourceName),
			zap.Int("stale_count", len(stale)),
			zap.Error(err))
		return 0
	}

	p.cascadeBinaryDelete(ctx, sourceType, sourceName, stale)

	p.log.Info("deletion sync: removed stale documents",
		zap.String("connector", sourceName),
		zap.Int("count", len(stale)))
	return len(stale)
}

// mergeDiffStaleIDs walks the connector's (sorted) keep list against
// a (sorted) stream of indexed IDs from OpenSearch. Returns the list
// of indexed IDs that are neither in keep nor a colon-suffix child of
// any keep entry — these are the stale docs to delete.
//
// Returns nil (not an empty slice) when the OpenSearch stream errored
// or the context cancelled: callers treat nil as "don't flush" while
// [] means "nothing to delete, confidently."
func mergeDiffStaleIDs(ctx context.Context, connectorIDs []string, osItems <-chan string, osErrs <-chan error) []string {
	// keepSet lets isChildOfKept do O(depth) prefix checks without
	// re-scanning the sorted connector list — needed when an OS id
	// is a colon-suffix child of a connector id that sorts earlier.
	keepSet := make(map[string]struct{}, len(connectorIDs))
	for _, sid := range connectorIDs {
		keepSet[sid] = struct{}{}
	}
	stale := make([]string, 0)
	ci := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case osID, ok := <-osItems:
			if !ok {
				// OS stream closed cleanly; drain errs below.
				return drainOSErrsOrReturn(osErrs, stale)
			}
			// Advance the connector cursor past any entries that
			// sort before osID. Everything skipped here is either
			// a doc that was never indexed (fine — it'll be
			// indexed on this or a future run) or already covered
			// by a matching OS id we handled earlier.
			for ci < len(connectorIDs) && connectorIDs[ci] < osID {
				ci++
			}
			if ci < len(connectorIDs) && connectorIDs[ci] == osID {
				ci++
				continue
			}
			if isChildOfKept(osID, keepSet) {
				continue
			}
			stale = append(stale, osID)
		case err := <-osErrs:
			if err != nil {
				return nil
			}
			// nil on errs means the stream closed without error;
			// keep reading osItems until it closes too.
		}
	}
}

// drainOSErrsOrReturn pulls any pending error off the OpenSearch
// stream's error channel now that its items channel has closed.
// Returns nil when an error surfaces (signalling "don't flush"), or
// the stale slice on clean close.
func drainOSErrsOrReturn(osErrs <-chan error, stale []string) []string {
	for err := range osErrs {
		if err != nil {
			return nil
		}
	}
	return stale
}

// cascadeBinaryDelete best-effort removes cached blobs for stale source_ids.
// A leftover blob is harmless (eviction picks it up); a leftover chunk has
// already been removed in reconcileDeletions above.
func (p *Pipeline) cascadeBinaryDelete(ctx context.Context, sourceType, sourceName string, stale []string) {
	if p.binaryStore == nil {
		return
	}
	for _, sid := range stale {
		if err := p.binaryStore.Delete(ctx, sourceType, sourceName, sid); err != nil {
			p.log.Debug("deletion sync: binary cache delete failed",
				zap.String("connector", sourceName),
				zap.String("source_id", sid),
				zap.Error(err))
		}
	}
}

// isChildOfKept reports whether sid is a colon-suffix child of any
// source_id in keep — i.e. sid starts with `{k}:` for some k in keep.
// Implements the convention documented on FetchItem.SourceID:
// connectors that emit parent-only identifiers (IMAP enumerates email
// UIDs but not attachment indices) get their children preserved
// automatically via the shared naming scheme.
//
// Scanning right-to-left lets us bail on the deepest prefix first, which
// for attachment-like structures (`folder:uid:attachment:N`) usually
// hits on the second iteration.
func isChildOfKept(sid string, keep map[string]struct{}) bool {
	for i := len(sid) - 1; i > 0; i-- {
		if sid[i] == ':' {
			if _, ok := keep[sid[:i]]; ok {
				return true
			}
		}
	}
	return false
}

func (p *Pipeline) getEmbedder() embedding.Embedder {
	if p.embeddings == nil {
		return nil
	}
	return p.embeddings.Get()
}

// urlStripRe matches URLs so they can be removed before counting alphabetic
// tokens. URLs are noise for the noise gate — a chunk that's "just a URL"
// has zero real semantic content.
var urlStripRe = regexp.MustCompile(`https?://\S+`)

// countAlphabeticTokens returns the number of whitespace-separated tokens
// in text (after URL stripping) that contain at least 2 alphabetic
// characters. This filters out pure URLs, hashes, base64 blobs, numeric IDs,
// and isolated punctuation — the kinds of "content" that produce noisy
// embeddings without contributing real semantic signal.
func countAlphabeticTokens(text string) int {
	text = urlStripRe.ReplaceAllString(text, " ")
	var count int
	for _, token := range strings.Fields(text) {
		alpha := 0
		for _, r := range token {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= 'À' && r <= 'ÿ') || // basic Latin-1 supplement
				r >= 0x0100 { // any non-ASCII letter — Cyrillic, CJK, etc.
				alpha++
				if alpha >= 2 {
					count++
					break
				}
			}
		}
	}
	return count
}

// ErrStreamClosed is returned when the pipeline attempts to read from a
// closed connector stream. Currently unused — reserved for future error
// paths that need to distinguish "stream ended cleanly" from "connector
// emitted a terminal error".
var ErrStreamClosed = errors.New("pipeline: connector stream closed")
