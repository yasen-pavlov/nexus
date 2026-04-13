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

// Relation describes a typed edge from a chunk (or its parent Document) to
// another document. Relations replace the ad-hoc `parent_message_id`-style
// metadata keys that connectors used to stuff into Metadata. See
// `plans/scalable-beaming-tower.md` for the design.
//
// One of TargetID or TargetSourceID must be set:
//
//   - TargetID: the UUID of the target Document, when it's resolvable at
//     emit time (same-connector same-batch edges like IMAP attachment →
//     email or Telegram message → window).
//   - TargetSourceID: a stable source-side pointer (IMAP Message-ID, Telegram
//     chat:msg:msg source id). The API resolves these lazily at read time.
type Relation struct {
	Type           string `json:"type"`
	TargetSourceID string `json:"target_source_id,omitempty"`
	TargetID       string `json:"target_id,omitempty"`
}

// Relation type constants — keep the wire values stable so reindex-free
// additions are possible, and new connectors can reuse them.
const (
	RelationAttachmentOf   = "attachment_of"
	RelationReplyTo        = "reply_to"
	RelationMemberOfThread = "member_of_thread"
	RelationMemberOfWindow = "member_of_window"
)

type Document struct {
	ID             uuid.UUID      `json:"id"`
	SourceType     string         `json:"source_type"`
	SourceName     string         `json:"source_name"`
	SourceID       string         `json:"source_id"`
	Title          string         `json:"title"`
	Content        string         `json:"content"`
	MimeType       string         `json:"mime_type,omitempty"`
	Size           int64          `json:"size,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Relations      []Relation     `json:"relations,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	IMAPMessageID  string         `json:"imap_message_id,omitempty"`
	Hidden         bool           `json:"hidden,omitempty"`
	URL            string         `json:"url,omitempty"`
	Visibility     string         `json:"visibility"`
	CreatedAt      time.Time      `json:"created_at"`
	IndexedAt      time.Time      `json:"indexed_at"`
}

// Chunk represents a segment of a document indexed in OpenSearch with an embedding.
//
// Hidden=true flags a chunk that must be excluded from default search results —
// used by Telegram per-message docs, which exist for relation targeting and
// chat-browser pagination but would produce duplicate hits alongside their
// parent conversation window if ranked. Polarity is inverted from the more
// obvious "Searchable" because the zero value must mean "visible" to survive
// legacy docs that predate the field.
type Chunk struct {
	ID             string         `json:"id"`
	ParentID       string         `json:"parent_id"`
	DocID          string         `json:"doc_id"`
	ChunkIndex     int            `json:"chunk_index"`
	Title          string         `json:"title"`
	Content        string         `json:"content"`
	FullContent    string         `json:"full_content,omitempty"`
	Embedding      []float32      `json:"embedding,omitempty"`
	SourceType     string         `json:"source_type"`
	SourceName     string         `json:"source_name"`
	SourceID       string         `json:"source_id"`
	MimeType       string         `json:"mime_type,omitempty"`
	Size           int64          `json:"size,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Relations      []Relation     `json:"relations,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	IMAPMessageID  string         `json:"imap_message_id,omitempty"`
	Hidden         bool           `json:"hidden,omitempty"`
	URL            string         `json:"url,omitempty"`
	Visibility     string         `json:"visibility"`
	OwnerID        string         `json:"owner_id,omitempty"`
	Shared         bool           `json:"shared,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	IndexedAt      time.Time      `json:"indexed_at"`
}
