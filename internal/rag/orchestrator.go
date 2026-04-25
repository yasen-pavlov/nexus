package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// SearchProvider runs Nexus's hybrid retrieval + reranking pipeline.
// Implemented by *api.SearchService — defined as an interface here so
// internal/rag stays free of internal/api imports and trivially fakeable.
type SearchProvider interface {
	Run(ctx context.Context, req model.SearchRequest) (*model.SearchResult, error)
}

// RegistryFunc returns the live llm.Registry. Pulled per call so the
// orchestrator picks up admin hot-reloads of provider keys / allowlist
// without restart.
type RegistryFunc func() llm.Registry

// ChatStore is the persistence surface the orchestrator needs. Mirrors
// the methods on *store.Store; defined as an interface so tests fake
// it in-memory.
type ChatStore interface {
	GetChat(ctx context.Context, id uuid.UUID) (*model.Chat, error)
	ListMessages(ctx context.Context, chatID uuid.UUID) ([]model.ChatMessage, error)
	AppendMessage(ctx context.Context, msg *model.ChatMessage) error
}

// Config tunes the orchestrator. DefaultConfig() is the right starting
// point; Phase 4 adds admin-tunable knobs as a `/api/settings/rag`
// endpoint.
type Config struct {
	// MaxEvidenceChunks caps the number of retrieved chunks fed to the
	// LLM. Default 10. Larger values trade context budget for recall.
	MaxEvidenceChunks int
	// HistoryTurns caps how many prior user+assistant pairs are
	// resent on each turn. Default 3 (= 6 messages).
	HistoryTurns int
	// MaxTokens is the per-turn output cap. Default 4096.
	MaxTokens int
	// SystemPrompt is the static instruction prefix; Default systemPromptDefault.
	SystemPrompt string
}

// DefaultConfig returns the orchestrator's compiled-in defaults.
func DefaultConfig() Config {
	return Config{
		MaxEvidenceChunks: 10,
		HistoryTurns:      3,
		MaxTokens:         4096,
		SystemPrompt:      systemPromptDefault,
	}
}

// Deps groups the orchestrator's dependencies for clarity at the call
// site (cmd/nexus/main.go).
type Deps struct {
	Registry RegistryFunc
	Search   SearchProvider
	Chats    ChatStore
	Cfg      Config
	Log      *zap.Logger
}

// Orchestrator runs a single user turn end-to-end: persist user message
// → retrieve → call LLM → fan events → persist assistant message.
type Orchestrator struct {
	registry RegistryFunc
	search   SearchProvider
	chats    ChatStore
	cfg      Config
	log      *zap.Logger
}

// NewOrchestrator wires the orchestrator. All Deps fields must be
// non-nil; missing Cfg fields fall back to DefaultConfig values.
func NewOrchestrator(d Deps) *Orchestrator {
	cfg := d.Cfg
	def := DefaultConfig()
	if cfg.MaxEvidenceChunks <= 0 {
		cfg.MaxEvidenceChunks = def.MaxEvidenceChunks
	}
	if cfg.HistoryTurns <= 0 {
		cfg.HistoryTurns = def.HistoryTurns
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = def.MaxTokens
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = def.SystemPrompt
	}
	return &Orchestrator{
		registry: d.Registry,
		search:   d.Search,
		chats:    d.Chats,
		cfg:      cfg,
		log:      d.Log,
	}
}

// RunInput is the single user turn the orchestrator runs.
type RunInput struct {
	ChatID  uuid.UUID
	UserID  uuid.UUID // for per-user retrieval scoping
	Content string    // the user's message
	Model   string    // per-message override; empty = chat default → registry default
}

// ErrChatNotFound is returned when the chat doesn't exist or has been
// deleted; callers map this to HTTP 404. The orchestrator does NOT
// enforce ownership — that's the caller's job.
var ErrChatNotFound = errors.New("rag: chat not found")

// Run executes one turn. Returns a read-only event channel; the channel
// closes after exactly one EvDone (which may carry stop_reason=cancelled
// or error). Errors before the goroutine starts are returned synchronously.
func (o *Orchestrator) Run(ctx context.Context, in RunInput) (<-chan Event, error) {
	chat, err := o.chats.GetChat(ctx, in.ChatID)
	if err != nil {
		return nil, err
	}
	if chat == nil {
		return nil, ErrChatNotFound
	}

	// Resolve model: per-request → chat default → registry default.
	modelID := strings.TrimSpace(in.Model)
	if modelID == "" {
		modelID = strings.TrimSpace(chat.DefaultModel)
	}
	registry := o.registry()
	if modelID == "" {
		// Pick the first visible model as a fallback; if none are
		// configured we fail fast.
		visible := registry.Models()
		if len(visible) == 0 {
			return nil, errors.New("rag: no LLM models configured")
		}
		modelID = visible[0].ID
	}
	gen, info, err := registry.Get(modelID)
	if err != nil {
		return nil, fmt.Errorf("rag: resolve model: %w", err)
	}

	// Persist the user turn before any retrieval — the user message is
	// part of history for the next turn even if generation fails.
	userMsg := &model.ChatMessage{
		ChatID:  in.ChatID,
		Role:    model.ChatRoleUser,
		Content: in.Content,
	}
	if err := o.chats.AppendMessage(ctx, userMsg); err != nil {
		return nil, fmt.Errorf("rag: persist user message: %w", err)
	}

	_ = chat // reserved for Phase 4 title generation
	out := make(chan Event, 32)
	go o.runTurn(ctx, in, modelID, info, gen, out)
	return out, nil
}

