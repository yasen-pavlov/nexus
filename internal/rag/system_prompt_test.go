package rag

import (
	"strings"
	"testing"
	"time"
)

func TestDateContextLine(t *testing.T) {
	now := time.Date(2026, 6, 5, 14, 0, 0, 0, time.UTC)
	got := dateContextLine(now)
	for _, want := range []string{"Current date:", "Friday", "5 June 2026", "ISO 2026-06-05"} {
		if !strings.Contains(got, want) {
			t.Errorf("dateContextLine missing %q in %q", want, got)
		}
	}
}

func TestSystemPrompt_HasChannelAndDateGuidance(t *testing.T) {
	// Guards the channel-coverage + date-grounding guidance that fixes the
	// "only emails / wrong date range" behavior.
	for _, want := range []string{"ACROSS", "Telegram", "date_from", "never infer today"} {
		if !strings.Contains(systemPromptDefault, want) {
			t.Errorf("systemPromptDefault missing guidance %q", want)
		}
	}
}
