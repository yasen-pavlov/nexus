// Package model defines shared types used across the application.
package model

import (
	"time"

	"github.com/google/uuid"
)

type Document struct {
	ID         uuid.UUID      `json:"id"`
	SourceType string         `json:"source_type"`
	SourceName string         `json:"source_name"`
	SourceID   string         `json:"source_id"`
	Title      string         `json:"title"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	URL        string         `json:"url,omitempty"`
	Visibility string         `json:"visibility"`
	CreatedAt  time.Time      `json:"created_at"`
	IndexedAt  time.Time      `json:"indexed_at"`
}

// Chunk represents a segment of a document indexed in OpenSearch with an embedding.
type Chunk struct {
	ID          string         `json:"id"`
	ParentID    string         `json:"parent_id"`
	ChunkIndex  int            `json:"chunk_index"`
	Title       string         `json:"title"`
	Content     string         `json:"content"`
	FullContent string         `json:"full_content,omitempty"`
	Embedding   []float32      `json:"embedding,omitempty"`
	SourceType  string         `json:"source_type"`
	SourceName  string         `json:"source_name"`
	SourceID    string         `json:"source_id"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	URL         string         `json:"url,omitempty"`
	Visibility  string         `json:"visibility"`
	CreatedAt   time.Time      `json:"created_at"`
	IndexedAt   time.Time      `json:"indexed_at"`
}
