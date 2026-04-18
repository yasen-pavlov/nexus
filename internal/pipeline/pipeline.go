// Package pipeline orchestrates the ingestion of documents from connectors into the store.
package pipeline

import (
	"context"
	"fmt"
	"regexp"
	"strings"
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

// ProgressFunc is called with (total, processed, errors) as documents are indexed.
type ProgressFunc func(total, processed, errors int)

// RunWithProgress fetches documents from a connector, chunks them, generates embeddings, and indexes them.
// connectorID identifies the connector in the sync_cursors table; ownerID and shared are
// written to each indexed chunk for search scoping. progress may be nil.
func (p *Pipeline) RunWithProgress(ctx context.Context, connectorID uuid.UUID, conn connector.Connector, ownerID string, shared bool, progress ProgressFunc) (*SyncReport, error) {
	start := time.Now()
	connName := conn.Name()

	p.log.Info("sync started", zap.String("connector", connName), zap.String("type", conn.Type()))

	if p.store == nil {
		return nil, fmt.Errorf("pipeline: store not configured")
	}

	cursor, err := p.store.GetSyncCursor(ctx, connectorID)
	if err != nil {
		return nil, fmt.Errorf("pipeline: get cursor: %w", err)
	}

	result, err := conn.Fetch(ctx, cursor)
	if err != nil {
		return nil, fmt.Errorf("pipeline: fetch: %w", err)
	}

	total := len(result.Documents)
	if progress != nil {
		progress(total, 0, 0)
	}

	var errCount int
	var processed int
	for i := range result.Documents {
		// Respect cancellation between documents. This loop is the
		// long-running phase of a sync; without this check the run
		// would only abort on the next network round-trip inside the
		// connector / search client. Returning a partial report lets
		// the caller persist what completed before the cancel.
		select {
		case <-ctx.Done():
			return &SyncReport{
				ConnectorName: connName,
				ConnectorType: conn.Type(),
				DocsProcessed: processed,
				Errors:        errCount,
				Duration:      time.Since(start),
			}, ctx.Err()
		default:
		}

		doc := &result.Documents[i]
		parentID := doc.SourceType + ":" + doc.SourceName + ":" + doc.SourceID
		docID := model.DocumentID(doc.SourceType, doc.SourceName, doc.SourceID).String()

		// Chunk the document
		textChunks := chunking.Split(doc.Content, chunking.DefaultMaxTokens, chunking.DefaultOverlapTokens)
		if len(textChunks) == 0 {
			textChunks = []chunking.Chunk{{Index: 0, Text: doc.Content}}
		}

		// Build model chunks
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

		// Generate embeddings for chunks that pass the noise gate. Chunks with
		// low alphabetic-token count get indexed for BM25 only — they don't
		// contribute a vector to kNN search. See minEmbeddingAlphabeticTokens
		// for the rationale (noise hubs in embedding space).
		//
		// Hidden documents (Telegram per-message canonical records) skip
		// embedding entirely. They exist for relation targeting and chat-
		// browser pagination — their content is already covered by the
		// parent conversation window, and embedding them would re-introduce
		// the short-message noise-hub problem windowing was built to solve.
		embedder := p.getEmbedder()
		if embedder != nil && !doc.Hidden && len(doc.Content) >= minEmbeddingContentLen {
			var embedTexts []string
			var embedIndices []int // index into chunks for each text we send
			for j, c := range chunks {
				if countAlphabeticTokens(c.Content) >= minEmbeddingAlphabeticTokens {
					embedTexts = append(embedTexts, c.Content)
					embedIndices = append(embedIndices, j)
				}
			}

			if len(embedTexts) > 0 {
				embeddings, err := embedder.Embed(ctx, embedTexts, embedding.InputTypeDocument)
				if err != nil {
					p.log.Warn("embedding failed, indexing without vectors",
						zap.String("source_id", doc.SourceID),
						zap.Error(err),
					)
				} else if len(embeddings) == len(embedTexts) {
					for k, idx := range embedIndices {
						chunks[idx].Embedding = embeddings[k]
					}
				}
			}
		}

		// Index chunks
		if err := p.search.IndexChunks(ctx, chunks); err != nil {
			p.log.Error("failed to index document",
				zap.String("source_id", doc.SourceID),
				zap.Error(err),
			)
			errCount++
		}

		processed = i + 1
		if progress != nil {
			progress(total, processed, errCount)
		}
	}

	deletedCount := p.reconcileDeletions(ctx, conn.Type(), connName, result.CurrentSourceIDs)

	if result.Cursor != nil {
		// Override whatever the connector set as ConnectorID — it doesn't know
		// the configured UUID. The store keys cursors by connector UUID.
		result.Cursor.ConnectorID = connectorID
		if err := p.store.UpsertSyncCursor(ctx, result.Cursor); err != nil {
			return nil, fmt.Errorf("pipeline: update cursor: %w", err)
		}
	}

	report := &SyncReport{
		ConnectorName: connName,
		ConnectorType: conn.Type(),
		DocsProcessed: processed,
		DocsDeleted:   deletedCount,
		Errors:        errCount,
		Duration:      time.Since(start),
	}

	p.log.Info("sync completed",
		zap.String("connector", connName),
		zap.Int("docs", report.DocsProcessed),
		zap.Int("deleted", report.DocsDeleted),
		zap.Int("errors", report.Errors),
		zap.Duration("duration", report.Duration),
	)

	return report, nil
}

// reconcileDeletions diffs the indexed source_ids against the
// connector's authoritative CurrentSourceIDs list and removes the
// stragglers (plus their cached binaries when a BinaryStore is
// wired). Returns the count of source_ids deleted.
//
// Failure-mode semantics are deliberately permissive: any error in
// enumeration or deletion is logged and the sync continues. Deletion
// is bookkeeping — losing one round of cleanup is recoverable on the
// next sync; failing the entire sync because of it would mean a
// transient OpenSearch hiccup blocks indexing too. The all-or-nothing
// rule lives one level up: connectors set CurrentSourceIDs to nil
// when their enumeration is incomplete, which short-circuits this
// function entirely.
func (p *Pipeline) reconcileDeletions(ctx context.Context, sourceType, sourceName string, currentSourceIDs []string) int {
	if currentSourceIDs == nil {
		return 0 // connector opted out (or its enumeration errored)
	}

	indexed, err := p.search.ListIndexedSourceIDs(ctx, sourceType, sourceName)
	if err != nil {
		p.log.Warn("deletion sync: list indexed source ids failed; skipping",
			zap.String("connector", sourceName), zap.Error(err))
		return 0
	}
	if len(indexed) == 0 {
		return 0
	}

	keep := make(map[string]struct{}, len(currentSourceIDs))
	for _, sid := range currentSourceIDs {
		keep[sid] = struct{}{}
	}
	stale := make([]string, 0)
	for _, sid := range indexed {
		if _, ok := keep[sid]; ok {
			continue
		}
		if isChildOfKept(sid, keep) {
			// Preserved by the "colon-suffix children" convention —
			// e.g. IMAP attachment `INBOX:42:attachment:0` when the
			// parent email `INBOX:42` is in keep. Lets connectors
			// declare parent-only source_ids and inherit child
			// preservation without enumerating every child.
			continue
		}
		stale = append(stale, sid)
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

	// Cascade to BinaryStore — best-effort. A leftover blob is harmless
	// (eviction picks it up); a leftover chunk shows up in search and is
	// already gone above.
	if p.binaryStore != nil {
		for _, sid := range stale {
			if err := p.binaryStore.Delete(ctx, sourceType, sourceName, sid); err != nil {
				p.log.Debug("deletion sync: binary cache delete failed",
					zap.String("connector", sourceName),
					zap.String("source_id", sid),
					zap.Error(err))
			}
		}
	}

	p.log.Info("deletion sync: removed stale documents",
		zap.String("connector", sourceName),
		zap.Int("count", len(stale)))
	return len(stale)
}

// isChildOfKept reports whether sid is a colon-suffix child of any
// source_id in keep — i.e. sid starts with `{k}:` for some k in keep.
// Implements the convention documented on FetchResult.CurrentSourceIDs:
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
