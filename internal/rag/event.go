// Package rag implements the RAG (Ask) orchestrator: it turns a single
// user turn into a streamed, grounded, cited answer over the user's
// existing index. Phase 2 ships the single-shot retrieve → generate →
// done loop without rewriter, tool use, or multi-modal — those land in
// Phases 4-6.
package rag

import (
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

// EventKind discriminates Event payloads. Use the matching field; others
// are zero-valued.
type EventKind int

const (
	// EvRetrieving fires once at the start of retrieval. Carries Query.
	EvRetrieving EventKind = iota
	// EvEvidence fires once after retrieval with the chunk previews the
	// model received in its prompt.
	EvEvidence
	// EvText carries an incremental text delta (post citation-marker
	// stripping for non-Anthropic providers).
	EvText
	// EvCitation pins a span of generated text back to a retrieved doc.
	EvCitation
	// EvUsage carries token accounting at the end of generation.
	EvUsage
	// EvDone fires last. Carries StopReason and the persisted assistant
	// MessageID. The channel closes immediately after.
	EvDone
	// EvError fires on failure; followed immediately by EvDone with
	// stop_reason=error and the channel closes.
	EvError
)

// ChunkPreview is re-exported from internal/model for backwards-compat
// inside the rag package. The canonical definition lives on the model
// package because chat_messages.evidence persists this shape.
type ChunkPreview = model.ChunkPreview

// Event is the streamed payload type. Exactly one payload field is
// populated per Kind.
type Event struct {
	Kind       EventKind
	Query      string              // EvRetrieving
	Evidence   []ChunkPreview      // EvEvidence
	TextDelta  string              // EvText
	Citation   *model.ChatCitation // EvCitation
	Usage      *model.ChatUsage    // EvUsage
	StopReason string              // EvDone
	MessageID  uuid.UUID           // EvDone
	Err        string              // EvError
}