// runTurn owns the output channel and is responsible for closing it
// after exactly one EvDone.
func (o *Orchestrator) runTurn(ctx context.Context, in RunInput, modelID string, info llm.ModelInfo, gen llm.Generator, out chan<- Event) {
	defer close(out)

	// Helper that always persists an assistant message at the end. We
	// run it via a sync.Once so cancellation + error paths can't double-write.
	var (
		finalOnce       sync.Once
		assistantID     uuid.UUID
		accumulatedText strings.Builder
		citations       []model.ChatCitation
		usage           *model.ChatUsage
	)

	persistAndDone := func(stop string, errMsg string) {
		finalOnce.Do(func() {
			// Always use the parent's request context for persistence —
			// this is bounded work and important even on cancel.
			persistCtx := context.Background()
			msg := &model.ChatMessage{
				ID:         uuid.New(),
				ChatID:     in.ChatID,
				Role:       model.ChatRoleAssistant,
				Content:    accumulatedText.String(),
				Model:      modelID,
				Citations:  citations,
				Usage:      usage,
				StopReason: stop,
			}
			if err := o.chats.AppendMessage(persistCtx, msg); err != nil {
				o.log.Error("rag: persist assistant message", zap.Error(err))
				// Don't crash — we still want EvDone downstream.
			}
			assistantID = msg.ID
			if errMsg != "" {
				out <- Event{Kind: EvError, Err: errMsg}
			}
			out <- Event{Kind: EvDone, StopReason: stop, MessageID: assistantID}
		})
	}

	// --- Retrieval ---
	out <- Event{Kind: EvRetrieving, Query: in.Content}

	searchReq := model.SearchRequest{
		Query:   in.Content,
		Limit:   o.cfg.MaxEvidenceChunks,
		OwnerID: in.UserID.String(),
	}
	result, err := o.search.Run(ctx, searchReq)
	if err != nil {
		o.log.Warn("rag: retrieval failed", zap.Error(err))
		persistAndDone("error", "retrieval failed: "+err.Error())
		return
	}
	docs := buildLLMDocs(result.Documents, o.cfg.MaxEvidenceChunks)
	previews := buildPreviews(result.Documents, o.cfg.MaxEvidenceChunks)
	out <- Event{Kind: EvEvidence, Evidence: previews}

	// --- History packing ---
	history, err := o.packHistory(ctx, in.ChatID, o.cfg.HistoryTurns, in.Content)
	if err != nil {
		o.log.Warn("rag: history packing failed", zap.Error(err))
		// Non-fatal — proceed with no history rather than fail the turn.
		history = nil
	}

	// --- Build LLM request ---
	// The adapter expects the BARE model id ("claude-sonnet-4-6"), not the
	// provider-prefixed id we use for routing ("anthropic:claude-sonnet-4-6").
	// Anthropic's SDK 404s on the prefixed form.
	bareModel := info.BareID
	if bareModel == "" {
		bareModel = modelID
	}
	llmReq := llm.GenerateRequest{
		Model:       bareModel,
		System:      o.cfg.SystemPrompt,
		Documents:   docs,
		Messages:    append(history, llm.Message{Role: llm.RoleUser, Content: in.Content}),
		MaxTokens:   o.cfg.MaxTokens,
		EnableCache: true,
	}

	events, err := gen.Generate(ctx, llmReq)
	if err != nil {
		persistAndDone("error", "generate failed: "+err.Error())
		return
	}

	// --- Fan events ---
	parser := NewCitationParser(parserDocsFromLLM(docs))
	useNativeCitations := info.SupportsCitations

	for {
		select {
		case <-ctx.Done():
			// Drain best-effort: emit Done and persist what we have.
			persistAndDone("cancelled", "")
			return
		case ev, ok := <-events:
			if !ok {
				// Channel closed without an EventDone — treat as error.
				persistAndDone("error", "stream closed unexpectedly")
				return
			}
			switch ev.Kind {
			case llm.EventText:
				if useNativeCitations {
					accumulatedText.WriteString(ev.TextDelta)
					out <- Event{Kind: EvText, TextDelta: ev.TextDelta}
				} else {
					clean, cites := parser.Feed(ev.TextDelta)
					if clean != "" {
						accumulatedText.WriteString(clean)
						out <- Event{Kind: EvText, TextDelta: clean}
					}
					for i := range cites {
						citations = append(citations, cites[i])
						c := cites[i]
						out <- Event{Kind: EvCitation, Citation: &c}
					}
				}
			case llm.EventCitation:
				if ev.Citation != nil {
					cite := model.ChatCitation{
						DocID:     ev.Citation.DocID,
						CitedText: ev.Citation.CitedText,
						SpanStart: ev.Citation.SpanStart,
						SpanEnd:   ev.Citation.SpanEnd,
					}
					citations = append(citations, cite)
					out <- Event{Kind: EvCitation, Citation: &cite}
				}
			case llm.EventToolCall:
				// Phase 5 wires this. For Phase 2 we don't include
				// any tools so this should never fire — log and skip.
				o.log.Warn("rag: unexpected tool call delta in Phase 2")
			case llm.EventDone:
				// Flush any trailing partial marker as plain text.
				if !useNativeCitations {
					if tail := parser.Flush(); tail != "" {
						accumulatedText.WriteString(tail)
						out <- Event{Kind: EvText, TextDelta: tail}
					}
				}
				if ev.Usage != nil {
					usage = &model.ChatUsage{
						Input:      ev.Usage.InputTokens,
						Output:     ev.Usage.OutputTokens,
						CacheRead:  ev.Usage.CacheReadTokens,
						CacheWrite: ev.Usage.CacheWriteTokens,
					}
					out <- Event{Kind: EvUsage, Usage: usage}
				}
				stop := string(ev.StopReason)
				if stop == "" {
					stop = string(llm.StopEnd)
				}
				persistAndDone(stop, "")
				return
			case llm.EventError:
				msg := "generation failed"
				if ev.Err != nil {
					msg = ev.Err.Error()
				}
				persistAndDone("error", msg)
				return
			}
		}
	}
}

