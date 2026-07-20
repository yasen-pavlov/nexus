package rag

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// newOrchTestWithSettings wires an Orchestrator with a tools-capable model
// and a custom Settings closure (so tests can flex MaxToolRounds).
func newOrchTestWithSettings(t *testing.T, gen *fakeGenerator, info llm.ModelInfo, search SearchProvider, settings Settings) (*Orchestrator, *fakeChats) {
	t.Helper()
	chats := newFakeChats()
	reg := &fakeRegistry{gen: gen, info: info}
	o := NewOrchestrator(Deps{
		Registry: func() llm.Registry { return reg },
		Settings: func() Settings { return settings },
		Search:   search,
		Chats:    chats,
		Cfg:      DefaultConfig(),
		Log:      zap.NewNop(),
	})
	return o, chats
}

func toolsCapableModel() llm.ModelInfo {
	return llm.ModelInfo{
		ID:                "anthropic:claude-sonnet-4-6",
		Provider:          "anthropic",
		BareID:            "claude-sonnet-4-6",
		SupportsTools:     true,
		SupportsCitations: true,
	}
}

func toolsIncapableModel() llm.ModelInfo {
	return llm.ModelInfo{
		ID:            "ollama:llama3.2-vision:11b",
		Provider:      "ollama",
		BareID:        "llama3.2-vision:11b",
		SupportsTools: false,
	}
}

