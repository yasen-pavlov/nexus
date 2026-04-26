package rag

import (
	"context"
	"testing"
	"time"

	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
)

// scriptedTitle builds a fakeGenerator that emits a single short title
// the orchestrator's auto-title path captures.
func scriptedTitle(title string) *fakeGenerator {
	return &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: title},
			{Kind: llm.EventDone, StopReason: llm.StopEnd, Usage: &llm.Usage{InputTokens: 25, OutputTokens: 4}},
		},
	}
}

func TestRun_AutoTitle_FirstTurnEndTurnSetsTitle(t *testing.T) {
	mainGen := &fakeGenerator{events: []llm.Event{
		{Kind: llm.EventText, TextDelta: "answer"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}}
	titleGen := scriptedTitle("Anthropic invoice summary")
	mainInfo := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	rewriterInfo := llm.ModelInfo{ID: "anthropic:claude-haiku-4-5", BareID: "claude-haiku-4-5"}
	search := &fakeSearch{}

	o, chats, _ := newOrchTestWithRewriter(t, mainGen, titleGen, mainInfo, rewriterInfo, search)
	chat := makeChat(t, chats, mainInfo.ID)

	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "find Anthropic invoices"})

	// EvTitle emitted before EvDone.
	titleIdx, doneIdx := -1, -1
	for i, ev := range events {
		switch ev.Kind {
		case EvTitle:
			titleIdx = i
			if ev.Title != "Anthropic invoice summary" {
				t.Errorf("title=%q", ev.Title)
			}
		case EvDone:
			doneIdx = i
		}
	}
	if titleIdx < 0 || doneIdx < 0 || titleIdx >= doneIdx {
		t.Errorf("title ordering wrong: title=%d done=%d", titleIdx, doneIdx)
	}

	// chats.UpdateChat was called with the title.
	if titles := chats.titleUpdates(); len(titles) != 1 || titles[0] != "Anthropic invoice summary" {
		t.Errorf("title updates=%v", titles)
	}
}

func TestRun_AutoTitle_SkippedWhenChatTitled(t *testing.T) {
	mainGen := &fakeGenerator{events: []llm.Event{{Kind: llm.EventDone, StopReason: llm.StopEnd}}}
	titleGen := scriptedTitle("should not run")
	mainInfo := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	rewriterInfo := llm.ModelInfo{ID: "anthropic:claude-haiku-4-5"}
	search := &fakeSearch{}
	o, chats, _ := newOrchTestWithRewriter(t, mainGen, titleGen, mainInfo, rewriterInfo, search)
	chat := makeChat(t, chats, mainInfo.ID)
	chat.Title = "Pre-existing title"
	chats.seed(chat) // re-seed with the title set

	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})

	for _, ev := range events {
		if ev.Kind == EvTitle {
			t.Fatalf("EvTitle should not fire when chat already titled (got %q)", ev.Title)
		}
	}
	if len(chats.titleUpdates()) != 0 {
		t.Errorf("title updates=%v want none", chats.titleUpdates())
	}
}

func TestRun_AutoTitle_SkippedOnNonEndTurnStop(t *testing.T) {
	mainGen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "starting "},
			{Kind: llm.EventError, Err: errExpected},
		},
	}
	titleGen := scriptedTitle("should not run")
	mainInfo := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	rewriterInfo := llm.ModelInfo{ID: "anthropic:claude-haiku-4-5"}
	search := &fakeSearch{}
	o, chats, _ := newOrchTestWithRewriter(t, mainGen, titleGen, mainInfo, rewriterInfo, search)
	chat := makeChat(t, chats, mainInfo.ID)

	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	for _, ev := range events {
		if ev.Kind == EvTitle {
			t.Fatal("EvTitle fired on error turn")
		}
	}
	if len(chats.titleUpdates()) != 0 {
		t.Errorf("title updates=%v want none on error", chats.titleUpdates())
	}
}

func TestRun_AutoTitle_SkippedOnNonFirstTurn(t *testing.T) {
	mainGen := &fakeGenerator{events: []llm.Event{{Kind: llm.EventDone, StopReason: llm.StopEnd}}}
	titleGen := scriptedTitle("should not run")
	mainInfo := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	rewriterInfo := llm.ModelInfo{ID: "anthropic:claude-haiku-4-5"}
	search := &fakeSearch{}
	o, chats, _ := newOrchTestWithRewriter(t, mainGen, titleGen, mainInfo, rewriterInfo, search)
	chat := makeChat(t, chats, mainInfo.ID)

	// Seed prior turn so the new turn isn't the first assistant turn.
	ctx := context.Background()
	_ = chats.AppendMessage(ctx, &model.ChatMessage{ChatID: chat.ID, Role: model.ChatRoleUser, Content: "old"})
	_ = chats.AppendMessage(ctx, &model.ChatMessage{ChatID: chat.ID, Role: model.ChatRoleAssistant, Content: "older answer"})

	events := runOrchAndDrain(t, o, RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "follow up"})

	for _, ev := range events {
		if ev.Kind == EvTitle {
			t.Fatal("EvTitle fired on non-first turn")
		}
	}
	// titleGen here doubles as the rewriter (same registry slot). It
	// SHOULD have run as the rewriter, just not as the titler — so we
	// only assert no title update was persisted.
	if len(chats.titleUpdates()) != 0 {
		t.Errorf("title updates=%v want none on follow-up", chats.titleUpdates())
	}
}

func TestRun_AutoTitle_TimeoutSkipped(t *testing.T) {
	// Title generator delays past the parent context deadline. The
	// orchestrator should still emit EvDone; no EvTitle.
	mainGen := &fakeGenerator{events: []llm.Event{
		{Kind: llm.EventText, TextDelta: "answer"},
		{Kind: llm.EventDone, StopReason: llm.StopEnd},
	}}
	titleGen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "would have been a title"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
		delay: 3 * time.Second, // longer than titleTimeout (2s)
	}
	mainInfo := llm.ModelInfo{ID: "anthropic:claude-sonnet-4-6", SupportsCitations: true}
	rewriterInfo := llm.ModelInfo{ID: "anthropic:claude-haiku-4-5"}
	search := &fakeSearch{}
	o, chats, _ := newOrchTestWithRewriter(t, mainGen, titleGen, mainInfo, rewriterInfo, search)
	chat := makeChat(t, chats, mainInfo.ID)

	// 5-second collection budget — comfortably longer than titleTimeout.
	ch, err := o.Run(context.Background(), RunInput{ChatID: chat.ID, UserID: chat.UserID, Content: "q"})
	if err != nil {
		t.Fatal(err)
	}
	events := collectEvents(t, ch, 5*time.Second)
	for _, ev := range events {
		if ev.Kind == EvTitle {
			t.Fatal("EvTitle fired despite title timeout")
		}
	}
	last := events[len(events)-1]
	if last.Kind != EvDone || last.StopReason != "end_turn" {
		t.Errorf("last=%+v", last)
	}
}

// errExpected is a sentinel used in skipped-on-error tests.
var errExpected = &expectedError{}

type expectedError struct{}

func (e *expectedError) Error() string { return "expected" }
