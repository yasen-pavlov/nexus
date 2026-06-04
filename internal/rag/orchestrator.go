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

// Getter fetches a cached binary by its (sourceType, sourceName,
// sourceID) triple. Satisfied by *storage.BinaryStore. The orchestrator
// uses it for cache-ONLY reads when attaching images — a miss returns an
// error and the image is silently skipped (never a synchronous refetch,
// per the multi-modal cost guardrails in the master plan §8).
type Getter interface {
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
	Binaries    Getter
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
	binaries    Getter
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

// turnState holds the mutable per-turn accumulators the orchestrator
// builds up while streaming one assistant message. Promoting the former
// runTurn closures to methods on this struct keeps their behaviour
// identical (same captured fields) while letting runTurn read as a
// sequence of phases. The orchestrator pointer + per-turn inputs needed by
// finalisation (auto-title, persist) are carried alongside the
// accumulators.
type turnState struct {
	o       *Orchestrator
	in      RunInput
	chat    *model.Chat
	out     chan<- Event
	modelID string

	turnStart       time.Time
	rewriterModelID string

	finalOnce       sync.Once
	assistantID     uuid.UUID
	accumulatedText strings.Builder
	citations       []model.ChatCitation
	evidence        []ChunkPreview
	// usage is the running LLM-call token total. The Phase-5 round loop
	// accumulates tokens from every Generate call (initial + each tool
	// round) into this single counter. auxUsage on top accumulates
	// rewriter + title costs so the persisted total reflects the full
	// turn cost (plan §"Token cost accounting").
	usage    *model.ChatUsage
	auxUsage model.ChatUsage
	// rewrittenQuery carries the rewriter's output so the persisted
	// message can surface it for post-hoc inspection (FE phase strip on
	// reload, eval harness, debug).
	rewrittenQuery   string
	skippedRetrieval bool
	// pendingCitations buffers Anthropic citations until the next
	// sentence terminator so pills land at clean sentence boundaries
	// instead of mid-word.
	pendingCitations []model.ChatCitation
	// toolCallsCollected accumulates one record per tool call dispatched
	// across all rounds in this turn. Persisted as the assistant
	// message's chat_messages.tool_calls JSONB so the FE can render the
	// collapsible tool-trace rows after page reload.
	toolCallsCollected []model.ChatToolCall

	persistedDurationMs int
}

// byteToUTF16 converts a byte offset into accumulatedText to a UTF-16 code
// unit offset (the unit the FE uses to anchor citation pills).
func (t *turnState) byteToUTF16(byteOffset int) int {
	s := t.accumulatedText.String()
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

// flushAllPendingAt anchors every still-buffered citation at a single
// offset and emits them. Used on the error/finalise paths where no further
// sentence boundary will arrive.
func (t *turnState) flushAllPendingAt(anchor int) {
	if len(t.pendingCitations) == 0 {
		return
	}
	for i := range t.pendingCitations {
		t.pendingCitations[i].SpanStart = anchor
		t.pendingCitations[i].SpanEnd = anchor
		t.citations = append(t.citations, t.pendingCitations[i])
		c := t.pendingCitations[i]
		t.out <- Event{Kind: EvCitation, Citation: &c}
	}
	t.pendingCitations = t.pendingCitations[:0]
}

// distributePendingAtBoundaries drains buffered citations onto the
// sentence boundaries found in text[fromByte:], one per boundary, so pills
// land at clean sentence ends instead of mid-word.
func (t *turnState) distributePendingAtBoundaries(text string, fromByte int) {
	cursor := fromByte
	for len(t.pendingCitations) > 0 {
		b := nextSentenceBoundary(text, cursor)
		if b == 0 {
			return
		}
		runeAnchor := t.byteToUTF16(b)
		head := t.pendingCitations[0]
		head.SpanStart = runeAnchor
		head.SpanEnd = runeAnchor
		t.citations = append(t.citations, head)
		t.out <- Event{Kind: EvCitation, Citation: &head}
		t.pendingCitations = t.pendingCitations[1:]
		cursor = b
	}
}

// addUsage merges a per-call llm.Usage into the running auxUsage
// accumulator. Tolerates nil. Used for both rewriter and title calls so a
// single Generate boundary doesn't lose tokens.
func (t *turnState) addUsage(u *llm.Usage) {
	if u == nil {
		return
	}
	t.auxUsage.Input += u.InputTokens
	t.auxUsage.Output += u.OutputTokens
	t.auxUsage.CacheRead += u.CacheReadTokens
	t.auxUsage.CacheWrite += u.CacheWriteTokens
}

// mergeUsage produces the final ChatUsage for persistence by adding
// auxUsage on top of the streamed-LLM usage. Returns nil when both are zero
// (no LLM calls were billed).
func (t *turnState) mergeUsage() *model.ChatUsage {
	if t.usage == nil && t.auxUsage == (model.ChatUsage{}) {
		return nil
	}
	merged := model.ChatUsage{}
	if t.usage != nil {
		merged = *t.usage
	}
	merged.Input += t.auxUsage.Input
	merged.Output += t.auxUsage.Output
	merged.CacheRead += t.auxUsage.CacheRead
	merged.CacheWrite += t.auxUsage.CacheWrite
	return &merged
}

// addStreamUsage folds a streamed Generate-call usage into the running
// LLM-call total (lazily allocating it on first use).
func (t *turnState) addStreamUsage(u *llm.Usage) {
	if u == nil {
		return
	}
	if t.usage == nil {
		t.usage = &model.ChatUsage{}
	}
	t.usage.Input += u.InputTokens
	t.usage.Output += u.OutputTokens
	t.usage.CacheRead += u.CacheReadTokens
	t.usage.CacheWrite += u.CacheWriteTokens
}

// persistAssistant writes the assistant message row. Captures the
// wall-clock duration from the runTurn start so the FE can render a stable
// label that agrees across the live view and the post-refresh persisted
// view.
func (t *turnState) persistAssistant(stop string) {
	persistCtx := context.Background()
	duration := int(time.Since(t.turnStart) / time.Millisecond)
	t.persistedDurationMs = duration
	msg := &model.ChatMessage{
		ID:               uuid.New(),
		ChatID:           t.in.ChatID,
		Role:             model.ChatRoleAssistant,
		Content:          t.accumulatedText.String(),
		Model:            t.modelID,
		Citations:        t.citations,
		Evidence:         t.evidence,
		ToolCalls:        t.toolCallsCollected,
		Usage:            t.mergeUsage(),
		StopReason:       stop,
		RewrittenQuery:   t.rewrittenQuery,
		SkippedRetrieval: t.skippedRetrieval,
		DurationMs:       &duration,
	}
	if err := t.o.chats.AppendMessage(persistCtx, msg); err != nil {
		t.o.log.Error("rag: persist assistant message", zap.Error(err))
	}
	t.assistantID = msg.ID
}

// maybeAutoTitle writes a best-effort chat title on the first successful
// end_turn assistant message when a rewriter model is configured (the
// title and rewriter share one cheap-model setting). context.Background()
// is intentional: title is supportive infrastructure that should land even
// if the SSE parent dies during the short title call.
func (t *turnState) maybeAutoTitle(stop string, isFirstAssistantTurn bool, userQuestion string) {
	if t.rewriterModelID == "" ||
		stop != string(llm.StopEnd) ||
		!isFirstAssistantTurn ||
		strings.TrimSpace(t.chat.Title) != "" ||
		strings.TrimSpace(t.accumulatedText.String()) == "" {
		return
	}

	registry := t.o.registry()
	titleGen, titleInfo, err := registry.Get(t.rewriterModelID)
	if err != nil {
		t.o.log.Info("rag: skipping auto-title; rewriter model not resolvable", zap.Error(err))
		t.out <- Event{Kind: EvTitleStatus, StatusReason: titleFailureError}
		return
	}
	title, tu, titleReason := summarizeForTitle(context.Background(), titleGen, titleInfo, userQuestion, t.accumulatedText.String(), t.o.log)
	t.addUsage(tu)
	switch {
	case title != "":
		if err := t.o.chats.UpdateChat(context.Background(), t.in.ChatID, store.ChatUpdate{Title: &title}); err != nil {
			t.o.log.Warn("rag: persist auto-title", zap.Error(err))
			t.out <- Event{Kind: EvTitleStatus, StatusReason: titleFailureError}
		} else {
			t.out <- Event{Kind: EvTitle, Title: title}
		}
	case titleReason != "":
		// Title attempt failed (timeout / empty / error) — surface to
		// the FE diagnostic glyph so admins know titles aren't silently
		// disabled.
		t.out <- Event{Kind: EvTitleStatus, StatusReason: titleReason}
	}
}

// persistAndDone runs persist + auto-title + emit-done as a single
// idempotent finalisation. Idempotent via finalOnce so the title + done
// emission can't double-write on the cancellation/error path.
func (t *turnState) persistAndDone(stop, errMsg string, isFirstAssistantTurn bool, userQuestion string) {
	t.finalOnce.Do(func() {
		// Surface the underlying failure message at WARN — every
		// error-stop path funnels through here, so a single log
		// statement covers "generate failed", "stream closed
		// unexpectedly", "retrieval failed", and EventError payloads.
		// Without this the SSE error frame is the only visible
		// diagnostic and gets lost as soon as the FE closes the stream.
		if stop == "error" && errMsg != "" {
			t.o.log.Warn("rag: turn finalised with error",
				zap.String("err", errMsg),
				zap.String("model", t.modelID),
				zap.String("chat_id", t.in.ChatID.String()))
		}
		t.persistAssistant(stop)
		t.maybeAutoTitle(stop, isFirstAssistantTurn, userQuestion)

		if errMsg != "" {
			t.out <- Event{Kind: EvError, Err: errMsg}
		}
		t.out <- Event{Kind: EvDone, StopReason: stop, MessageID: t.assistantID, DurationMs: t.persistedDurationMs}
	})
}

// runTurn owns the output channel and is responsible for closing it
// after exactly one EvDone.
func (o *Orchestrator) runTurn(ctx context.Context, in RunInput, chat *model.Chat, modelID string, info llm.ModelInfo, gen llm.Generator, out chan<- Event) {
	defer close(out)

	settings := o.settings()
	st := &turnState{
		o:       o,
		in:      in,
		chat:    chat,
		out:     out,
		modelID: modelID,
		// Server-measured turn start. Captured here (after user persist,
		// before retrieval/generation) so the persisted duration_ms is
		// the orchestrator's wall-clock and stays stable across page
		// refreshes — the FE's live "completedAt − startedAt" timer
		// captures network + SSE-flush latency too and would drift after
		// refresh.
		turnStart:       time.Now(),
		rewriterModelID: strings.TrimSpace(settings.RewriterModel),
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
		st.persistAndDone("cancelled", "", isFirstAssistantTurn, in.Content)
		return
	}

	// --- Rewriter step (Phase 4) ---
	searchQuery, needsRetrieval := st.runRewriter(ctx, history, priorAssistantTurns)

	// Cancellation check after rewriter, before retrieval.
	if err := ctx.Err(); err != nil {
		st.persistAndDone("cancelled", "", isFirstAssistantTurn, in.Content)
		return
	}

	// --- Retrieval (only when the rewriter said yes) ---
	docs, ok := st.runRetrieval(ctx, searchQuery, needsRetrieval, info, settings, isFirstAssistantTurn)
	if !ok {
		return
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
	loop := &roundLoop{
		st:                 st,
		gen:                gen,
		info:               info,
		settings:           settings,
		bareModel:          bareModel,
		dispatcher:         dispatcher,
		useNativeCitations: info.SupportsCitations,
		isFirst:            isFirstAssistantTurn,
		documents:          documents,
		rolledMessages:     rolledMessages,
	}
	loop.run(ctx)
}

// runRewriter executes the Phase-4 turn-aware query rewriter. Returns the
// search query (the rewritten form when the rewriter produced one, else the
// raw user content) and whether retrieval is still needed. A no-op (returns
// the raw content + true) when no rewriter model is configured or this is
// the first assistant turn. Mutates the turn's rewrittenQuery + usage.
func (t *turnState) runRewriter(ctx context.Context, history []llm.Message, priorAssistantTurns int) (string, bool) {
	searchQuery := t.in.Content
	needsRetrieval := true
	if t.rewriterModelID == "" || priorAssistantTurns == 0 {
		return searchQuery, needsRetrieval
	}

	registry := t.o.registry()
	rewriterGen, rewriterInfo, err := registry.Get(t.rewriterModelID)
	if err != nil {
		t.o.log.Warn("rag: rewriter model not resolvable; skipping rewrite", zap.Error(err))
		t.out <- Event{Kind: EvRewriterStatus, StatusReason: rewriterFailureError}
		return searchQuery, needsRetrieval
	}

	// Strip prior ASSISTANT turns from the rewriter history. Cheap models
	// (Haiku-class) take prior assistant content as data to compute over
	// and answer the question themselves instead of rewriting it (one
	// Wolt-orders turn produced a literal "Total: €222.65" reply,
	// breaking the parser). User-only history still gives the rewriter
	// enough topic continuity for the common coreference cases ("how much
	// in total" → "how much did I pay in total for the wolt orders"); the
	// cases that genuinely need an assistant-introduced referent ("the
	// second invoice") fall through to the tool loop.
	res := rewriteQuery(ctx, rewriterGen, rewriterInfo, userHistoryOnly(history), t.in.Content, t.o.log)
	t.addUsage(res.Usage)
	if strings.TrimSpace(res.Rewritten) != "" {
		searchQuery = res.Rewritten
		t.rewrittenQuery = res.Rewritten
	}
	needsRetrieval = res.NeedsRetrieval
	if res.FailureReason != "" {
		// Surface the rewriter fallback to the FE so the phase chip can
		// render a quiet diagnostic glyph instead of silently looking
		// like "no rewrite was needed".
		t.out <- Event{Kind: EvRewriterStatus, StatusReason: res.FailureReason}
	}
	return searchQuery, needsRetrieval
}

// runRetrieval performs the initial retrieval (when needsRetrieval) and
// emits the evidence/skip event. Returns the initial llm.Documents and
// ok=false when the turn was already finalised on a retrieval error (the
// caller must return). Mutates the turn's evidence + skippedRetrieval.
func (t *turnState) runRetrieval(ctx context.Context, searchQuery string, needsRetrieval bool, info llm.ModelInfo, settings Settings, isFirstAssistantTurn bool) ([]llm.Document, bool) {
	if !needsRetrieval {
		t.skippedRetrieval = true
		t.out <- Event{Kind: EvSkippedRetrieval, Query: searchQuery}
		return nil, true
	}

	t.out <- Event{Kind: EvRetrieving, Query: searchQuery}
	searchReq := model.SearchRequest{
		Query:   searchQuery,
		Limit:   t.o.cfg.MaxEvidenceChunks,
		OwnerID: t.in.UserID.String(),
	}
	result, err := t.o.search.Run(ctx, searchReq)
	if err != nil {
		t.o.log.Warn("rag: retrieval failed", zap.Error(err))
		t.persistAndDone("error", "retrieval failed: "+err.Error(), isFirstAssistantTurn, t.in.Content)
		return nil, false
	}
	docs := buildLLMDocs(result.Documents, t.o.cfg.MaxEvidenceChunks)
	// Multi-modal: attach cached images (vision models) and PDFs
	// (native-PDF models) to the docs when the admin hasn't disabled it.
	// Best-effort and cache-only — never blocks or fails the turn (master
	// plan §8).
	t.o.attachMedia(ctx, docs, result.Documents, info, settings)
	previews := buildPreviews(result.Documents, t.o.cfg.MaxEvidenceChunks)
	t.evidence = previews
	t.out <- Event{Kind: EvEvidence, Evidence: previews}
	return docs, true
}

// roundLoop drives the Phase-5 agentic tool-use loop: generate → drain →
// either finalise (stop != tool_use) or dispatch tool calls and loop. It
// carries the cumulative documents + rolled message history that grow each
// round; turnState carries the streamed answer/citation/usage accumulators.
type roundLoop struct {
	st                 *turnState
	gen                llm.Generator
	info               llm.ModelInfo
	settings           Settings
	bareModel          string
	dispatcher         *searchToolDispatcher
	useNativeCitations bool
	isFirst            bool

	documents      []llm.Document
	rolledMessages []llm.Message
}

// roundResult is the outcome of draining one Generate stream.
type roundResult struct {
	stop      llm.StopReason
	roundText string
	calls     []llm.ToolCall
	// done is set when the drain already finalised the turn (cancel /
	// stream-closed / EventError); the loop must return without further
	// work.
	done bool
}

// run executes the round loop until a non-tool stop finalises the turn.
func (l *roundLoop) run(ctx context.Context) {
	st := l.st
	for toolRound := 0; ; toolRound++ {
		// Cancellation between rounds — Phase 4's pattern of checking ctx
		// before each expensive stage applies to each new generate call.
		if err := ctx.Err(); err != nil {
			st.persistAndDone("cancelled", "", l.isFirst, st.in.Content)
			return
		}

		events, ok := l.startRound(ctx, toolRound)
		if !ok {
			return
		}

		res := l.drain(ctx, toolRound, events)
		if res.done {
			return
		}

		if res.stop != llm.StopToolUse {
			// end_turn / max_tokens / filtered → finalize the turn.
			st.flushAllPendingAt(st.byteToUTF16(st.accumulatedText.Len()))
			if merged := st.mergeUsage(); merged != nil {
				st.out <- Event{Kind: EvUsage, Usage: merged}
			}
			st.persistAndDone(string(res.stop), "", l.isFirst, st.in.Content)
			return
		}

		l.dispatchRound(ctx, res)
		// Loop continues — toolRound++ then another generate call with
		// the extended messages + documents. On the round AFTER reaching
		// maxToolRounds, roundTools=nil forces the model to finish.
	}
}

// startRound builds the per-round tools + parser-free request and starts
// the Generate stream. Returns ok=false (after finalising) when Generate
// errors synchronously.
func (l *roundLoop) startRound(ctx context.Context, toolRound int) (<-chan llm.Event, bool) {
	st := l.st
	// Tools for this round. nil after the cap so the model is forced to
	// answer from current context — no separate "force-finish" branch
	// needed.
	var roundTools []llm.Tool
	if toolRound < l.settings.MaxToolRounds {
		roundTools = BuildToolList(l.info, l.settings.MaxToolRounds, l.settings.EnableOpenAttachment)
	}

	llmReq := llm.GenerateRequest{
		Model:       l.bareModel,
		System:      st.o.cfg.SystemPrompt,
		Documents:   l.documents,
		Messages:    l.rolledMessages,
		Tools:       roundTools,
		MaxTokens:   st.o.cfg.MaxTokens,
		EnableCache: true,
	}

	events, err := l.gen.Generate(ctx, llmReq)
	if err != nil {
		st.persistAndDone("error", "generate failed: "+err.Error(), l.isFirst, st.in.Content)
		return nil, false
	}
	return events, true
}

// drain consumes one Generate stream, streaming text + citations to the
// caller and collecting tool calls in invocation order. Returns a
// roundResult; res.done is set when the drain itself finalised the turn
// (cancel / stream-closed / EventError).
func (l *roundLoop) drain(ctx context.Context, toolRound int, events <-chan llm.Event) roundResult {
	st := l.st
	// Citation parser is per-round for non-Anthropic providers so [N]
	// markers map against the cumulative documents slice (which may have
	// grown since the previous round via tool results).
	var parser *CitationParser
	if !l.useNativeCitations {
		parser = NewCitationParser(parserDocsFromLLM(l.documents))
	}

	// Per-round accumulators. roundText feeds the assistant message we
	// append to rolledMessages so the next round sees the model's
	// reasoning (and any text it emitted alongside its tool_use blocks).
	// toolBuf accumulates ToolCallDelta.ArgsJSON across deltas keyed by
	// call ID; toolOrder preserves invocation order so we dispatch in the
	// order the model produced them.
	var (
		roundText strings.Builder
		toolBuf   = map[string]*llm.ToolCall{}
		toolOrder []string
	)

	for {
		select {
		case <-ctx.Done():
			st.persistAndDone("cancelled", "", l.isFirst, st.in.Content)
			return roundResult{done: true}
		case ev, ok := <-events:
			if !ok {
				st.persistAndDone("error", "stream closed unexpectedly", l.isFirst, st.in.Content)
				return roundResult{done: true}
			}
			if res, terminal := l.handleEvent(ev, toolRound, parser, &roundText, toolBuf, &toolOrder); terminal {
				return res
			}
		}
	}
}

// handleEvent processes one streamed LLM event. terminal=true means the
// drain is over (EventDone or EventError) and res is the round outcome;
// otherwise the event was a streaming delta and the loop continues.
func (l *roundLoop) handleEvent(ev llm.Event, toolRound int, parser *CitationParser, roundText *strings.Builder, toolBuf map[string]*llm.ToolCall, toolOrder *[]string) (roundResult, bool) {
	st := l.st
	switch ev.Kind {
	case llm.EventText:
		l.handleText(ev, parser, roundText)
	case llm.EventCitation:
		if ev.Citation != nil {
			st.pendingCitations = append(st.pendingCitations, model.ChatCitation{
				DocID:     ev.Citation.DocID,
				CitedText: ev.Citation.CitedText,
			})
		}
	case llm.EventToolCall:
		l.collectToolCall(ev, toolRound, toolBuf, toolOrder)
	case llm.EventDone:
		return l.handleDone(ev, parser, roundText, toolBuf, *toolOrder), true
	case llm.EventError:
		return l.handleStreamError(ev), true
	}
	return roundResult{}, false
}

// handleDone flushes any buffered parser tail, folds in the call usage, and
// assembles the round result from the stop reason + collected tool calls.
func (l *roundLoop) handleDone(ev llm.Event, parser *CitationParser, roundText *strings.Builder, toolBuf map[string]*llm.ToolCall, toolOrder []string) roundResult {
	st := l.st
	if !l.useNativeCitations {
		if tail := parser.Flush(); tail != "" {
			st.accumulatedText.WriteString(tail)
			roundText.WriteString(tail)
			st.out <- Event{Kind: EvText, TextDelta: tail}
		}
	}
	st.addStreamUsage(ev.Usage)
	roundStop := ev.StopReason
	if roundStop == "" {
		roundStop = llm.StopEnd
	}
	return l.finishDrain(roundStop, roundText.String(), toolBuf, toolOrder)
}

// handleStreamError anchors any buffered citations and finalises the turn
// with the error stop reason; the returned result signals done.
func (l *roundLoop) handleStreamError(ev llm.Event) roundResult {
	st := l.st
	st.flushAllPendingAt(st.byteToUTF16(st.accumulatedText.Len()))
	msg := "generation failed"
	if ev.Err != nil {
		msg = ev.Err.Error()
	}
	st.persistAndDone("error", msg, l.isFirst, st.in.Content)
	return roundResult{done: true}
}

// handleText streams one text delta: native-citation providers append
// verbatim and distribute pending citations at sentence boundaries;
// non-native providers run the [N] marker parser first.
func (l *roundLoop) handleText(ev llm.Event, parser *CitationParser, roundText *strings.Builder) {
	st := l.st
	if l.useNativeCitations {
		prevLen := st.accumulatedText.Len()
		st.accumulatedText.WriteString(ev.TextDelta)
		roundText.WriteString(ev.TextDelta)
		st.out <- Event{Kind: EvText, TextDelta: ev.TextDelta}
		st.distributePendingAtBoundaries(st.accumulatedText.String(), prevLen)
		return
	}
	clean, cites := parser.Feed(ev.TextDelta)
	if clean != "" {
		st.accumulatedText.WriteString(clean)
		roundText.WriteString(clean)
		st.out <- Event{Kind: EvText, TextDelta: clean}
	}
	for i := range cites {
		st.citations = append(st.citations, cites[i])
		c := cites[i]
		st.out <- Event{Kind: EvCitation, Citation: &c}
	}
}

// collectToolCall folds one streamed tool-call delta into toolBuf keyed by
// (synthesised) call id, preserving first-seen invocation order.
func (l *roundLoop) collectToolCall(ev llm.Event, toolRound int, toolBuf map[string]*llm.ToolCall, toolOrder *[]string) {
	if ev.ToolCall == nil {
		return
	}
	id := ev.ToolCall.ID
	if id == "" {
		// Ollama emits Final=true tool calls without an SDK-supplied ID.
		// Synthesize one stable per (round, order) so the matching
		// tool_result message round-trips ToolCallID correctly.
		id = fmt.Sprintf("ollama-r%d-%d", toolRound, len(*toolOrder))
	}
	tc, exists := toolBuf[id]
	if !exists {
		tc = &llm.ToolCall{ID: id, Name: ev.ToolCall.Name}
		toolBuf[id] = tc
		*toolOrder = append(*toolOrder, id)
	}
	if tc.Name == "" && ev.ToolCall.Name != "" {
		tc.Name = ev.ToolCall.Name
	}
	if ev.ToolCall.ArgsJSON != "" {
		tc.ArgsJSON += ev.ToolCall.ArgsJSON
	}
}

// finishDrain assembles the final tool-call list (in invocation order) and
// returns the round's result.
func (l *roundLoop) finishDrain(stop llm.StopReason, roundText string, toolBuf map[string]*llm.ToolCall, toolOrder []string) roundResult {
	calls := make([]llm.ToolCall, 0, len(toolOrder))
	for _, id := range toolOrder {
		calls = append(calls, *toolBuf[id])
	}
	return roundResult{stop: stop, roundText: roundText, calls: calls}
}

// dispatchRound appends the assistant tool_use turn, dispatches each tool
// call in order, appends the tool_result turns, and grows the cumulative
// documents/evidence/tool-trace accumulators. Document ordering is stable:
// appendUniqueDocs only ever appends new doc ids at the tail.
func (l *roundLoop) dispatchRound(ctx context.Context, res roundResult) {
	st := l.st
	// Append the assistant turn (text emitted this round + tool_use
	// blocks) to rolledMessages so the next round sees the model's own
	// reasoning. Anthropic and OpenAI both require the assistant turn
	// carrying the tool_use to be present before the tool_result turn
	// lands.
	l.rolledMessages = append(l.rolledMessages, llm.Message{
		Role:      llm.RoleAssistant,
		Content:   res.roundText,
		ToolCalls: res.calls,
	})

	// Dispatch each call. Visibility filter applies because the dispatcher
	// was constructed with in.UserID baked in.
	for _, call := range res.calls {
		st.out <- Event{Kind: EvToolStart, ToolName: call.Name, ToolArgs: call.ArgsJSON}
		outcome := l.dispatcher.Dispatch(ctx, call)
		l.rolledMessages = append(l.rolledMessages, llm.Message{
			Role:       llm.RoleTool,
			Content:    outcome.ResultText,
			ToolCallID: call.ID,
		})
		l.documents = appendUniqueDocs(l.documents, outcome.Docs)
		st.evidence = appendUniqueChunks(st.evidence, outcome.Chunks)
		st.toolCallsCollected = append(st.toolCallsCollected, model.ChatToolCall{
			Name:          call.Name,
			ArgsJSON:      call.ArgsJSON,
			ResultID:      call.ID,
			ResultSummary: outcome.Summary,
			// Denormalise per-call chunks so the FE persisted ToolTrace
			// can render its expanded body after page reload (the union
			// on chat_messages.evidence is deduped + can't be split back
			// per-call).
			Chunks: outcome.Chunks,
		})
		st.out <- Event{Kind: EvToolResult, ToolName: call.Name, ToolSummary: outcome.Summary, ToolChunks: outcome.Chunks}
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
		switch classifyBoundary(text, i) {
		case boundaryHere:
			return i + 1
		case boundaryWait:
			return 0
		}
		// boundaryNone: keep scanning.
	}
	return 0
}

// boundaryDecision classifies position i as a confirmed sentence boundary,
// an incomplete one (terminator is the last byte seen so far — wait for the
// next streamed delta), or not a boundary at all.
type boundaryDecision int

const (
	boundaryNone boundaryDecision = iota
	boundaryHere
	boundaryWait
)

// classifyBoundary applies the streaming-markdown sentence-end heuristics to
// the byte at text[i]. See nextSentenceBoundary's doc comment for the rules.
func classifyBoundary(text string, i int) boundaryDecision {
	switch text[i] {
	case '\n':
		return boundaryHere
	case '!', '?':
		if i+1 >= len(text) {
			return boundaryWait
		}
		if isBoundaryWhitespace(text[i+1]) {
			return boundaryHere
		}
		return boundaryNone
	case '.':
		if i+1 >= len(text) {
			return boundaryWait
		}
		if !isBoundaryWhitespace(text[i+1]) {
			return boundaryNone
		}
		if i > 0 && text[i-1] >= '0' && text[i-1] <= '9' {
			return boundaryNone
		}
		return boundaryHere
	}
	return boundaryNone
}

// isBoundaryWhitespace reports whether b is the whitespace that confirms a
// sentence terminator (space, tab, or newline).
func isBoundaryWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n'
}
