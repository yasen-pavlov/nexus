package rag

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// --- fakes ---

type fakeChats struct {
	mu       sync.Mutex
	chats    map[uuid.UUID]*model.Chat
	messages map[uuid.UUID][]model.ChatMessage
}

func newFakeChats() *fakeChats {
	return &fakeChats{
		chats:    map[uuid.UUID]*model.Chat{},
		messages: map[uuid.UUID][]model.ChatMessage{},
	}
}

func (f *fakeChats) seed(c *model.Chat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chats[c.ID] = c
}

func (f *fakeChats) GetChat(_ context.Context, id uuid.UUID) (*model.Chat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.chats[id]
	if !ok {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}

func (f *fakeChats) ListMessages(_ context.Context, chatID uuid.UUID) ([]model.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.ChatMessage, len(f.messages[chatID]))
	copy(out, f.messages[chatID])
	return out, nil
}

func (f *fakeChats) AppendMessage(_ context.Context, msg *model.ChatMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.chats[msg.ChatID]; !ok {
		return errors.New("chat not found")
	}
	if msg.ID == uuid.Nil {
		msg.ID = uuid.New()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	msg.Seq = len(f.messages[msg.ChatID]) + 1
	f.messages[msg.ChatID] = append(f.messages[msg.ChatID], *msg)
	return nil
}

type fakeSearch struct {
	docs []model.DocumentHit
	err  error
}

func (f *fakeSearch) Run(_ context.Context, _ model.SearchRequest) (*model.SearchResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &model.SearchResult{Documents: f.docs, TotalCount: len(f.docs)}, nil
}

type fakeGenerator struct {
	events []llm.Event
	delay  time.Duration // optional per-event delay to exercise cancellation
	err    error
}

func (f *fakeGenerator) Generate(ctx context.Context, _ llm.GenerateRequest) (<-chan llm.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(chan llm.Event, len(f.events))
	go func() {
		defer close(out)
		for _, ev := range f.events {
			if f.delay > 0 {
				select {
				case <-ctx.Done():
					out <- llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
					return
				case <-time.After(f.delay):
				}
			}
			select {
			case <-ctx.Done():
				out <- llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
				return
			case out <- ev:
			}
		}
	}()
	return out, nil
}

type fakeRegistry struct {
	gen  llm.Generator
	info llm.ModelInfo
	err  error
}

func (f *fakeRegistry) Get(_ string) (llm.Generator, llm.ModelInfo, error) {
	return f.gen, f.info, f.err
}

func (f *fakeRegistry) Models() []llm.ModelInfo {
	if f.err != nil {
		return nil
	}
	return []llm.ModelInfo{f.info}
}

// --- helpers ---

func newOrchTest(t *testing.T, gen *fakeGenerator, info llm.ModelInfo, search SearchProvider) (*Orchestrator, *fakeChats) {
	t.Helper()
	chats := newFakeChats()
	reg := &fakeRegistry{gen: gen, info: info}
	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry { return reg },
		Search:   search,
		Chats:    chats,
		Cfg:      DefaultConfig(),
		Log:      zap.NewNop(),
	})
	return o, chats
}

func collectEvents(t *testing.T, ch <-chan Event, timeout time.Duration) []Event {
	t.Helper()
	var events []Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("timed out collecting events; got %d so far", len(events))
		}
	}
}

func makeChat(t *testing.T, chats *fakeChats, defaultModel string) *model.Chat {
	t.Helper()
	c := &model.Chat{
		ID:           uuid.New(),
		UserID:       uuid.New(),
		DefaultModel: defaultModel,
	}
	chats.seed(c)
	return c
}

// --- tests ---

