// Package pipeline orchestrates the ingestion of documents from connectors into the store.
package pipeline

import (
	"context"
	"fmt"
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

// minEmbeddingContentLen is the minimum content length (chars) for generating embeddings.
// Shorter content produces unstable vectors that pollute semantic search results.
const minEmbeddingContentLen = 50

// EmbedderProvider returns the current embedder (supports hot-reload).
type EmbedderProvider interface {
	Get() embedding.Embedder
}

// SyncReport contains the results of a sync operation.
type SyncReport struct {
	ConnectorName string        `json:"connector_name"`
	ConnectorType string        `json:"connector_type"`
	DocsProcessed int           `json:"docs_processed"`
	Errors        int           `json:"errors"`
	Duration      time.Duration `json:"duration"`
}

// Pipeline orchestrates fetching documents and indexing them.
type Pipeline struct {
	store      *store.Store
	search     *search.Client
	embeddings EmbedderProvider
	log        *zap.Logger
}

// New creates a new Pipeline. embeddings can be nil for BM25-only mode.
func New(store *store.Store, search *search.Client, embeddings EmbedderProvider, log *zap.Logger) *Pipeline {
	return &Pipeline{store: store, search: search, embeddings: embeddings, log: log}
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
	for i := range result.Documents {
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
				ID:         fmt.Sprintf("%s:%d", parentID, tc.Index),
				ParentID:   parentID,
				DocID:      docID,
				ChunkIndex: tc.Index,
				Title:      doc.Title,
				Content:    tc.Text,
				SourceType: doc.SourceType,
				SourceName: doc.SourceName,
				SourceID:   doc.SourceID,
				MimeType:   doc.MimeType,
				Size:       doc.Size,
				Metadata:   doc.Metadata,
				URL:        doc.URL,
				Visibility: doc.Visibility,
				OwnerID:    ownerID,
				Shared:     shared,
				CreatedAt:  doc.CreatedAt,
			}
			if tc.Index == 0 {
				chunks[j].FullContent = doc.Content
			}
		}

		// Generate embeddings if available and content is long enough
		// Short content (< 50 chars) produces unstable embeddings that match random queries
		embedder := p.getEmbedder()
		if embedder != nil && len(doc.Content) >= minEmbeddingContentLen {
			texts := make([]string, len(chunks))
			for j, c := range chunks {
				texts[j] = c.Content
			}

			embeddings, err := embedder.Embed(ctx, texts)
			if err != nil {
				p.log.Warn("embedding failed, indexing without vectors",
					zap.String("source_id", doc.SourceID),
					zap.Error(err),
				)
			} else if len(embeddings) == len(chunks) {
				for j := range chunks {
					chunks[j].Embedding = embeddings[j]
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

		if progress != nil {
			progress(total, i+1, errCount)
		}
	}

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
		DocsProcessed: len(result.Documents),
		Errors:        errCount,
		Duration:      time.Since(start),
	}

	p.log.Info("sync completed",
		zap.String("connector", connName),
		zap.Int("docs", report.DocsProcessed),
		zap.Int("errors", report.Errors),
		zap.Duration("duration", report.Duration),
	)

	return report, nil
}

func (p *Pipeline) getEmbedder() embedding.Embedder {
	if p.embeddings == nil {
		return nil
	}
	return p.embeddings.Get()
}
