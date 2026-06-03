package rag

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// ImageStore fetches a cached binary by its (sourceType, sourceName,
// sourceID) triple. Satisfied by *storage.BinaryStore. The orchestrator
// uses it for cache-ONLY reads when attaching images — a miss returns an
// error and the image is silently skipped (never a synchronous refetch,
// per the multi-modal cost guardrails in the master plan §8).
type ImageStore interface {
	Get(ctx context.Context, sourceType, sourceName, sourceID string) (io.ReadCloser, error)
}

// AttachmentResolver looks chunks up by relation or by id. Satisfied by
// *search.Client.
//   - FindChunksReferencing finds the chunks whose relations point at any
//     of the given parent docs (the reverse `attachment_of` edge) — used
//     to pull a retrieved email/Telegram message's image attachments into
//     the prompt even when the attachment chunk wasn't a top hit.
//   - GetChunkByDocID resolves a single chunk by id, backing the
//     nexus_open_attachment tool. The dispatcher re-checks ownership on
//     the returned chunk because the model supplies an arbitrary id.
type AttachmentResolver interface {
	FindChunksReferencing(ctx context.Context, targetIDs, targetSourceIDs []string) ([]model.Chunk, error)
	GetChunkByDocID(ctx context.Context, docID string) (*model.Chunk, error)
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
	// MaxToolRounds caps how many tool-use rounds the orchestrator will
	// allow during one user turn. 0 disables tools entirely (the
	// orchestrator passes Tools=nil on round 1, forcing single-shot
	// answers). Read per turn so admin saves take effect without restart.
	MaxToolRounds int
	// MaxImagesPerTurn caps how many cached image attachments the
	// orchestrator feeds to a vision-capable model per turn. 0 disables
	// image attachment. Default 4.
	MaxImagesPerTurn int
	// EnableMultimodal is the global on/off for image attachment. When
	// false the orchestrator never attaches images even to vision models.
	EnableMultimodal bool
	// EnableOpenAttachment exposes the flag-gated nexus_open_attachment
	// tool to the model (lets it pull a specific attachment by chunk id
	// mid-answer). Off by default; independent of EnableMultimodal so an
	// admin can ship auto-attachment without the agentic tool.
	EnableOpenAttachment bool
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
	// Binaries and Attachments power multi-modal image attachment. Both
	// optional — when either is nil the orchestrator simply never attaches
	// images (text-only flow), so non-multimodal tests don't need to wire
	// them.
	Binaries    ImageStore
	Attachments AttachmentResolver
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
	registry    RegistryFunc
	settings    SettingsFunc
	search      SearchProvider
	chats       ChatStore
	cfg         Config
	log         *zap.Logger
	binaries    ImageStore
	attachments AttachmentResolver
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
		registry:    d.Registry,
		settings:    settings,
		search:      d.Search,
		chats:       d.Chats,
		cfg:         cfg,
		log:         d.Log,
		binaries:    d.Binaries,
		attachments: d.Attachments,
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
		// usage is the running LLM-call token total. The Phase-5 round
		// loop accumulates tokens from every Generate call (initial +
		// each tool round) into this single counter. auxUsage on top
		// accumulates rewriter + title costs so the persisted total
		// reflects the full turn cost (plan §"Token cost accounting").
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
		// toolCallsCollected accumulates one record per tool call
		// dispatched across all rounds in this turn. Persisted as the
		// assistant message's chat_messages.tool_calls JSONB so the FE
		// can render the collapsible tool-trace rows after page reload.
		toolCallsCollected []model.ChatToolCall
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
			ToolCalls:        toolCallsCollected,
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
			// Surface the underlying failure message at WARN — every
			// error-stop path funnels through here, so a single log
			// statement covers "generate failed", "stream closed
			// unexpectedly", "retrieval failed", and EventError
			// payloads. Without this the SSE error frame is the only
			// visible diagnostic and gets lost as soon as the FE
			// closes the stream.
			if stop == "error" && errMsg != "" {
				o.log.Warn("rag: turn finalised with error",
					zap.String("err", errMsg),
					zap.String("model", modelID),
					zap.String("chat_id", in.ChatID.String()))
			}
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
			// Strip prior ASSISTANT turns from the rewriter history.
			// Cheap models (Haiku-class) take prior assistant content
			// as data to compute over and answer the question
			// themselves instead of rewriting it (one Wolt-orders
			// turn produced a literal "Total: €222.65" reply,
			// breaking the parser). User-only history still gives
			// the rewriter enough topic continuity for the common
			// coreference cases ("how much in total" → "how much
			// did I pay in total for the wolt orders"); the cases
			// that genuinely need an assistant-introduced referent
			// ("the second invoice") fall through to the tool loop.
			res := rewriteQuery(ctx, rewriterGen, rewriterInfo, userHistoryOnly(history), in.Content, o.log)
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
		// Multi-modal: attach cached images (vision models) and PDFs
		// (native-PDF models) to the docs when the admin hasn't disabled
		// it. Best-effort and cache-only — never blocks or fails the turn
		// (master plan §8).
		o.attachMedia(ctx, docs, result.Documents, info, settings)
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

	// Phase 5 round loop. Initial state:
	//   documents       — cumulative across rounds (initial + tool-fetched), deduped
	//                     by DocID. Stable order so citation indices stay valid for
	//                     non-Anthropic providers and Anthropic's document_index
	//                     also stays monotonic.
	//   rolledMessages  — the conversation we feed the LLM. Grows with each round:
	//                     [history, user, asst+tool_use, tool_result, asst+tool_use,
	//                     tool_result, ..., asst]. Ends when stop != tool_use.
	//   dispatcher      — built per turn so the per-user OwnerID is baked in
	//                     (master plan §2 hard constraint).
	documents := docs
	rolledMessages := append(history, llm.Message{Role: llm.RoleUser, Content: in.Content})
	dispatcher := newSearchToolDispatcher(o.search, o.attachments, o.binaries, in.UserID, info.SupportsVision, info.SupportsPDF, o.log)
	maxToolRounds := settings.MaxToolRounds
	useNativeCitations := info.SupportsCitations

	for toolRound := 0; ; toolRound++ {
		// Cancellation between rounds — Phase 4's pattern of checking ctx
		// before each expensive stage applies to each new generate call.
		if err := ctx.Err(); err != nil {
			persistAndDone("cancelled", "", isFirstAssistantTurn, in.Content)
			return
		}

		// Tools for this round. nil after the cap so the model is forced to
		// answer from current context — no separate "force-finish" branch
		// needed.
		var roundTools []llm.Tool
		if toolRound < maxToolRounds {
			roundTools = BuildToolList(info, maxToolRounds, settings.EnableOpenAttachment)
		}

		// Citation parser is per-round for non-Anthropic providers so [N]
		// markers map against the cumulative documents slice (which may
		// have grown since the previous round via tool results).
		var parser *CitationParser
		if !useNativeCitations {
			parser = NewCitationParser(parserDocsFromLLM(documents))
		}

		llmReq := llm.GenerateRequest{
			Model:       bareModel,
			System:      o.cfg.SystemPrompt,
			Documents:   documents,
			Messages:    rolledMessages,
			Tools:       roundTools,
			MaxTokens:   o.cfg.MaxTokens,
			EnableCache: true,
		}

		events, err := gen.Generate(ctx, llmReq)
		if err != nil {
			persistAndDone("error", "generate failed: "+err.Error(), isFirstAssistantTurn, in.Content)
			return
		}

		// Per-round accumulators. roundText feeds the assistant message we
		// append to rolledMessages so the next round sees the model's
		// reasoning (and any text it emitted alongside its tool_use blocks).
		// toolBuf accumulates ToolCallDelta.ArgsJSON across deltas keyed by
		// call ID; toolOrder preserves invocation order so we dispatch in
		// the order the model produced them.
		var (
			roundText strings.Builder
			toolBuf   = map[string]*llm.ToolCall{}
			toolOrder []string
			roundStop = llm.StopEnd
		)

	drain:
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
						roundText.WriteString(ev.TextDelta)
						out <- Event{Kind: EvText, TextDelta: ev.TextDelta}
						distributePendingAtBoundaries(accumulatedText.String(), prevLen)
					} else {
						clean, cites := parser.Feed(ev.TextDelta)
						if clean != "" {
							accumulatedText.WriteString(clean)
							roundText.WriteString(clean)
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
					if ev.ToolCall == nil {
						continue
					}
					id := ev.ToolCall.ID
					if id == "" {
						// Ollama emits Final=true tool calls without an
						// SDK-supplied ID. Synthesize one stable per
						// (round, order) so the matching tool_result
						// message round-trips ToolCallID correctly.
						id = fmt.Sprintf("ollama-r%d-%d", toolRound, len(toolOrder))
					}
					tc, exists := toolBuf[id]
					if !exists {
						tc = &llm.ToolCall{ID: id, Name: ev.ToolCall.Name}
						toolBuf[id] = tc
						toolOrder = append(toolOrder, id)
					}
					if tc.Name == "" && ev.ToolCall.Name != "" {
						tc.Name = ev.ToolCall.Name
					}
					if ev.ToolCall.ArgsJSON != "" {
						tc.ArgsJSON += ev.ToolCall.ArgsJSON
					}
				case llm.EventDone:
					if !useNativeCitations {
						if tail := parser.Flush(); tail != "" {
							accumulatedText.WriteString(tail)
							roundText.WriteString(tail)
							out <- Event{Kind: EvText, TextDelta: tail}
						}
					}
					if ev.Usage != nil {
						if usage == nil {
							usage = &model.ChatUsage{}
						}
						usage.Input += ev.Usage.InputTokens
						usage.Output += ev.Usage.OutputTokens
						usage.CacheRead += ev.Usage.CacheReadTokens
						usage.CacheWrite += ev.Usage.CacheWriteTokens
					}
					roundStop = ev.StopReason
					if roundStop == "" {
						roundStop = llm.StopEnd
					}
					break drain
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

		if roundStop != llm.StopToolUse {
			// end_turn / max_tokens / filtered → finalize the turn.
			flushAllPendingAt(byteToUTF16(accumulatedText.Len()))
			if merged := mergeUsage(); merged != nil {
				out <- Event{Kind: EvUsage, Usage: merged}
			}
			persistAndDone(string(roundStop), "", isFirstAssistantTurn, in.Content)
			return
		}

		// Tool round — collect calls in invocation order, dispatch each.
		finalCalls := make([]llm.ToolCall, 0, len(toolOrder))
		for _, id := range toolOrder {
			finalCalls = append(finalCalls, *toolBuf[id])
		}

		// Append the assistant turn (text emitted this round + tool_use
		// blocks) to rolledMessages so the next round sees the model's
		// own reasoning. Anthropic and OpenAI both require the assistant
		// turn carrying the tool_use to be present before the tool_result
		// turn lands.
		rolledMessages = append(rolledMessages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   roundText.String(),
			ToolCalls: finalCalls,
		})

		// Dispatch each call. Visibility filter applies because the
		// dispatcher was constructed with in.UserID baked in.
		for _, call := range finalCalls {
			out <- Event{Kind: EvToolStart, ToolName: call.Name, ToolArgs: call.ArgsJSON}
			outcome := dispatcher.Dispatch(ctx, call)
			rolledMessages = append(rolledMessages, llm.Message{
				Role:       llm.RoleTool,
				Content:    outcome.ResultText,
				ToolCallID: call.ID,
			})
			documents = appendUniqueDocs(documents, outcome.Docs)
			evidence = appendUniqueChunks(evidence, outcome.Chunks)
			toolCallsCollected = append(toolCallsCollected, model.ChatToolCall{
				Name:          call.Name,
				ArgsJSON:      call.ArgsJSON,
				ResultID:      call.ID,
				ResultSummary: outcome.Summary,
				// Denormalise per-call chunks so the FE persisted
				// ToolTrace can render its expanded body after page
				// reload (the union on chat_messages.evidence is
				// deduped + can't be split back per-call).
				Chunks: outcome.Chunks,
			})
			out <- Event{Kind: EvToolResult, ToolName: call.Name, ToolSummary: outcome.Summary, ToolChunks: outcome.Chunks}
		}
		// Loop continues — toolRound++ then another generate call with
		// the extended messages + documents. On the round AFTER reaching
		// maxToolRounds, roundTools=nil forces the model to finish.
	}
}

// userHistoryOnly returns msgs filtered to user turns only. Used by
// the rewriter call so cheap models can't be tempted to compute over
// or summarise prior assistant content. See the rewriter call site
// in runTurn for the failure mode this prevents.
func userHistoryOnly(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			out = append(out, m)
		}
	}
	return out
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
			MimeType: h.MimeType,
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