func TestRun_HappyPath_AnthropicNativeCitations(t *testing.T) {
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "hello"},
			{Kind: llm.EventCitation, Citation: &llm.Citation{DocID: "doc-a", CitedText: "src", SpanStart: 0, SpanEnd: 5}},
			{Kind: llm.EventText, TextDelta: " world"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd, Usage: &llm.Usage{InputTokens: 100, OutputTokens: 30, CacheReadTokens: 5, CacheWriteTokens: 80}},
		},
	}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", Provider: "anthropic", SupportsCitations: true}
	search := &fakeSearch{docs: []model.DocumentHit{
		{Document: model.Document{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Title: "Doc A", SourceType: "filesystem", Content: "alpha"}, Headline: "alpha"},
	}}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	ch, err := o.Run(context.Background(), RunInput{
		ChatID:  chat.ID,
		UserID:  chat.UserID,
		Content: "ping",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch, 2*time.Second)

	// retrieving + evidence + 2x text + 1x citation + usage + done = 7
	if len(events) < 6 {
		t.Fatalf("got %d events: %+v", len(events), events)
	}
	if events[0].Kind != EvRetrieving || events[0].Query != "ping" {
		t.Errorf("first=%+v want EvRetrieving/ping", events[0])
	}
	if events[1].Kind != EvEvidence || len(events[1].Evidence) != 1 {
		t.Errorf("second=%+v want EvEvidence", events[1])
	}
	last := events[len(events)-1]
	if last.Kind != EvDone || last.StopReason != "end_turn" {
		t.Errorf("last=%+v want EvDone/end_turn", last)
	}

	// Verify the assistant message was persisted
	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages", len(msgs))
	}
	asst := msgs[1]
	if asst.Role != model.ChatRoleAssistant {
		t.Errorf("role=%q", asst.Role)
	}
	if asst.Content != "hello world" {
		t.Errorf("content=%q", asst.Content)
	}
	if len(asst.Citations) != 1 || asst.Citations[0].DocID != "doc-a" {
		t.Errorf("citations=%+v", asst.Citations)
	}
	if asst.StopReason != "end_turn" {
		t.Errorf("stop=%q", asst.StopReason)
	}
	if asst.Usage == nil || asst.Usage.Input != 100 {
		t.Errorf("usage=%+v", asst.Usage)
	}
	if asst.Model != "anthropic:claude-sonnet-4-6" {
		t.Errorf("model=%q", asst.Model)
	}
}

func TestRun_NonAnthropic_ParsesBracketCitations(t *testing.T) {
	docID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "see "},
			{Kind: llm.EventText, TextDelta: "[1] for details"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}
	info := llm.ModelInfo{ID: "openai:gpt-5-mini", Provider: "openai", SupportsCitations: false}
	search := &fakeSearch{docs: []model.DocumentHit{
		{Document: model.Document{ID: docID, Title: "Doc", SourceType: "email", Content: "body"}},
	}}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "openai:gpt-5-mini")

	ch, err := o.Run(context.Background(), RunInput{
		ChatID:  chat.ID,
		UserID:  chat.UserID,
		Content: "q",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch, 2*time.Second)

	var citationCount int
	var textChunks []string
	for _, ev := range events {
		switch ev.Kind {
		case EvCitation:
			citationCount++
			if ev.Citation == nil || ev.Citation.DocID != docID.String() {
				t.Errorf("citation=%+v", ev.Citation)
			}
		case EvText:
			textChunks = append(textChunks, ev.TextDelta)
		}
	}
	if citationCount != 1 {
		t.Errorf("citation count=%d", citationCount)
	}

	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	asst := msgs[1]
	if asst.Content != "see  for details" {
		t.Errorf("content=%q (chunks=%v)", asst.Content, textChunks)
	}
	if len(asst.Citations) != 1 {
		t.Errorf("citations=%+v", asst.Citations)
	}
}

func TestRun_Cancellation_PersistsPartialMessage(t *testing.T) {
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "partial"},
			{Kind: llm.EventText, TextDelta: " never sent"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
		delay: 50 * time.Millisecond,
	}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	search := &fakeSearch{}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := o.Run(ctx, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Read the first text event, then cancel.
	var seenPartial bool
	for ev := range ch {
		if ev.Kind == EvText && ev.TextDelta == "partial" {
			seenPartial = true
			cancel()
		}
		if ev.Kind == EvDone {
			if ev.StopReason != "cancelled" {
				t.Errorf("stop_reason=%q want cancelled", ev.StopReason)
			}
			break
		}
	}
	if !seenPartial {
		t.Fatal("never saw partial text")
	}

	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	if len(msgs) != 2 {
		t.Fatalf("got %d msgs", len(msgs))
	}
	if msgs[1].StopReason != "cancelled" {
		t.Errorf("persisted stop=%q", msgs[1].StopReason)
	}
}

