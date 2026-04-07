// Package pipeline orchestrates the ingestion of documents from connectors into the store.
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

type SyncReport struct {
	ConnectorName string        `json:"connector_name"`
	ConnectorType string        `json:"connector_type"`
	DocsProcessed int           `json:"docs_processed"`
	Errors        int           `json:"errors"`
	Duration      time.Duration `json:"duration"`
}

type Pipeline struct {
	store *store.Store
	log   *zap.Logger
}

func New(store *store.Store, log *zap.Logger) *Pipeline {
	return &Pipeline{store: store, log: log}
}

func (p *Pipeline) Run(ctx context.Context, conn connector.Connector) (*SyncReport, error) {
	start := time.Now()
	connID := conn.Name()

	p.log.Info("sync started", zap.String("connector", connID), zap.String("type", conn.Type()))

	// Load existing cursor
	cursor, err := p.store.GetSyncCursor(ctx, connID)
	if err != nil {
		return nil, fmt.Errorf("pipeline: get cursor: %w", err)
	}

	// Fetch documents
	result, err := conn.Fetch(ctx, cursor)
	if err != nil {
		return nil, fmt.Errorf("pipeline: fetch: %w", err)
	}

	// Store each document
	var errCount int
	for i := range result.Documents {
		if err := p.store.UpsertDocument(ctx, &result.Documents[i]); err != nil {
			p.log.Error("failed to upsert document",
				zap.String("source_id", result.Documents[i].SourceID),
				zap.Error(err),
			)
			errCount++
		}
	}

	// Update sync cursor
	if result.Cursor != nil {
		if err := p.store.UpsertSyncCursor(ctx, result.Cursor); err != nil {
			return nil, fmt.Errorf("pipeline: update cursor: %w", err)
		}
	}

	report := &SyncReport{
		ConnectorName: connID,
		ConnectorType: conn.Type(),
		DocsProcessed: len(result.Documents),
		Errors:        errCount,
		Duration:      time.Since(start),
	}

	p.log.Info("sync completed",
		zap.String("connector", connID),
		zap.Int("docs", report.DocsProcessed),
		zap.Int("errors", report.Errors),
		zap.Duration("duration", report.Duration),
	)

	return report, nil
}
