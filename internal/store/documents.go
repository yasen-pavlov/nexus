package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/muty/nexus/internal/model"
)

var ErrNotFound = errors.New("not found")

func (s *Store) UpsertDocument(ctx context.Context, doc *model.Document) error {
	if doc.ID == uuid.Nil {
		doc.ID = uuid.New()
	}
	doc.IndexedAt = time.Now()

	metadata, err := json.Marshal(doc.Metadata)
	if err != nil {
		return fmt.Errorf("store: marshal metadata: %w", err)
	}

	query := `
		INSERT INTO documents (id, source_type, source_name, source_id, title, content, metadata, url, visibility, created_at, indexed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (source_type, source_name, source_id)
		DO UPDATE SET
			title = EXCLUDED.title,
			content = EXCLUDED.content,
			metadata = EXCLUDED.metadata,
			url = EXCLUDED.url,
			visibility = EXCLUDED.visibility,
			indexed_at = EXCLUDED.indexed_at
	`

	_, err = s.pool.Exec(ctx, query,
		doc.ID, doc.SourceType, doc.SourceName, doc.SourceID,
		doc.Title, doc.Content, metadata, doc.URL,
		doc.Visibility, doc.CreatedAt, doc.IndexedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upsert document: %w", err)
	}
	return nil
}

func (s *Store) GetDocument(ctx context.Context, id uuid.UUID) (*model.Document, error) {
	query := `
		SELECT id, source_type, source_name, source_id, title, content, metadata, url, visibility, created_at, indexed_at
		FROM documents WHERE id = $1
	`
	row := s.pool.QueryRow(ctx, query, id)

	var doc model.Document
	var metadata []byte
	err := row.Scan(
		&doc.ID, &doc.SourceType, &doc.SourceName, &doc.SourceID,
		&doc.Title, &doc.Content, &metadata, &doc.URL,
		&doc.Visibility, &doc.CreatedAt, &doc.IndexedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get document: %w", err)
	}

	if err := json.Unmarshal(metadata, &doc.Metadata); err != nil {
		return nil, fmt.Errorf("store: unmarshal metadata: %w", err)
	}
	return &doc, nil
}