func TestRun_SearchFailure_PersistsErrorMessage(t *testing.T) {
	gen := &fakeGenerator{}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	search := &fakeSearch{err: errors.New("boom")}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	ch, err := o.Run(context.Background(), RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch, 2*time.Second)

	var sawError bool
	for _, ev := range events {
		if ev.Kind == EvError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatalf("expected EvError, events=%+v", events)
	}
	last := events[len(events)-1]
	if last.Kind != EvDone || last.StopReason != "error" {
		t.Errorf("last=%+v", last)
	}

	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	if msgs[1].StopReason != "error" {
		t.Errorf("persisted stop=%q", msgs[1].StopReason)
	}
}

func TestRun_GenerateFailure_PersistsErrorMessage(t *testing.T) {
	gen := &fakeGenerator{err: errors.New("provider down")}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	search := &fakeSearch{}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	ch, err := o.Run(context.Background(), RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch, 2*time.Second)

	last := events[len(events)-1]
	if last.Kind != EvDone || last.StopReason != "error" {
		t.Errorf("last=%+v", last)
	}
}

func TestRun_LLMEventError_StopsAndPersists(t *testing.T) {
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "starting "},
			{Kind: llm.EventError, Err: errors.New("rate limit")},
		},
	}
	info := llm.ModelInfo{ID: "openai:gpt-5-mini", SupportsCitations: false}
	search := &fakeSearch{}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "openai:gpt-5-mini")

	ch, err := o.Run(context.Background(), RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch, 2*time.Second)
	last := events[len(events)-1]
	if last.Kind != EvDone || last.StopReason != "error" {
		t.Errorf("last=%+v", last)
	}
	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	if msgs[1].Content != "starting " {
		t.Errorf("partial content=%q", msgs[1].Content)
	}
}

func TestRun_ChatNotFound(t *testing.T) {
	gen := &fakeGenerator{}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6"}
	search := &fakeSearch{}
	o, _ := newOrchTest(t, gen, info, search)

	_, err := o.Run(context.Background(), RunInput{
		ChatID:  uuid.New(),
		UserID:  uuid.New(),
		Content: "q",
	})
	if !errors.Is(err, ErrChatNotFound) {
		t.Errorf("err=%v want ErrChatNotFound", err)
	}
}