// One tool round → executes → re-generates → end_turn.
//
// Asserts:
//   - tool_start + tool_result events fire in the right order
//   - persisted ChatToolCall is recorded
//   - persisted Evidence is the union (initial + tool-fetched)
//   - final answer text spans both rounds
//   - generator was called twice; bare model id passed each time
func TestRunTurn_OneToolRound_DispatchesAndCompletes(t *testing.T) {
	initial := []model.DocumentHit{
		{Document: model.Document{ID: uuid.New(), Title: "Email A", SourceType: "imap", Content: "alpha"}},
	}
	toolFetched := []model.DocumentHit{
		{Document: model.Document{ID: uuid.New(), Title: "Email B", SourceType: "imap", Content: "beta"}},
	}

	// Two stubs: initial retrieve + the dispatcher's nexus_search call.
	// Both share the same fakeSearch — we rotate the doc set after the
	// first call so the second call returns the tool-fetched docs.
	srch := &rotatingSearch{turns: [][]model.DocumentHit{initial, toolFetched}}

	gen := &fakeGenerator{eventsPerCall: [][]llm.Event{
		// Round 1: model decides to search.
		{
			{Kind: llm.EventText, TextDelta: "Let me check. "},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{"query":"beta"}`}},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Final: true}},
			{Kind: llm.EventDone, StopReason: llm.StopToolUse, Usage: &llm.Usage{InputTokens: 100, OutputTokens: 20}},
		},
		// Round 2: model produces the answer.
		{
			{Kind: llm.EventText, TextDelta: "Found beta."},
			{Kind: llm.EventDone, StopReason: llm.StopEnd, Usage: &llm.Usage{InputTokens: 150, OutputTokens: 5}},
		},
	}}

	o, chats := newOrchTestWithSettings(t, gen, toolsCapableModel(), srch, Settings{MaxToolRounds: 3})
	chat := makeChat(t, chats, toolsCapableModel().ID)

	events := runOrchAndDrain(t, o, RunInput{
		ChatID: chat.ID, UserID: chat.UserID, Content: "what about beta?",
	})

	// Drain assertions.
	if got := gen.callCount(); got != 2 {
		t.Fatalf("generator calls = %d, want 2", got)
	}
	wantSequence := []EventKind{
		EvRetrieving, EvEvidence,
		EvText, // "Let me check. "
		EvToolStart, EvToolResult,
		EvText, // "Found beta."
		EvUsage, EvDone,
	}
	if !eventKindsMatch(events, wantSequence) {
		t.Errorf("event kinds = %v, want %v", eventKinds(events), wantSequence)
	}

	// Tool start/result payload.
	for _, ev := range events {
		switch ev.Kind {
		case EvToolStart:
			if ev.ToolName != "nexus_search" || !strings.Contains(ev.ToolArgs, "beta") {
				t.Errorf("EvToolStart payload bad: name=%q args=%q", ev.ToolName, ev.ToolArgs)
			}
		case EvToolResult:
			if ev.ToolName != "nexus_search" {
				t.Errorf("EvToolResult name = %q", ev.ToolName)
			}
			if len(ev.ToolChunks) != 1 {
				t.Errorf("EvToolResult chunks = %d, want 1", len(ev.ToolChunks))
			}
		}
	}

	// Bare model id passed on EVERY round (regression for feedback_llm_bare_id.md).
	for i := range 2 {
		req := gen.requestAt(i)
		if req.Model != "claude-sonnet-4-6" {
			t.Errorf("round %d Model = %q, want bare id", i, req.Model)
		}
	}

	// Round-2 request carries the cumulative documents (initial + tool-fetched).
	if got := len(gen.requestAt(1).Documents); got != 2 {
		t.Errorf("round 2 Documents = %d, want 2 (cumulative)", got)
	}

	// Persisted assistant message captures everything.
	msgs, _ := chats.ListMessages(context.Background(), chat.ID)
	var asst *model.ChatMessage
	for i := range msgs {
		if msgs[i].Role == model.ChatRoleAssistant {
			asst = &msgs[i]
		}
	}
	if asst == nil {
		t.Fatal("no assistant message persisted")
	}
	if !strings.Contains(asst.Content, "Let me check") || !strings.Contains(asst.Content, "Found beta") {
		t.Errorf("persisted content lost a round: %q", asst.Content)
	}
	if len(asst.ToolCalls) != 1 {
		t.Errorf("ToolCalls = %d, want 1", len(asst.ToolCalls))
	}
	if got := asst.ToolCalls[0].Name; got != "nexus_search" {
		t.Errorf("ToolCalls[0].Name = %q", got)
	}
	if len(asst.Evidence) != 2 {
		t.Errorf("persisted Evidence = %d, want 2 (initial + tool-fetched union)", len(asst.Evidence))
	}
	if asst.Usage == nil || asst.Usage.Input != 250 {
		t.Errorf("Usage.Input = %v, want 250 (sum across rounds)", asst.Usage)
	}
}

func TestRunTurn_TwoToolRounds_DispatchesBoth(t *testing.T) {
	srch := &rotatingSearch{turns: [][]model.DocumentHit{
		{{Document: model.Document{ID: uuid.New(), Title: "init", SourceType: "imap", Content: "x"}}},
		{{Document: model.Document{ID: uuid.New(), Title: "r1", SourceType: "imap", Content: "y"}}},
		{{Document: model.Document{ID: uuid.New(), Title: "r2", SourceType: "imap", Content: "z"}}},
	}}
	gen := &fakeGenerator{eventsPerCall: [][]llm.Event{
		{
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{"query":"y"}`}},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Final: true}},
			{Kind: llm.EventDone, StopReason: llm.StopToolUse},
		},
		{
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_2", Name: "nexus_search", ArgsJSON: `{"query":"z"}`}},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_2", Final: true}},
			{Kind: llm.EventDone, StopReason: llm.StopToolUse},
		},
		{
			{Kind: llm.EventText, TextDelta: "done."},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}}
	o, chats := newOrchTestWithSettings(t, gen, toolsCapableModel(), srch, Settings{MaxToolRounds: 3})
	chat := makeChat(t, chats, toolsCapableModel().ID)

	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

	if gen.callCount() != 3 {
		t.Errorf("generator calls = %d, want 3", gen.callCount())
	}
	starts := countEvents(events, EvToolStart)
	if starts != 2 {
		t.Errorf("EvToolStart = %d, want 2", starts)
	}
	// Cumulative documents grow per round: init=1 → after r1=2 → after r2=3.
	if got := len(gen.requestAt(2).Documents); got != 3 {
		t.Errorf("round 3 Documents = %d, want 3", got)
	}
}

