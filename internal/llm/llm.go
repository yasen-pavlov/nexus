// Package llm provides a unified interface over chat-completion / answer
// generation providers (Anthropic, OpenAI, Ollama) used by the RAG layer.
//
// The interface is provider-neutral: callers describe the request as
// (system + retrieved documents + tools + conversation history + question)
// and read a stream of typed Events. Provider adapters live under
// internal/llm/<provider>/.
//
// The Event channel's contract:
//   - Channel is closed after exactly one EventDone or EventError event.
//   - EventText carries an incremental text delta.
//   - EventCitation is provider-aware: Anthropic emits these natively from
//     citations_delta; other providers rely on the orchestrator's [N]
//     parser (not in this package).
//   - EventToolCall arguments accumulate across deltas; Final=true marks the
//     end of a tool call's arguments.
//   - ctx cancellation aborts the underlying SDK call and emits a final
//     EventDone with StopCancelled.
package llm

import "context"

// Role names roles in the conversation. The provider adapters map these to
// their wire equivalents.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Document is a retrieved chunk passed to the model for grounded answering.
// The Anthropic adapter wraps each Document as a citation-enabled custom-content
// block; other adapters render them into the system/context prefix with [N]
// markers the orchestrator-side parser can lift back into Citation events.
//
// ID must be a stable Nexus chunk handle so emitted Citation events can be
// joined back to the original chunk by the caller.
type Document struct {
	ID      string
	Title   string
	Source  string // filesystem|email|telegram|paperless|...
	Date    string // ISO date (YYYY-MM-DD) when known
	Content string
	// Optional multi-modal payload. Adapters that don't support vision drop it.
	Images []Image
}

// Image is an inline image attached to a Document. Adapters base64-encode as
// each provider's wire format requires.
type Image struct {
	MediaType string // e.g. "image/png", "image/jpeg"
	Data      []byte // raw bytes
	SourceID  string // for citation back-mapping
}

// Message is one turn of conversation history. Tool turns reference the
// originating call via ToolCallID; assistant turns may carry ToolCalls when the
// orchestrator persists a pre-tool message before the tool result lands.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // assistant turns
	ToolCallID string     // tool turns reference the assistant's tool_use id
}

// ToolCall is a fully-formed tool invocation in a persisted message.
type ToolCall struct {
	ID       string
	Name     string
	ArgsJSON string
}

// Tool is a JSON-Schema-described function the model may invoke.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema (object with properties, required)
}

// GenerateRequest is the unified shape all adapters consume.
type GenerateRequest struct {
	// Model is registry-resolved, e.g. "anthropic:claude-sonnet-4-6". The
	// adapter for the resolved provider only sees the bare model id.
	Model       string
	System      string
	Documents   []Document
	Messages    []Message
	Tools       []Tool
	MaxTokens   int
	Temperature *float32
	// EnableCache hints prompt caching where supported. Anthropic uses
	// explicit cache_control breakpoints; OpenAI auto-prefix-caches; Ollama
	// relies on the model's KV cache. Adapters that don't have an API
	// surface for caching ignore the hint.
	EnableCache bool
}

// StopReason is the normalized reason a stream ended. Adapters map their
// provider-specific stop reasons into this enum so callers don't branch on
// provider strings.
type StopReason string

const (
	StopEnd       StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	StopFiltered  StopReason = "filtered"
	StopCancelled StopReason = "cancelled"
)

// EventKind discriminates Event payloads. Use the matching field; others are
// zero-valued.
type EventKind int

const (
	EventText EventKind = iota
	EventCitation
	EventToolCall
	EventDone
	EventError
)

// Event is the single streaming payload type. Exactly one payload field is
// populated per Kind. The channel emitting Events is closed after exactly one
// EventDone or EventError.
type Event struct {
	Kind       EventKind
	TextDelta  string         // EventText
	Citation   *Citation      // EventCitation
	ToolCall   *ToolCallDelta // EventToolCall
	StopReason StopReason     // EventDone
	Usage      *Usage         // EventDone (may be nil if provider didn't report)
	Err        error          // EventError
}

// Citation ties a span of generated text back to a source Document. DocID
// matches Document.ID supplied in the request.
//
// Anthropic populates CitedText with the quoted source text — that text does
// not count against output token billing on Anthropic. Other providers leave
// it empty (the orchestrator's [N] parser fills it from the source chunk).
type Citation struct {
	DocID     string
	CitedText string
	SpanStart int // char offset in the answer text where the citation attaches
	SpanEnd   int
}

// ToolCallDelta accumulates a streaming tool-use call. Callers buffer ArgsJSON
// across deltas and treat Final=true as the boundary at which the args are
// safe to json.Unmarshal.
type ToolCallDelta struct {
	ID       string
	Name     string
	ArgsJSON string
	Final    bool
}

// Usage reports token accounting for an entire response. CacheRead /
// CacheWrite are provider-specific (Anthropic-only at this time).
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheWriteTokens int
	CacheReadTokens  int
}

// Generator generates a streaming chat completion.
type Generator interface {
	// Generate kicks off generation. The returned channel emits Events until
	// it is closed (after exactly one EventDone or EventError). Cancel via ctx.
	Generate(ctx context.Context, req GenerateRequest) (<-chan Event, error)
}

// ModelInfo describes a model the registry can route to. Capability flags
// drive UI affordances and orchestrator decisions (e.g. only attach images
// when SupportsVision).
type ModelInfo struct {
	ID                string // provider-prefixed: "anthropic:claude-sonnet-4-6"
	Provider          string
	BareID            string // "claude-sonnet-4-6"
	DisplayName       string
	ContextWindow     int
	SupportsCitations bool
	SupportsTools     bool
	SupportsVision    bool
	SupportsCaching   bool
	InputCostPerMtok  float64
	OutputCostPerMtok float64
	TypicalTTFTms     int
}

// Registry routes provider-prefixed model ids to a Generator + ModelInfo.
// Models() returns the post-allowlist set the UI surfaces. AllConfiguredModels()
// returns the pre-allowlist set (every catalog/extras model whose provider
// has a Generator) — used by the admin allowlist editor so deselected models
// don't disappear from the UI.
type Registry interface {
	Get(model string) (Generator, ModelInfo, error)
	Models() []ModelInfo
	AllConfiguredModels() []ModelInfo
}