func TestRun_ModelOverrideBeatsChatDefault(t *testing.T) {
	gen := &fakeGenerator{events: []llm.Event{{Kind: llm.EventDone, StopReason: llm.StopEnd}}}
	info := llm.ModelInfo{ID: "anthropic:claude-haiku-4-5", SupportsCitations: true}
	search := &fakeSearch{}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	ch, err := o.Run(context.Background(), RunInput{
		ChatID:  chat.ID,
		UserID:  chat.UserID,
		Content: "q",
		Model:   "anthropic:claude-haiku-4-5",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for range ch {
	}
	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	if msgs[1].Model != "anthropic:claude-haiku-4-5" {
		t.Errorf("persisted model=%q want override", msgs[1].Model)
	}
}

func TestRun_NoModelConfigured(t *testing.T) {
	gen := &fakeGenerator{}
	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry { return &fakeRegistry{gen: gen, err: errors.New("no models")} },
		Search:   &fakeSearch{},
		Chats:    newFakeChats(),
		Cfg:      DefaultConfig(),
		Log:      zap.NewNop(),
	})
	chats := newFakeChats()
	chat := makeChat(t, chats, "")
	o.chats = chats

	_, err := o.Run(context.Background(), RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err == nil {
		t.Fatal("expected error when no models configured")
	}
}

func TestRun_HistoryPackedFromPriorMessages(t *testing.T) {
	gen := &fakeGenerator{events: []llm.Event{{Kind: llm.EventDone, StopReason: llm.StopEnd}}}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	search := &fakeSearch{}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	// Seed two prior turns
	ctx := context.Background()
	if err := chats.AppendMessage(ctx, &model.ChatMessage{ChatID: chat.ID, Role: model.ChatRoleUser, Content: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := chats.AppendMessage(ctx, &model.ChatMessage{ChatID: chat.ID, Role: model.ChatRoleAssistant, Content: "first answer"}); err != nil {
		t.Fatal(err)
	}

	ch, err := o.Run(ctx, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "follow up"})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}

	// Expect 4 messages stored: 2 prior + new user + new assistant
	msgs, _ := chats.ListMessages(ctx, chat.ID)
	if len(msgs) != 4 {
		t.Errorf("got %d msgs", len(msgs))
	}
}

func TestRun_PacksHistoryWithoutCurrentDuplicate(t *testing.T) {
	gen := &fakeGenerator{events: []llm.Event{{Kind: llm.EventDone, StopReason: llm.StopEnd}}}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	search := &fakeSearch{}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	ctx := context.Background()
	ch, err := o.Run(ctx, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}

	// First-turn case: history packing needs to NOT include the current
	// user message twice. Verify by checking message count.
	msgs, _ := chats.ListMessages(ctx, chat.ID)
	if len(msgs) != 2 {
		t.Errorf("got %d msgs", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Errorf("user msg=%q", msgs[0].Content)
	}
}

func TestDefaultConfig_FillsZeros(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxEvidenceChunks != 10 || cfg.HistoryTurns != 3 || cfg.MaxTokens != 4096 || cfg.SystemPrompt == "" {
		t.Errorf("defaults wrong: %+v", cfg)
	}

	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry { return &fakeRegistry{} },
		Search:   &fakeSearch{},
		Chats:    newFakeChats(),
		Cfg:      Config{}, // all zero
		Log:      zap.NewNop(),
	})
	if o.cfg.MaxEvidenceChunks != 10 || o.cfg.HistoryTurns != 3 || o.cfg.MaxTokens != 4096 || o.cfg.SystemPrompt == "" {
		t.Errorf("zero-value config not filled: %+v", o.cfg)
	}
}

func TestBuildLLMDocs_RespectsMaxAndDate(t *testing.T) {
	hits := []model.DocumentHit{
		{Document: model.Document{ID: uuid.New(), Title: "a", SourceType: "filesystem", Content: "x", CreatedAt: time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)}},
		{Document: model.Document{ID: uuid.New(), Title: "b", SourceType: "filesystem", Content: "y"}},
		{Document: model.Document{ID: uuid.New(), Title: "c", SourceType: "filesystem", Content: "z"}},
	}
	docs := buildLLMDocs(hits, 2)
	if len(docs) != 2 {
		t.Fatalf("got %d docs", len(docs))
	}
	if docs[0].Date != "2026-04-25" {
		t.Errorf("date=%q", docs[0].Date)
	}
}

func TestBuildLLMDocs_FallsBackToHeadlineWhenContentEmpty(t *testing.T) {
	hits := []model.DocumentHit{
		{Document: model.Document{ID: uuid.New(), Title: "a", SourceType: "filesystem"}, Headline: "preview"},
	}
	docs := buildLLMDocs(hits, 5)
	if docs[0].Content != "preview" {
		t.Errorf("content=%q", docs[0].Content)
	}
}

func TestBuildPreviews_Caps(t *testing.T) {
	hits := []model.DocumentHit{
		{Document: model.Document{ID: uuid.New(), Title: "a", SourceType: "filesystem", CreatedAt: time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)}, Headline: "h"},
		{Document: model.Document{ID: uuid.New(), Title: "b", SourceType: "filesystem"}, Headline: "h"},
	}
	out := buildPreviews(hits, 1)
	if len(out) != 1 || out[0].Date != "2026-04-25" {
		t.Errorf("previews=%+v", out)
	}
}

