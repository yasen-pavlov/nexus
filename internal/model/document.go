// Package model defines shared types used across the application.
package model

import (
	"time"

	"github.com/google/uuid"
)

// nexusNamespace is a fixed UUID v5 namespace for generating deterministic document IDs.
var nexusNamespace = uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890")

// DocumentID returns a deterministic UUID v5 derived from the source triple.
// The same source document always produces the same ID across re-syncs.
func DocumentID(sourceType, sourceName, sourceID string) uuid.UUID {
	return uuid.NewSHA1(nexusNamespace, []byte(sourceType+":"+sourceName+":"+sourceID))
}

type Document struct {
	ID         uuid.UUID      `json:"id"`
	SourceType string         `json:"source_type"`
	SourceName string         `json:"source_name"`
	SourceID   string         `json:"source_id"`
	Title      string         `json:"title"`
	Content    string         `json:"content"`
	MimeType   string         `json:"mime_type,omitempty"`
	Size       int64          `json:"size,omitempty"`
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
	DocID       string         `json:"doc_id"`
	ChunkIndex  int            `json:"chunk_index"`
	Title       string         `json:"title"`
	Content     string         `json:"content"`
	FullContent string         `json:"full_content,omitempty"`
	Embedding   []float32      `json:"embedding,omitempty"`
	SourceType  string         `json:"source_type"`
	SourceName  string         `json:"source_name"`
	SourceID    string         `json:"source_id"`
	MimeType    string         `json:"mime_type,omitempty"`
	Size        int64          `json:"size,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	URL         string         `json:"url,omitempty"`
	Visibility  string         `json:"visibility"`
	OwnerID     string         `json:"owner_id,omitempty"`
	Shared      bool           `json:"shared,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	IndexedAt   time.Time      `json:"indexed_at"`
}
