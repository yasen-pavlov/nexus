package rag

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/muty/nexus/internal/llm"
	"go.uber.org/zap"
)

// Title failure reasons surfaced via EvTitleStatus when the auto-title
// path attempted but couldn't deliver. Empty title from the model is
// reported as "empty"; an underlying generator error or timeout is
// reported as "timeout"/"error" so the FE can show a specific glyph.
const (
	titleFailureTimeout = "timeout"
	titleFailureEmpty   = "empty"
	titleFailureError   = "error"
)

// titleTimeout is intentionally aggressive — auto-titles are nice to
// have, and a degraded provider must not stall the user-visible `done`
// event. 2s comfortably covers Haiku/4o-mini TTFT for an 8-word output.
// On any longer delay we ship the chat untitled and the FE falls back
// to first_message_preview.
const titleTimeout = 2 * time.Second

// titleMaxTokens caps the cheap model's output. The system prompt asks
// for 4–8 words; 24 tokens leaves comfortable headroom for short
// non-English titles (Bulgarian / German often runs longer per word).
const titleMaxTokens = 24

// titleMaxRunes caps the persisted title length defensively. Absurdly
// long output from a misbehaving model would otherwise pollute the
// recent-chats grid. 80 runes ≈ "Quarterly Anthropic invoice
// reconciliation across email and Paperless" — well past anything a
// 4–8-word summary should produce.
const titleMaxRunes = 80

// systemPromptTitle steers the cheap model to emit a single short
// title, no quotes, no preamble, in the user's language.
const systemPromptTitle = `You write a short descriptive title for a chat conversation. Output exactly the title — 4 to 8 words, no quotes, no trailing punctuation, no preamble. Match the language of the user's message (English, German, Bulgarian, etc.). Capture the topic concisely; do not summarise the answer.`

// summarizeForTitle dispatches one short cheap-model call to summarise
// the (user, assistant) pair into a title and persists it on
// chats.title via the orchestrator's UpdateChat path. Returns "" on any
// error or empty/unusable output — caller must treat empty as "leave
// chat untitled, FE falls back to first message preview". When the
// returned title is empty, `failureReason` carries the specific reason
// ("timeout"|"empty"|"error") so the orchestrator can emit
// EvTitleStatus for FE diagnostics.
//
// `info` carries the bare model id; adapters require it
// (`feedback_llm_bare_id`).
func summarizeForTitle(parentCtx context.Context, gen llm.Generator, info llm.ModelInfo, userQ, assistantA string, log *zap.Logger) (title string, usage *llm.Usage, failureReason string) {
	ctx, cancel := context.WithTimeout(parentCtx, titleTimeout)
	defer cancel()

	bareModel := info.BareID
	if bareModel == "" {
		bareModel = info.ID
	}
	req := llm.GenerateRequest{
		Model:  bareModel,
		System: systemPromptTitle,
		Messages: []llm.Message{
			{
				Role: llm.RoleUser,
				Content: "User question:\n" + strings.TrimSpace(userQ) +
					"\n\nAssistant answer:\n" + strings.TrimSpace(assistantA) +
					"\n\nWrite a 4–8 word title for this exchange.",
			},
		},
		MaxTokens: titleMaxTokens,
	}

	text, u, err := collectText(ctx, gen, req)
	usage = u
	if err != nil {
		reason := titleFailureError
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			reason = titleFailureTimeout
		}
		log.Info("rag: title generation skipped",
			zap.Error(err),
			zap.String("raw_response", text),
			zap.String("reason", reason),
		)
		return "", usage, reason
	}

	title = cleanTitle(text)
	if title == "" {
		log.Info("rag: title generation produced empty result")
		return "", usage, titleFailureEmpty
	}
	return title, usage, ""
}

// cleanTitle strips quotes, preambles, trailing punctuation, and caps
// the length. Defensive against models that ignore the system prompt.
func cleanTitle(text string) string {
	t := strings.TrimSpace(text)
	// Drop common preambles like 'Title: ' that some models prepend.
	for _, prefix := range []string{"title:", "Title:", "TITLE:"} {
		if strings.HasPrefix(t, prefix) {
			t = strings.TrimSpace(t[len(prefix):])
		}
	}
	// Take only the first non-empty line — multi-line responses are a
	// model misbehaviour but we shouldn't propagate them as titles.
	if idx := strings.IndexByte(t, '\n'); idx >= 0 {
		t = strings.TrimSpace(t[:idx])
	}
	// Strip surrounding quotes (straight + smart).
	for {
		trimmed := strings.Trim(t, "\"'`“”‘’ ")
		if trimmed == t {
			break
		}
		t = trimmed
	}
	// Strip trailing period / exclamation / question-mark — titles
	// don't carry sentence-end punctuation.
	t = strings.TrimRight(t, ".!?。 ")

	// Cap length by RUNE count, not bytes — Bulgarian / German titles
	// exceed the byte budget before they exceed the visual budget.
	if utf8.RuneCountInString(t) > titleMaxRunes {
		runes := []rune(t)
		t = string(runes[:titleMaxRunes])
		// Re-trim trailing whitespace introduced by the cut.
		t = strings.TrimRight(t, " ")
	}
	return t
}