// packHistory loads prior messages for the chat and returns up to
// `2*turns` of them (in order, user/assistant only). Drops the
// just-appended user turn (its content arrives separately as the
// final Messages entry built by Run).
func (o *Orchestrator) packHistory(ctx context.Context, chatID uuid.UUID, turns int, currentContent string) ([]llm.Message, error) {
	if turns <= 0 {
		return nil, nil
	}
	msgs, err := o.chats.ListMessages(ctx, chatID)
	if err != nil {
		return nil, err
	}
	// Drop the just-appended user message — Run packs it as the final
	// turn separately to keep history + question explicit.
	if n := len(msgs); n > 0 && msgs[n-1].Role == model.ChatRoleUser && msgs[n-1].Content == currentContent {
		msgs = msgs[:n-1]
	}
	// Skip tool turns (Phase 2 doesn't generate any, but be defensive).
	filtered := make([]model.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == model.ChatRoleTool {
			continue
		}
		filtered = append(filtered, m)
	}
	max := 2 * turns
	if len(filtered) > max {
		filtered = filtered[len(filtered)-max:]
	}
	out := make([]llm.Message, 0, len(filtered))
	for _, m := range filtered {
		role := llm.RoleUser
		if m.Role == model.ChatRoleAssistant {
			role = llm.RoleAssistant
		}
		out = append(out, llm.Message{Role: role, Content: m.Content})
	}
	return out, nil
}

// buildLLMDocs maps DocumentHits → llm.Documents, capped by max. Uses
// the chunk's Headline (or first 800 chars of Content) as the LLM-side
// text to keep token costs reasonable. The full content is still
// retrievable via /api/documents if the user wants to drill down.
func buildLLMDocs(hits []model.DocumentHit, max int) []llm.Document {
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]llm.Document, 0, len(hits))
	for _, h := range hits {
		date := ""
		if !h.CreatedAt.IsZero() {
			date = h.CreatedAt.Format("2006-01-02")
		}
		content := h.Content
		if content == "" {
			content = h.Headline
		}
		out = append(out, llm.Document{
			ID:      h.ID.String(),
			Title:   h.Title,
			Source:  h.SourceType,
			Date:    date,
			Content: content,
		})
	}
	return out
}

// buildPreviews maps DocumentHits → ChunkPreview for the EvEvidence
// event the SSE layer streams to the UI.
func buildPreviews(hits []model.DocumentHit, max int) []ChunkPreview {
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]ChunkPreview, 0, len(hits))
	for _, h := range hits {
		date := ""
		if !h.CreatedAt.IsZero() {
			date = h.CreatedAt.Format("2006-01-02")
		}
		out = append(out, ChunkPreview{
			DocID:    h.ID.String(),
			Title:    h.Title,
			Source:   h.SourceType,
			Date:     date,
			Headline: h.Headline,
		})
	}
	return out
}

// parserDocsFromLLM extracts the parser-shaped doc list from the same
// llm.Documents the model received. The 1-based ordering is preserved
// so [N] markers map to the right doc.
func parserDocsFromLLM(docs []llm.Document) []ParserDoc {
	out := make([]ParserDoc, len(docs))
	for i, d := range docs {
		out[i] = ParserDoc{DocID: d.ID}
	}
	return out
}
