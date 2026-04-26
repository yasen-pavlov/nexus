package rag

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// Phase 2/3 baseline: turn-flow, citation anchoring, history packing,
// error paths. Phase 4 cases live in orchestrator_rewrite_test.go and
// orchestrator_title_test.go; shared fakes + helpers in
// orchestrator_fakes_test.go.

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

	events := runOrchAndDrain(t, o, RunInput{
		ChatID:  chat.ID,
		UserID:  chat.UserID,
		Content: "ping",
	})

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

func TestRun_AnthropicCitations_AnchoredAtSentenceBoundary(t *testing.T) {
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "Hel"},
			{Kind: llm.EventCitation, Citation: &llm.Citation{DocID: "doc-a", CitedText: "src", SpanStart: 0, SpanEnd: 0}},
			{Kind: llm.EventText, TextDelta: "lo, world. Next sentence"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", Provider: "anthropic", SupportsCitations: true}
	search := &fakeSearch{docs: []model.DocumentHit{
		{Document: model.Document{ID: uuid.MustParse("00000000-0000-0000-0000-000000000003"), Title: "Doc A", SourceType: "filesystem", Content: "alpha"}, Headline: "alpha"},
	}}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "ping"})

	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages", len(msgs))
	}
	asst := msgs[1]
	if asst.Content != "Hello, world. Next sentence" {
		t.Errorf("content=%q", asst.Content)
	}
	if len(asst.Citations) != 1 {
		t.Fatalf("citations=%+v", asst.Citations)
	}
	const wantAnchor = 13
	if asst.Citations[0].SpanStart != wantAnchor || asst.Citations[0].SpanEnd != wantAnchor {
		t.Errorf("anchor=[%d,%d] want=[%d,%d]", asst.Citations[0].SpanStart, asst.Citations[0].SpanEnd, wantAnchor, wantAnchor)
	}
}

func TestRun_AnthropicCitations_FlushOnDoneWithoutTerminator(t *testing.T) {
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "abc"},
			{Kind: llm.EventCitation, Citation: &llm.Citation{DocID: "doc-a", CitedText: "src", SpanStart: 0, SpanEnd: 0}},
			{Kind: llm.EventText, TextDelta: " def"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", Provider: "anthropic", SupportsCitations: true}
	search := &fakeSearch{docs: []model.DocumentHit{
		{Document: model.Document{ID: uuid.MustParse("00000000-0000-0000-0000-000000000004"), Title: "Doc A", SourceType: "filesystem", Content: "alpha"}, Headline: "alpha"},
	}}
	o, chats := newOrchTest(t, gen, info, search)
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "ping"})

	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	asst := msgs[1]
	if asst.Content != "abc def" {
		t.Errorf("content=%q", asst.Content)
	}
	if len(asst.Citations) != 1 {
		t.Fatalf("citations=%+v", asst.Citations)
	}
	if asst.Citations[0].SpanEnd != len(asst.Content) {
		t.Errorf("anchor=%d want=%d", asst.Citations[0].SpanEnd, len(asst.Content))
	}
}

func TestNextSentenceBoundary(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		start int
		want  int
	}{
		{"period", "Hello, world. Next", 0, 13},
		{"newline", "First\nSecond", 0, 6},
		{"question", "Right? Yes", 0, 6},
		{"exclamation", "Wow! Ok", 0, 4},
		{"none", "no terminator yet", 0, 0},
		{"start past end", "abc", 5, 0},
		{"start past first terminator", "Sentence one. Sentence two.", 14, 0},
		{"decimal not boundary", "Claude Opus 4.7 launch", 0, 0},
		{"money not boundary", "€107.10 was paid", 0, 0},
		{"trailing period defers", "Claude Opus 4.", 0, 0},
		{"period before newline", "End.\nNext", 0, 4},
		{"list marker not boundary", "Done.\n\n2. Next item", 0, 5},
		{"list marker scanned past", "2. First item\n", 0, 14},
		{"digit period at sentence end punts", "Page 5. New section", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextSentenceBoundary(tc.text, tc.start); got != tc.want {
				t.Errorf("got %d want %d", got, tc.want)
			}
		})
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

	events := runOrchAndDrain(t, o, RunInput{
		ChatID:  chat.ID,
		UserID:  chat.UserID,
		Content: "q",
	})

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

	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

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

	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

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

	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
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

	runOrchAndDrain(t, o, RunInput{
		ChatID:  chat.ID,
		UserID:  chat.UserID,
		Content: "q",
		Model:   "anthropic:claude-haiku-4-5",
	})
	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	if msgs[1].Model != "anthropic:claude-haiku-4-5" {
		t.Errorf("persisted model=%q want override", msgs[1].Model)
	}
}

func TestRun_NoModelConfigured(t *testing.T) {
	gen := &fakeGenerator{}
	chats := newFakeChats()
	chat := makeChat(t, chats, "")
	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry { return &fakeRegistry{gen: gen, err: errors.New("no models")} },
		Settings: func() Settings { return Settings{} },
		Search:   &fakeSearch{},
		Chats:    chats,
		Cfg:      DefaultConfig(),
		Log:      zap.NewNop(),
	})

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

	ctx := context.Background()
	if err := chats.AppendMessage(ctx, &model.ChatMessage{ChatID: chat.ID, Role: model.ChatRoleUser, Content: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := chats.AppendMessage(ctx, &model.ChatMessage{ChatID: chat.ID, Role: model.ChatRoleAssistant, Content: "first answer"}); err != nil {
		t.Fatal(err)
	}

	runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "follow up"})

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
	runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "hello"})

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
		Settings: func() Settings { return Settings{} },
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
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "abrupt"},
		},
	}
	info := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	o, chats := newOrchTest(t, gen, info, &fakeSearch{})
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
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

	runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	if msgs[1].Content != "ok" {
		t.Errorf("content=%q", msgs[1].Content)
	}
}

func TestRun_GetChatErrorPropagates(t *testing.T) {
	chats := newFakeChats()
	failing := &errChatStore{fakeChats: chats, getErr: errors.New("db down")}
	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry {
			return &fakeRegistry{gen: &fakeGenerator{}, info: llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6"}}
		},
		Settings: func() Settings { return Settings{} },
		Search:   &fakeSearch{},
		Chats:    failing,
		Cfg:      DefaultConfig(),
		Log:      zap.NewNop(),
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
		Settings: func() Settings { return Settings{} },
		Search:   &fakeSearch{},
		Chats:    failing,
		Cfg:      DefaultConfig(),
		Log:      zap.NewNop(),
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
		Settings: func() Settings { return Settings{} },
		Search:   &fakeSearch{},
		Chats:    failing,
		Cfg:      DefaultConfig(),
		Log:      zap.NewNop(),
	})
	chat := makeChat(t, chats, "anthropic:claude-sonnet-4-6")

	runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

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