func TestRunTurn_ToolRoundCapExceeded_FinalRoundHasNoTools(t *testing.T) {
	srch := &rotatingSearch{turns: [][]model.DocumentHit{
		{{Document: model.Document{ID: uuid.New(), Title: "init", SourceType: "imap", Content: "x"}}},
		{{Document: model.Document{ID: uuid.New(), Title: "r1", SourceType: "imap", Content: "y"}}},
	}}
	gen := &fakeGenerator{eventsPerCall: [][]llm.Event{
		{
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{"query":"y"}`}},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Final: true}},
			{Kind: llm.EventDone, StopReason: llm.StopToolUse},
		},
		{
			{Kind: llm.EventText, TextDelta: "ok"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}}
	o, chats := newOrchTestWithSettings(t, gen, toolsCapableModel(), srch, Settings{MaxToolRounds: 1})
	chat := makeChat(t, chats, toolsCapableModel().ID)
	runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

	// Round 1 (toolRound==0): tools enabled.
	if got := len(gen.requestAt(0).Tools); got != 1 {
		t.Errorf("round 0 Tools = %d, want 1", got)
	}
	// Round 2 (toolRound==1, == MaxToolRounds): tools disabled — model is forced to answer.
	if got := len(gen.requestAt(1).Tools); got != 0 {
		t.Errorf("round 1 Tools = %d, want 0 (cap exceeded)", got)
	}
}

func TestRunTurn_MaxToolRoundsZero_DisablesToolsEntirely(t *testing.T) {
	srch := &fakeSearch{docs: []model.DocumentHit{
		{Document: model.Document{ID: uuid.New(), Title: "init", SourceType: "imap", Content: "x"}},
	}}
	gen := &fakeGenerator{events: []llm.Event{
		{Kind: llm.EventText, TextDelta: "answer"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}}
	o, chats := newOrchTestWithSettings(t, gen, toolsCapableModel(), srch, Settings{MaxToolRounds: 0})
	chat := makeChat(t, chats, toolsCapableModel().ID)
	runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if got := len(gen.requestAt(0).Tools); got != 0 {
		t.Errorf("round 0 Tools = %d, want 0 (MaxToolRounds=0)", got)
	}
}

func TestRunTurn_ToolsOmittedForUnsupportedModel(t *testing.T) {
	srch := &fakeSearch{docs: []model.DocumentHit{
		{Document: model.Document{ID: uuid.New(), Title: "init", SourceType: "imap", Content: "x"}},
	}}
	gen := &fakeGenerator{events: []llm.Event{
		{Kind: llm.EventText, TextDelta: "answer"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}}
	o, chats := newOrchTestWithSettings(t, gen, toolsIncapableModel(), srch, Settings{MaxToolRounds: 3})
	chat := makeChat(t, chats, toolsIncapableModel().ID)
	runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if got := len(gen.requestAt(0).Tools); got != 0 {
		t.Errorf("Tools = %d, want 0 for tools-incapable model", got)
	}
}

func TestRunTurn_ToolDispatcherMalformedArgs_ContinuesLoop(t *testing.T) {
	srch := &fakeSearch{docs: []model.DocumentHit{
		{Document: model.Document{ID: uuid.New(), Title: "init", SourceType: "imap", Content: "x"}},
	}}
	gen := &fakeGenerator{eventsPerCall: [][]llm.Event{
		{
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{not-json`}},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Final: true}},
			{Kind: llm.EventDone, StopReason: llm.StopToolUse},
		},
		{
			{Kind: llm.EventText, TextDelta: "sorry."},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}}
	o, chats := newOrchTestWithSettings(t, gen, toolsCapableModel(), srch, Settings{MaxToolRounds: 3})
	chat := makeChat(t, chats, toolsCapableModel().ID)
	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

	if gen.callCount() != 2 {
		t.Errorf("generator calls = %d, want 2 (malformed args shouldn't crash the round)", gen.callCount())
	}
	// Tool result text should explain the failure so the model can correct.
	var tr Event
	for _, ev := range events {
		if ev.Kind == EvToolResult {
			tr = ev
		}
	}
	if !strings.Contains(tr.ToolSummary, "invalid") {
		t.Errorf("tool summary = %q, want mention of invalid args", tr.ToolSummary)
	}
}

