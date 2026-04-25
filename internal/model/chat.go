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
	ID         uuid.UUID      `json:"id"`
	ChatID     uuid.UUID      `json:"chat_id"`
	Role       ChatRole       `json:"role"`
	Seq        int            `json:"seq"`
	Content    string         `json:"content"`
	Model      string         `json:"model,omitempty"`
	Citations  []ChatCitation `json:"citations,omitempty"`
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`
	Usage      *ChatUsage     `json:"usage,omitempty"`
	StopReason string         `json:"stop_reason,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
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
type ChatToolCall struct {
	Name          string `json:"name"`
	ArgsJSON      string `json:"args"`
	ResultID      string `json:"result_id,omitempty"`
	ResultSummary string `json:"result_summary,omitempty"`
}

// ChatUsage records token accounting for an assistant turn. Cache fields
// only populate when the provider exposes them (Anthropic).
type ChatUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}