func TestRun_StreamClosedWithoutDone(t *testing.T) {
	// scripted generator emits text then closes — no Done. Orchestrator
	// must persist an error message and emit EvDone.
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "abrupt"},
			// no EventDone — channel closes after this
		},
	}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	o, chats := newOrchTest(t, gen, info, &fakeSearch{})
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	ch, err := o.Run(context.Background(), RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 2*time.Second)
	last := events[len(events)-1]
	if last.Kind != EvDone || last.StopReason != "error" {
		t.Errorf("last=%+v", last)
	}
}

func TestRun_UnexpectedToolCallLogsAndContinues(t *testing.T) {
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "1", Name: "x", Final: true}},
			{Kind: llm.EventText, TextDelta: "ok"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	o, chats := newOrchTest(t, gen, info, &fakeSearch{})
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	ch, err := o.Run(context.Background(), RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err != nil {
		t.Fatal(err)
	}
	for ev := range ch {
		_ = ev
	}
	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	if msgs[1].Content != "ok" {
		t.Errorf("content=%q", msgs[1].Content)
	}
}

// failingChatStore wraps fakeChats but returns an error from
// ListMessages so we can exercise the packHistory error path.
type failingChatStore struct{ *fakeChats }

func (f *failingChatStore) ListMessages(_ context.Context, _ uuid.UUID) ([]model.ChatMessage, error) {
	return nil, errors.New("list-msgs failure")
}

// errChatStore returns an error from GetChat or AppendMessage as
// configured.
type errChatStore struct {
	*fakeChats
	getErr    error
	appendErr error
}

func (e *errChatStore) GetChat(ctx context.Context, id uuid.UUID) (*model.Chat, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.fakeChats.GetChat(ctx, id)
}

func (e *errChatStore) AppendMessage(ctx context.Context, msg *model.ChatMessage) error {
	if e.appendErr != nil {
		return e.appendErr
	}
	return e.fakeChats.AppendMessage(ctx, msg)
}

func TestRun_GetChatErrorPropagates(t *testing.T) {
	chats := newFakeChats()
	failing := &errChatStore{fakeChats: chats, getErr: errors.New("db down")}
	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry {
			return &fakeRegistry{gen: &fakeGenerator{}, info: llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6"}}
		},
		Search: &fakeSearch{},
		Chats:  failing,
		Cfg:    DefaultConfig(),
		Log:    zap.NewNop(),
	})
	_, err := o.Run(context.Background(), RunInput{ChatID: uuid.New(), UserID: uuid.New(), Content: "q"})
	if err == nil {
		t.Fatal("expected error from GetChat")
	}
}

func TestRun_AppendUserMessageErrorPropagates(t *testing.T) {
	chats := newFakeChats()
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")
	failing := &errChatStore{fakeChats: chats, appendErr: errors.New("disk full")}
	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry {
			return &fakeRegistry{gen: &fakeGenerator{}, info: llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6"}}
		},
		Search: &fakeSearch{},
		Chats:  failing,
		Cfg:    DefaultConfig(),
		Log:    zap.NewNop(),
	})
	_, err := o.Run(context.Background(), RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err == nil {
		t.Fatal("expected error from AppendMessage")
	}
}

func TestRun_PackHistoryFailureFallsBackToEmptyHistory(t *testing.T) {
	gen := &fakeGenerator{events: []llm.Event{{Kind: llm.EventDone, StopReason: llm.StopEnd}}}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	chats := newFakeChats()
	failing := &failingChatStore{fakeChats: chats}
	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry { return &fakeRegistry{gen: gen, info: info} },
		Search:   &fakeSearch{},
		Chats:    failing,
		Cfg:      DefaultConfig(),
		Log:      zap.NewNop(),
	})
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	ch, err := o.Run(context.Background(), RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for range ch {
	}
	// Assistant message still persisted despite the history failure.
	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	if len(msgs) != 2 {
		t.Errorf("got %d msgs", len(msgs))
	}
}

func TestParserDocsFromLLM_PreservesOrder(t *testing.T) {
	docs := []llm.Document{
		{ID: "x"},
		{ID: "y"},
	}
	pd := parserDocsFromLLM(docs)
	if len(pd) != 2 || pd[0].DocID != "x" || pd[1].DocID != "y" {
		t.Errorf("pd=%+v", pd)
	}
}
