package rag

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/muty/nexus/internal/llm"
	"go.uber.org/zap"
)

func TestCleanTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Anthropic invoice summary", "Anthropic invoice summary"},
		{"trailing period", "Anthropic invoices for April.", "Anthropic invoices for April"},
		{"trailing exclamation", "Found the file!", "Found the file"},
		{"trailing question", "What about that?", "What about that"},
		{"surrounding quotes", `"Quarterly summary"`, "Quarterly summary"},
		{"smart quotes", "“Quarterly summary”", "Quarterly summary"},
		{"single quotes", "'Quick recap'", "Quick recap"},
		{"backticks", "`debug log`", "debug log"},
		{"title prefix", "Title: Anthropic invoices", "Anthropic invoices"},
		{"title prefix lower", "title: chat about logs", "chat about logs"},
		{"multi-line first only", "First line\nsecond line", "First line"},
		{"whitespace", "   spaced   ", "spaced"},
		{"empty", "", ""},
		{"only punctuation", "...", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanTitle(tc.in); got != tc.want {
				t.Errorf("cleanTitle(%q) = %q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanTitle_LengthCapByRunes(t *testing.T) {
	// Bulgarian / German titles exceed the byte budget before they
	// exceed visual length. Make sure the cap is rune-based.
	in := strings.Repeat("ы", 100) // 100 runes; 200 bytes
	got := cleanTitle(in)
	runeCount := 0
	for range got {
		runeCount++
	}
	if runeCount > titleMaxRunes {
		t.Errorf("got %d runes, max %d", runeCount, titleMaxRunes)
	}
}

func TestSummarizeForTitle_HappyPath(t *testing.T) {
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "Anthropic invoice summary"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd, Usage: &llm.Usage{InputTokens: 30, OutputTokens: 4}},
		},
	}
	title, usage, _ := summarizeForTitle(context.Background(), gen, llm.ModelInfo{ID: "test", BareID: "test"}, "Find Anthropic invoices", "Here are 3 invoices…", zap.NewNop())
	if title != "Anthropic invoice summary" {
		t.Errorf("title=%q", title)
	}
	if usage == nil || usage.InputTokens != 30 {
		t.Errorf("usage=%+v", usage)
	}
}

func TestSummarizeForTitle_ErrorReturnsEmpty(t *testing.T) {
	gen := &fakeGenerator{err: errors.New("provider down")}
	title, _, _ := summarizeForTitle(context.Background(), gen, llm.ModelInfo{ID: "test", BareID: "test"}, "q", "a", zap.NewNop())
	if title != "" {
		t.Errorf("want empty title, got %q", title)
	}
}

func TestSummarizeForTitle_EmptyOutputReturnsEmpty(t *testing.T) {
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}
	title, _, _ := summarizeForTitle(context.Background(), gen, llm.ModelInfo{ID: "test", BareID: "test"}, "q", "a", zap.NewNop())
	if title != "" {
		t.Errorf("want empty title, got %q", title)
	}
}

func TestSummarizeForTitle_TimeoutReturnsEmpty(t *testing.T) {
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "would have been a title"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
		delay: 100 * time.Millisecond,
	}
	parent, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	title, _, _ := summarizeForTitle(parent, gen, llm.ModelInfo{ID: "test", BareID: "test"}, "q", "a", zap.NewNop())
	if title != "" {
		t.Errorf("expected empty title on timeout, got %q", title)
	}
}

func TestSummarizeForTitle_StripsTitlePrefix(t *testing.T) {
	gen := &fakeGenerator{
		events: []llm.Event{
			{Kind: llm.EventText, TextDelta: "Title: Anthropic invoices"},
			{Kind: llm.EventDone, StopReason: llm.StopEnd},
		},
	}
	title, _, _ := summarizeForTitle(context.Background(), gen, llm.ModelInfo{ID: "test", BareID: "test"}, "q", "a", zap.NewNop())
	if title != "Anthropic invoices" {
		t.Errorf("title=%q", title)
	}
}