func TestRunTurn_ToolDispatcherSearchError_TurnSucceeds(t *testing.T) {
	// Initial retrieval works; the in-tool search errors. Use a sequenced
	// search: 1st call succeeds (initial), 2nd call (tool) returns err.
	srch := &sequencedSearch{steps: []searchStep{
		{docs: []model.DocumentHit{{Document: model.Document{ID: uuid.New(), Title: "init", SourceType: "imap", Content: "x"}}}},
		{err: errors.New("os down")},
	}}
	gen := &fakeGenerator{eventsPerCall: [][]llm.Event{
		{
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{"query":"y"}`}},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Final: true}},
			{Kind: llm.EventDone, StopReason: llm.StopToolUse},
		},
		{
			{Kind: llm.EventText, TextDelta: "ok"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}}
	o, chats := newOrchTestWithSettings(t, gen, toolsCapableModel(), srch, Settings{MaxToolRounds: 3})
	chat := makeChat(t, chats, toolsCapableModel().ID)
	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

	// Turn must complete with end_turn (not error) — search failure folds
	// into the tool_result text.
	last := events[len(events)-1]
	if last.Kind != EvDone || last.StopReason != string(llm.StopEnd) {
		t.Errorf("last event = %+v, want EvDone end_turn", last)
	}
}

func TestRunTurn_CancellationBetweenRounds(t *testing.T) {
	srch := &fakeSearch{docs: []model.DocumentHit{
		{Document: model.Document{ID: uuid.New(), Title: "init", SourceType: "imap", Content: "x"}},
	}}
	// Round 1 finishes with tool_use; ctx is cancelled via the second
	// generator call's delay before round 2 can drain.
	gen := &fakeGenerator{
		eventsPerCall: [][]llm.Event{
			{
				{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{"query":"y"}`}},
				{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Final: true}},
				{Kind: llm.EventDone, StopReason: llm.StopToolUse},
			},
			// Round 2 hangs forever (delay) — the ctx-cancel below will
			// fire the EvDone{Cancelled} path inside the goroutine.
			{
				{Kind: llm.EventText, TextDelta: "would-be-answer"},
				{Kind: llm.EventDone, StopReason: llm.StopEnd},
			},
		},
		delay: 5 * time.Second,
	}
	o, chats := newOrchTestWithSettings(t, gen, toolsCapableModel(), srch, Settings{MaxToolRounds: 3})
	chat := makeChat(t, chats, toolsCapableModel().ID)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := o.Run(ctx, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Cancel after a short window so round 1 dispatches but round 2 is interrupted.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	events := collectEvents(t, ch, 3*time.Second)

	last := events[len(events)-1]
	if last.Kind != EvDone || last.StopReason != "cancelled" {
		t.Errorf("final event = %+v, want EvDone cancelled", last)
	}
}

func TestRunTurn_CumulativeDocsDeduped(t *testing.T) {
	// Initial doc and tool-fetched doc share a DocID — cumulative slice
	// should still be unique.
	sharedID := uuid.New()
	srch := &rotatingSearch{turns: [][]model.DocumentHit{
		{{Document: model.Document{ID: sharedID, Title: "shared", SourceType: "imap", Content: "x"}}},
		{
			{Document: model.Document{ID: sharedID, Title: "shared", SourceType: "imap", Content: "x"}},
			{Document: model.Document{ID: uuid.New(), Title: "new", SourceType: "imap", Content: "y"}},
		},
	}}
	gen := &fakeGenerator{eventsPerCall: [][]llm.Event{
		{
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{"query":"shared"}`}},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Final: true}},
			{Kind: llm.EventDone, StopReason: llm.StopToolUse},
		},
		{
			{Kind: llm.EventText, TextDelta: "k"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}}
	o, chats := newOrchTestWithSettings(t, gen, toolsCapableModel(), srch, Settings{MaxToolRounds: 3})
	chat := makeChat(t, chats, toolsCapableModel().ID)
	runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

	round2 := gen.requestAt(1)
	if got := len(round2.Documents); got != 2 {
		t.Errorf("round 2 Documents = %d, want 2 (deduped — shared id collapsed)", got)
	}
}

// Non-Anthropic provider — citation parser must rebuild per round so [N]
// markers in round 2 can reference a doc index added by the tool result.
// TestRunTurn_CitationSpanOffsetAcrossRounds pins the fix for a per-round
// parser that started its cursor at 0: when round 1 emits a text preamble
// alongside its tool call, a round-2 citation must anchor at its offset in the
// WHOLE-turn answer (past the preamble), not at the round-local offset.
func TestRunTurn_CitationSpanOffsetAcrossRounds(t *testing.T) {
	initialID := uuid.New().String()
	toolID := uuid.New().String()
	srch := &rotatingSearch{turns: [][]model.DocumentHit{
		{{Document: model.Document{ID: parseUUID(t, initialID), Title: "init", SourceType: "imap", Content: "x"}}},
		{{Document: model.Document{ID: parseUUID(t, toolID), Title: "from-tool", SourceType: "imap", Content: "y"}}},
	}}

	gen := &fakeGenerator{eventsPerCall: [][]llm.Event{
		{
			{Kind: llm.EventText, TextDelta: "Let me search. "}, // round-1 preamble
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{"query":"x"}`}},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Final: true}},
			{Kind: llm.EventDone, StopReason: llm.StopToolUse},
		},
		{
			{Kind: llm.EventText, TextDelta: "You paid it [2]."},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}}

	noCitationModel := llm.ModelInfo{
		ID: "openai:gpt-5", Provider: "openai", BareID: "gpt-5",
		SupportsTools: true, SupportsCitations: false,
	}
	o, chats := newOrchTestWithSettings(t, gen, noCitationModel, srch, Settings{MaxToolRounds: 3})
	chat := makeChat(t, chats, noCitationModel.ID)
	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

	// utf16("Let me search. ")=15 + utf16("You paid it ")=12.
	const wantSpan = 27
	span := -1
	for _, ev := range events {
		if ev.Kind == EvCitation && ev.Citation != nil {
			span = ev.Citation.SpanStart
		}
	}
	if span != wantSpan {
		t.Errorf("citation SpanStart = %d, want %d (offset past round-1 preamble)", span, wantSpan)
	}
}

