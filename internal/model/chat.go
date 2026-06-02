package model

import (
	"time"

	"github.com/google/uuid"
)

// ChatRole is the persona of a chat message turn. "tool" is forward-compat
// for the Phase 5 tool-use loop; Phase 2 only writes user + assistant.
type ChatRole string

const (
	ChatRoleUser      ChatRole = "user"
	ChatRoleAssistant ChatRole = "assistant"
	ChatRoleTool      ChatRole = "tool"
)

// Chat is a single conversation thread owned by a user. Title defaults
// to ” until the rewriter (Phase 4) auto-summarises the first turn.
type Chat struct {
	ID           uuid.UUID `json:"id"`
	UserID       uuid.UUID `json:"user_id"`
	Title        string    `json:"title"`
	DefaultModel string    `json:"default_model"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ChatListEntry is the row shape returned by GET /api/chats. Carries the
// first user message as a preview so the FE recent-chats list can show
// "what is this chat about" without N+1ing /api/chats/{id} per row.
// Empty until the chat has its first user turn (e.g. just after creation
// but before the first message is appended).
type ChatListEntry struct {
	Chat
	FirstMessagePreview string `json:"first_message_preview,omitempty"`
}

// ChatMessage is one persisted turn inside a chat. Seq is monotonic per
// chat (assigned inside store.AppendMessage under a row lock).
type ChatMessage struct {
	ID        uuid.UUID      `json:"id"`
	ChatID    uuid.UUID      `json:"chat_id"`
	Role      ChatRole       `json:"role"`
	Seq       int            `json:"seq"`
	Content   string         `json:"content"`
	Model     string         `json:"model,omitempty"`
	Citations []ChatCitation `json:"citations,omitempty"`
	// Evidence is the chunks the orchestrator retrieved for THIS turn
	// (the same payload that streamed in EvEvidence). Persisting them
	// alongside citations preserves the citation→source link end-to-end:
	// ChunkPreview.DocID is the OpenSearch chunk handle, so historical
	// chats stay grounded — citations can render as numbered pills, the
	// FE evidence rail repopulates, and any future feature can hop
	// /api/documents/{id}, /related, /conversations, /blob from the
	// stored handles.
	Evidence  []ChunkPreview `json:"evidence,omitempty"`
	ToolCalls []ChatToolCall `json:"tool_calls,omitempty"`
	Usage     *ChatUsage     `json:"usage,omitempty"`
	// RewrittenQuery is the rewriter's normalised search query when the
	// rewriter ran on this turn. Empty when the rewriter was disabled
	// or this was the first user turn (rewriter only runs when prior
	// assistant turns exist). Persisted so the FE phase strip can
	// repopulate "Searching for: <rewritten>" on chat reload, and so
	// the Phase 7 eval harness knows which query actually hit OpenSearch.
	RewrittenQuery string `json:"rewritten_query,omitempty"`
	// SkippedRetrieval is true when the rewriter judged the question
	// answerable from chat history alone (greetings, meta questions,
	// history-only follow-ups). On these turns no OpenSearch call ran
	// and Evidence is empty.
	SkippedRetrieval bool `json:"skipped_retrieval,omitempty"`
	// DurationMs is the wall-clock time the orchestrator's runTurn
	// took, in milliseconds. Persisted at orchestrator-side so the FE
	// can render a consistent label across the live (in-flight) view
	// and the post-refresh persisted view. Nil for messages written
	// before migration 019 and for user messages.
	DurationMs *int      `json:"duration_ms,omitempty"`
	StopReason string    `json:"stop_reason,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ChunkPreview is the minimum a UI needs to render an evidence card. The
// DocID field is the OpenSearch chunk handle — same id used by /api/search
// hits and /api/documents/{id} — so it doubles as a stable graph pointer
// from any persisted chat message back to its grounding sources.
type ChunkPreview struct {
	DocID    string `json:"id"`
	Title    string `json:"title"`
	Source   string `json:"source"`
	Date     string `json:"date,omitempty"`
	Headline string `json:"headline,omitempty"`
	// MimeType is the retrieved chunk's content type when known. The FE
	// uses an image/* prefix to render an inline thumbnail (fetched via
	// /api/documents/{id}/content) below the evidence card. Empty for
	// chunks with no binary (most text chunks).
	MimeType string `json:"mime_type,omitempty"`
}

// ChatCitation pins an assistant claim to a retrieved document. SpanStart
// and SpanEnd are character offsets into the assistant's emitted text
// (post-marker-stripping for non-Anthropic providers).
type ChatCitation struct {
	DocID     string `json:"doc_id"`
	CitedText string `json:"cited_text,omitempty"`
	SpanStart int    `json:"span_start"`
	SpanEnd   int    `json:"span_end"`
}

// ChatToolCall is a forward-compat record of a tool invocation made by
// the assistant. Phase 5 populates this; Phase 2 leaves it nil.
//
// Chunks denormalises the per-call result chunks so the FE persisted
// ToolTrace expand-body can render after page reload — the union on
// ChatMessage.evidence is deduped across calls and doesn't preserve
// per-call attribution. Empty when the tool returned zero results
// (renders as "No matching documents.").
type ChatToolCall struct {
	Name          string         `json:"name"`
	ArgsJSON      string         `json:"args"`
	ResultID      string         `json:"result_id,omitempty"`
	ResultSummary string         `json:"result_summary,omitempty"`
	Chunks        []ChunkPreview `json:"chunks,omitempty"`
}

// ChatUsage records token accounting for an assistant turn. Cache fields
// only populate when the provider exposes them (Anthropic).
type ChatUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}
