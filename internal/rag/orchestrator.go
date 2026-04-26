package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/store"
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

// Settings carries the runtime knobs the orchestrator reads per turn.
// SettingsFunc returns the live snapshot so admin saves to settings
// (e.g. swapping the rewriter model) take effect without restart —
// mirrors the RegistryFunc pattern.
type Settings struct {
	// RewriterModel is the cheap-model id (provider-prefixed) used for
	// query rewriting AND auto-titling. Empty disables both features.
	RewriterModel string
}

// SettingsFunc is read once per turn from runTurn so orchestrator
// behaviour reflects the latest admin settings without a process
// restart.
type SettingsFunc func() Settings

// ChatStore is the persistence surface the orchestrator needs. Mirrors
// the methods on *store.Store; defined as an interface so tests fake
// it in-memory.
type ChatStore interface {
	GetChat(ctx context.Context, id uuid.UUID) (*model.Chat, error)
	ListMessages(ctx context.Context, chatID uuid.UUID) ([]model.ChatMessage, error)
	AppendMessage(ctx context.Context, msg *model.ChatMessage) error
	UpdateChat(ctx context.Context, id uuid.UUID, fields store.ChatUpdate) error
}

// Config tunes the orchestrator. DefaultConfig() is the right starting
// point.
type Config struct {
	// MaxEvidenceChunks caps the number of retrieved chunks fed to the
	// LLM. Default 10.
	MaxEvidenceChunks int
	// HistoryTurns caps how many prior user+assistant pairs are
	// resent on each turn. Default 3 (= 6 messages).
	HistoryTurns int
	// MaxTokens is the per-turn output cap. Default 4096.
	MaxTokens int
	// SystemPrompt is the static instruction prefix; default systemPromptDefault.
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
	Settings SettingsFunc
	Search   SearchProvider
	Chats    ChatStore
	Cfg      Config
	Log      *zap.Logger
}

// Orchestrator runs a single user turn end-to-end: persist user message
// → retrieve → call LLM → fan events → persist assistant message. As of
// Phase 4 it also runs a turn-aware query rewriter (Haiku-class cheap
// model) before retrieval, may skip retrieval entirely on the
// rewriter's recommendation, and writes an auto-title on the first
// assistant turn. Rewriter and title token usage is aggregated into the
// turn's persisted Usage so cost reporting includes the supporting
// calls.
type Orchestrator struct {
	registry RegistryFunc
	settings SettingsFunc
	search   SearchProvider
	chats    ChatStore
	cfg      Config
	log      *zap.Logger
}