func TestRunTurn_CitationParserRebuiltPerRound(t *testing.T) {
	initialID := uuid.New().String()
	toolID := uuid.New().String()

	// Search returns docs whose IDs we can recognize in citation events.
	srch := &rotatingSearch{turns: [][]model.DocumentHit{
		{{Document: model.Document{ID: parseUUID(t, initialID), Title: "init", SourceType: "imap", Content: "x"}}},
		{{Document: model.Document{ID: parseUUID(t, toolID), Title: "from-tool", SourceType: "imap", Content: "y"}}},
	}}

	gen := &fakeGenerator{eventsPerCall: [][]llm.Event{
		{
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{"query":"x"}`}},
			{Kind: llm.EventToolCall, ToolCall: &llm.ToolCallDelta{ID: "tu_1", Final: true}},
			{Kind: llm.EventDone, StopReason: llm.StopToolUse},
		},
		// Round 2: emit a [2] citation — must resolve to the tool-fetched doc.
		{
			{Kind: llm.EventText, TextDelta: "Found it [2]."},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}}

	// Use a model that does NOT support native citations so the [N] parser engages.
	noCitationModel := llm.ModelInfo{
		ID:                "openai:gpt-5",
		Provider:          "openai",
		BareID:            "gpt-5",
		SupportsTools:     true,
		SupportsCitations: false,
	}
	o, chats := newOrchTestWithSettings(t, gen, noCitationModel, srch, Settings{MaxToolRounds: 3})
	chat := makeChat(t, chats, noCitationModel.ID)
	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

	var resolved string
	for _, ev := range events {
		if ev.Kind == EvCitation && ev.Citation != nil {
			resolved = ev.Citation.DocID
		}
	}
	if resolved != toolID {
		t.Errorf("citation DocID = %q, want %q (the tool-fetched doc)", resolved, toolID)
	}
}

// --- helpers ---

func parseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parseUUID(%q): %v", s, err)
	}
	return u
}

func eventKinds(events []Event) []EventKind {
	out := make([]EventKind, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

// eventKindsMatch returns true when `actual` contains `expected` in
// order, allowing extra interleaved events of other kinds. Used so tests
// don't have to enumerate every status frame the orchestrator can emit.
func eventKindsMatch(actual []Event, expected []EventKind) bool {
	i := 0
	for _, ev := range actual {
		if i < len(expected) && ev.Kind == expected[i] {
			i++
		}
	}
	return i == len(expected)
}

func countEvents(events []Event, kind EventKind) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

// rotatingSearch returns a different doc set on each successive call.
// Last entry repeats forever.
type rotatingSearch struct {
	turns [][]model.DocumentHit
	calls int
}

func (r *rotatingSearch) Run(_ context.Context, _ model.SearchRequest) (*model.SearchResult, error) {
	idx := r.calls
	if idx >= len(r.turns) {
		idx = len(r.turns) - 1
	}
	r.calls++
	return &model.SearchResult{Documents: r.turns[idx], TotalCount: len(r.turns[idx])}, nil
}

// sequencedSearch returns a scripted (docs, err) per call. Last entry
// repeats forever.
type searchStep struct {
	docs []model.DocumentHit
	err  error
}
type sequencedSearch struct {
	steps []searchStep
	calls int
}

func (s *sequencedSearch) Run(_ context.Context, _ model.SearchRequest) (*model.SearchResult, error) {
	idx := s.calls
	if idx >= len(s.steps) {
		idx = len(s.steps) - 1
	}
	s.calls++
	step := s.steps[idx]
	if step.err != nil {
		return nil, step.err
	}
	return &model.SearchResult{Documents: step.docs, TotalCount: len(step.docs)}, nil
}
