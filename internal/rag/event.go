// Package rag implements the RAG (Ask) orchestrator: it turns a single
// user turn into a streamed, grounded, cited answer over the user's
// existing index. Phase 4 ships the multi-turn rewriter, the
// skip-retrieval branch (greetings / history-only follow-ups), and
// auto-titles. Phase 5 will add the agentic tool-use loop; Phase 6 the
// multi-modal images.
//
// SSE ordering invariant on POST /api/chats/:id/messages:
//
//		(retrieving | skipped_retrieval) → evidence? → text* → citation* →
//		usage → title? → done
//
//	  - Exactly one of `retrieving` or `skipped_retrieval` fires per turn,
//	    never both.
//	  - `evidence` only fires after `retrieving`. The skip-retrieval branch
//	    never produces evidence.
//	  - `title` fires zero or one times, only on the first end_turn assistant
//	    message of a chat (when the rewriter model is configured + auto-title
//	    succeeds within its 2s timeout).
//	  - `done` is always last; the channel closes immediately after.
//	  - `error` may interleave with text/citation; followed by `done`
//	    carrying stop_reason=error.
package rag

import (
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

// EventKind discriminates Event payloads. Use the matching field; others
// are zero-valued.
type EventKind int

const (
	// EvRetrieving fires once at the start of retrieval. Carries Query
	// — the query that actually went to OpenSearch (= the rewriter's
	// output when it ran, otherwise the user's content).
	EvRetrieving EventKind = iota
	// EvEvidence fires once after retrieval with the chunk previews the
	// model received in its prompt.
	EvEvidence
	// EvText carries an incremental text delta (post citation-marker
	// stripping for non-Anthropic providers).
	EvText
	// EvCitation pins a span of generated text back to a retrieved doc.
	EvCitation
	// EvUsage carries token accounting at the end of generation
	// (aggregates rewriter + title call usage when those ran).
	EvUsage
	// EvDone fires last. Carries StopReason and the persisted assistant
	// MessageID. The channel closes immediately after.
	EvDone
	// EvError fires on failure; followed immediately by EvDone with
	// stop_reason=error and the channel closes.
	EvError
	// EvSkippedRetrieval replaces EvRetrieving when the rewriter judged
	// the question answerable from chat history alone (greetings, meta
	// questions, history-only follow-ups). The Query field carries the
	// rewritten phrasing for the FE phase strip. No EvEvidence follows.
	EvSkippedRetrieval
	// EvTitle fires zero or one times, only after a successful first
	// assistant turn when the rewriter model is configured and the
	// auto-title call returns a non-empty result. Always emitted before
	// EvDone so the FE state machine can update the recent-chats grid
	// without an extra refetch.
	EvTitle
	// EvRewriterStatus fires when the rewriter ran but its result
	// couldn't be used (timeout / empty / parse-failed / error). The FE
	// renders a quiet diagnostic glyph on the phase chip so users know
	// "Searching your corpus" means "rewriter fell back" rather than
	// "rewriter chose not to rewrite". Carries StatusReason.
	EvRewriterStatus
	// EvTitleStatus fires when the auto-title path attempted but failed
	// (timeout / empty / error). Supportive transparency for the admin
	// who configured the rewriter model and is wondering why titles
	// aren't appearing. Carries StatusReason.
	EvTitleStatus
)

// ChunkPreview is re-exported from internal/model for backwards-compat
// inside the rag package. The canonical definition lives on the model
// package because chat_messages.evidence persists this shape.
type ChunkPreview = model.ChunkPreview

// Event is the streamed payload type. Exactly one payload field is
// populated per Kind.
type Event struct {
	Kind         EventKind
	Query        string              // EvRetrieving / EvSkippedRetrieval
	Evidence     []ChunkPreview      // EvEvidence
	TextDelta    string              // EvText
	Citation     *model.ChatCitation // EvCitation
	Usage        *model.ChatUsage    // EvUsage
	StopReason   string              // EvDone
	MessageID    uuid.UUID           // EvDone
	DurationMs   int                 // EvDone — server-measured runTurn wall-clock
	Err          string              // EvError
	Title        string              // EvTitle
	StatusReason string              // EvRewriterStatus / EvTitleStatus — "timeout"|"empty"|"parse_failed"|"error"
}