// NewOrchestrator wires the orchestrator. All Deps fields must be
// non-nil; missing Cfg fields fall back to DefaultConfig values. A nil
// Settings closure is treated as "no rewriter, no auto-title" — the
// orchestrator still runs the legacy single-shot flow.
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
	settings := d.Settings
	if settings == nil {
		settings = func() Settings { return Settings{} }
	}
	return &Orchestrator{
		registry: d.Registry,
		settings: settings,
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

	modelID := strings.TrimSpace(in.Model)
	if modelID == "" {
		modelID = strings.TrimSpace(chat.DefaultModel)
	}
	registry := o.registry()
	if modelID == "" {
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

	userMsg := &model.ChatMessage{
		ChatID:  in.ChatID,
		Role:    model.ChatRoleUser,
		Content: in.Content,
	}
	if err := o.chats.AppendMessage(ctx, userMsg); err != nil {
		return nil, fmt.Errorf("rag: persist user message: %w", err)
	}

	out := make(chan Event, 32)
	go o.runTurn(ctx, in, chat, modelID, info, gen, out)
	return out, nil
}

// runTurn owns the output channel and is responsible for closing it
// after exactly one EvDone.
func (o *Orchestrator) runTurn(ctx context.Context, in RunInput, chat *model.Chat, modelID string, info llm.ModelInfo, gen llm.Generator, out chan<- Event) {
	defer close(out)

	// Server-measured turn start. Captured here (after user persist,
	// before retrieval/generation) so the persisted duration_ms is the
	// orchestrator's wall-clock and stays stable across page refreshes
	// — the FE's live "completedAt − startedAt" timer captures network
	// + SSE-flush latency too and would drift after refresh.
	turnStart := time.Now()

	var (
		finalOnce       sync.Once
		assistantID     uuid.UUID
		accumulatedText strings.Builder
		citations       []model.ChatCitation
		evidence        []ChunkPreview
		// usage is the LLM call's reported tokens. auxUsage accumulates
		// rewriter + title costs so the persisted total reflects the
		// full turn cost (plan §"Token cost accounting").
		usage    *model.ChatUsage
		auxUsage model.ChatUsage
		// rewriterRan tracks whether the rewriter modified the search
		// query so the persisted message can carry the original
		// rewritten form for post-hoc inspection (FE phase strip on
		// reload, eval harness, debug).
		rewrittenQuery   string
		skippedRetrieval bool
		// pendingCitations buffers Anthropic citations until the next
		// sentence terminator so pills land at clean sentence
		// boundaries instead of mid-word.
		pendingCitations []model.ChatCitation
	)

	settings := o.settings()
	rewriterModelID := strings.TrimSpace(settings.RewriterModel)

	byteToUTF16 := func(byteOffset int) int {
		s := accumulatedText.String()
		if byteOffset > len(s) {
			byteOffset = len(s)
		}
		n := 0
		for _, r := range s[:byteOffset] {
			if r >= 0x10000 {
				n += 2
			} else {
				n++
			}
		}
		return n
	}

	flushAllPendingAt := func(anchor int) {
		if len(pendingCitations) == 0 {
			return
		}
		for i := range pendingCitations {
			pendingCitations[i].SpanStart = anchor
			pendingCitations[i].SpanEnd = anchor
			citations = append(citations, pendingCitations[i])
			c := pendingCitations[i]
			out <- Event{Kind: EvCitation, Citation: &c}
		}
		pendingCitations = pendingCitations[:0]
	}

	distributePendingAtBoundaries := func(text string, fromByte int) {
		cursor := fromByte
		for len(pendingCitations) > 0 {
			b := nextSentenceBoundary(text, cursor)
			if b == 0 {
				return
			}
			runeAnchor := byteToUTF16(b)
			head := pendingCitations[0]
			head.SpanStart = runeAnchor
			head.SpanEnd = runeAnchor
			citations = append(citations, head)
			out <- Event{Kind: EvCitation, Citation: &head}
			pendingCitations = pendingCitations[1:]
			cursor = b
		}
	}

	// addUsage merges a per-call llm.Usage into the running auxUsage
	// accumulator. Tolerates nil. Used for both rewriter and title
	// calls so a single Generate boundary doesn't lose tokens.
	addUsage := func(u *llm.Usage) {
		if u == nil {
			return
		}
		auxUsage.Input += u.InputTokens
		auxUsage.Output += u.OutputTokens
		auxUsage.CacheRead += u.CacheReadTokens
		auxUsage.CacheWrite += u.CacheWriteTokens
	}

	// mergeUsage produces the final ChatUsage for persistence by adding
	// auxUsage on top of the streamed-LLM usage. Returns nil when both
	// are zero (no LLM calls were billed).
	mergeUsage := func() *model.ChatUsage {
		if usage == nil && auxUsage == (model.ChatUsage{}) {
			return nil
		}
		merged := model.ChatUsage{}
		if usage != nil {
			merged = *usage
		}
		merged.Input += auxUsage.Input
		merged.Output += auxUsage.Output
		merged.CacheRead += auxUsage.CacheRead
		merged.CacheWrite += auxUsage.CacheWrite
		return &merged
	}

	// persistAssistant writes the assistant message row. Idempotent via
	// finalOnce so the title + done emission below can't double-write
	// on the cancellation/error path. Captures the wall-clock duration
	// from the runTurn start so the FE can render a stable label that
	// agrees across the live view and the post-refresh persisted view.
	var persistedDurationMs int
	persistAssistant := func(stop string) {
		persistCtx := context.Background()
		duration := int(time.Since(turnStart) / time.Millisecond)
		persistedDurationMs = duration
		msg := &model.ChatMessage{
			ID:               uuid.New(),
			ChatID:           in.ChatID,
			Role:             model.ChatRoleAssistant,
			Content:          accumulatedText.String(),
			Model:            modelID,
			Citations:        citations,
			Evidence:         evidence,
			Usage:            mergeUsage(),
			StopReason:       stop,
			RewrittenQuery:   rewrittenQuery,
			SkippedRetrieval: skippedRetrieval,
			DurationMs:       &duration,
		}
		if err := o.chats.AppendMessage(persistCtx, msg); err != nil {
			o.log.Error("rag: persist assistant message", zap.Error(err))
		}
		assistantID = msg.ID
	}

	// persistAndDone runs persist + auto-title + emit-done as a single
	// idempotent finalisation. Auto-title only runs on the first
	// successful end_turn assistant message AND when the rewriter model
	// is configured (the title and rewriter share one cheap-model setting).
	persistAndDone := func(stop string, errMsg string, isFirstAssistantTurn bool, userQuestion string) {
		finalOnce.Do(func() {
			persistAssistant(stop)

			// Auto-title — best-effort, fire only on the first
			// successful end_turn. parentCtx is intentionally
			// context.Background(): if the SSE was just cancelled
			// we won't be here (stop != end_turn). If the parent
			// was healthy when we arrived but happens to die
			// during the 2s title call, we still want the title
			// to land — auto-title is supportive infrastructure.
			if rewriterModelID != "" &&
				stop == string(llm.StopEnd) &&
				isFirstAssistantTurn &&
				strings.TrimSpace(chat.Title) == "" &&
				strings.TrimSpace(accumulatedText.String()) != "" {

				registry := o.registry()
				if titleGen, titleInfo, err := registry.Get(rewriterModelID); err == nil {
					title, tu, titleReason := summarizeForTitle(context.Background(), titleGen, titleInfo, userQuestion, accumulatedText.String(), o.log)
					addUsage(tu)
					switch {
					case title != "":
						if err := o.chats.UpdateChat(context.Background(), in.ChatID, store.ChatUpdate{Title: &title}); err != nil {
							o.log.Warn("rag: persist auto-title", zap.Error(err))
							out <- Event{Kind: EvTitleStatus, StatusReason: titleFailureError}
						} else {
							out <- Event{Kind: EvTitle, Title: title}
						}
					case titleReason != "":
						// Title attempt failed (timeout / empty / error)
						// — surface to the FE diagnostic glyph so admins
						// know titles aren't silently disabled.
						out <- Event{Kind: EvTitleStatus, StatusReason: titleReason}
					}
				} else {
					o.log.Info("rag: skipping auto-title; rewriter model not resolvable", zap.Error(err))
					out <- Event{Kind: EvTitleStatus, StatusReason: titleFailureError}
				}
			}

			if errMsg != "" {
				out <- Event{Kind: EvError, Err: errMsg}
			}
			out <- Event{Kind: EvDone, StopReason: stop, MessageID: assistantID, DurationMs: persistedDurationMs}
		})
	}

	// --- History packing (moved BEFORE retrieval in Phase 4 so the
	// rewriter has history to consume) ---
	history, err := o.packHistory(ctx, in.ChatID, o.cfg.HistoryTurns, in.Content)
	if err != nil {
		o.log.Warn("rag: history packing failed", zap.Error(err))
		history = nil
	}

	// Count prior assistant turns. Gate the rewriter and the auto-title
	// on this rather than raw history length: a regenerate-of-first-turn
	// has non-empty history (user message + failed assistant), but
	// rewriting and titling are still meaningless there.
	priorAssistantTurns := 0
	for _, m := range history {
		if m.Role == llm.RoleAssistant {
			priorAssistantTurns++
		}
	}
	isFirstAssistantTurn := priorAssistantTurns == 0

	// Cancellation check before doing anything expensive — if the SSE
	// already disconnected, abort cleanly without touching retrieval.
	if err := ctx.Err(); err != nil {
		persistAndDone("cancelled", "", isFirstAssistantTurn, in.Content)
		return
	}

	// --- Rewriter step (Phase 4) ---
	searchQuery := in.Content
	needsRetrieval := true
	if rewriterModelID != "" && priorAssistantTurns > 0 {
		registry := o.registry()
		if rewriterGen, rewriterInfo, err := registry.Get(rewriterModelID); err != nil {
			o.log.Warn("rag: rewriter model not resolvable; skipping rewrite", zap.Error(err))
			out <- Event{Kind: EvRewriterStatus, StatusReason: rewriterFailureError}
		} else {
			res := rewriteQuery(ctx, rewriterGen, rewriterInfo, history, in.Content, o.log)
			addUsage(res.Usage)
			if strings.TrimSpace(res.Rewritten) != "" {
				searchQuery = res.Rewritten
				rewrittenQuery = res.Rewritten
			}
			needsRetrieval = res.NeedsRetrieval
			if res.FailureReason != "" {
				// Surface the rewriter fallback to the FE so the phase
				// chip can render a quiet diagnostic glyph instead of
				// silently looking like "no rewrite was needed".
				out <- Event{Kind: EvRewriterStatus, StatusReason: res.FailureReason}
			}
		}
	}

	// Cancellation check after rewriter, before retrieval.
	if err := ctx.Err(); err != nil {
		persistAndDone("cancelled", "", isFirstAssistantTurn, in.Content)
		return
	}

	// --- Retrieval (only when the rewriter said yes) ---
	var docs []llm.Document
	if needsRetrieval {
		out <- Event{Kind: EvRetrieving, Query: searchQuery}
		searchReq := model.SearchRequest{
			Query:   searchQuery,
			Limit:   o.cfg.MaxEvidenceChunks,
			OwnerID: in.UserID.String(),
		}
		result, err := o.search.Run(ctx, searchReq)
		if err != nil {
			o.log.Warn("rag: retrieval failed", zap.Error(err))
			persistAndDone("error", "retrieval failed: "+err.Error(), isFirstAssistantTurn, in.Content)
			return
		}
		docs = buildLLMDocs(result.Documents, o.cfg.MaxEvidenceChunks)
		previews := buildPreviews(result.Documents, o.cfg.MaxEvidenceChunks)
		evidence = previews
		out <- Event{Kind: EvEvidence, Evidence: previews}
	} else {
		skippedRetrieval = true
		out <- Event{Kind: EvSkippedRetrieval, Query: searchQuery}
	}

	// --- Build LLM request ---
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
		persistAndDone("error", "generate failed: "+err.Error(), isFirstAssistantTurn, in.Content)
		return
	}

	parser := NewCitationParser(parserDocsFromLLM(docs))
	useNativeCitations := info.SupportsCitations

	for {
		select {
		case <-ctx.Done():
			persistAndDone("cancelled", "", isFirstAssistantTurn, in.Content)
			return
		case ev, ok := <-events:
			if !ok {
				persistAndDone("error", "stream closed unexpectedly", isFirstAssistantTurn, in.Content)
				return
			}
			switch ev.Kind {
			case llm.EventText:
				if useNativeCitations {
					prevLen := accumulatedText.Len()
					accumulatedText.WriteString(ev.TextDelta)
					out <- Event{Kind: EvText, TextDelta: ev.TextDelta}
					distributePendingAtBoundaries(accumulatedText.String(), prevLen)
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
					pendingCitations = append(pendingCitations, model.ChatCitation{
						DocID:     ev.Citation.DocID,
						CitedText: ev.Citation.CitedText,
					})
				}
			case llm.EventToolCall:
				o.log.Warn("rag: unexpected tool call delta in Phase 4")
			case llm.EventDone:
				if !useNativeCitations {
					if tail := parser.Flush(); tail != "" {
						accumulatedText.WriteString(tail)
						out <- Event{Kind: EvText, TextDelta: tail}
					}
				}
				flushAllPendingAt(byteToUTF16(accumulatedText.Len()))
				if ev.Usage != nil {
					usage = &model.ChatUsage{
						Input:      ev.Usage.InputTokens,
						Output:     ev.Usage.OutputTokens,
						CacheRead:  ev.Usage.CacheReadTokens,
						CacheWrite: ev.Usage.CacheWriteTokens,
					}
				}
				if merged := mergeUsage(); merged != nil {
					out <- Event{Kind: EvUsage, Usage: merged}
				}
				stop := string(ev.StopReason)
				if stop == "" {
					stop = string(llm.StopEnd)
				}
				persistAndDone(stop, "", isFirstAssistantTurn, in.Content)
				return
			case llm.EventError:
				flushAllPendingAt(byteToUTF16(accumulatedText.Len()))
				msg := "generation failed"
				if ev.Err != nil {
					msg = ev.Err.Error()
				}
				persistAndDone("error", msg, isFirstAssistantTurn, in.Content)
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
	if n := len(msgs); n > 0 && msgs[n-1].Role == model.ChatRoleUser && msgs[n-1].Content == currentContent {
		msgs = msgs[:n-1]
	}
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

// buildLLMDocs maps DocumentHits → llm.Documents, capped by max.
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

// nextSentenceBoundary scans text[start:] for the first sentence
// terminator and returns the offset right after it.
//
// Heuristics tuned for streaming markdown chat output:
//   - A bare `\n` is a hard boundary.
//   - `.` / `!` / `?` only count as a sentence end when the NEXT
//     character is whitespace (avoids decimals `4.7`, money `€107.10`,
//     version strings, ellipses).
//   - For `.` specifically, the PRECEDING character must NOT be a digit
//     (rejects markdown list markers `2. ` and `5. `).
//   - When the terminator candidate is the last character we've seen
//     so far, return 0 and wait — the next streamed delta might
//     confirm (whitespace) or reject (non-space) the boundary.
func nextSentenceBoundary(text string, start int) int {
	if start >= len(text) {
		return 0
	}
	for i := start; i < len(text); i++ {
		c := text[i]
		switch c {
		case '\n':
			return i + 1
		case '!', '?':
			if i+1 >= len(text) {
				return 0
			}
			next := text[i+1]
			if next == ' ' || next == '\t' || next == '\n' {
				return i + 1
			}
		case '.':
			if i+1 >= len(text) {
				return 0
			}
			next := text[i+1]
			if next != ' ' && next != '\t' && next != '\n' {
				continue
			}
			if i > 0 {
				prev := text[i-1]
				if prev >= '0' && prev <= '9' {
					continue
				}
			}
			return i + 1
		}
	}
	return 0
}
