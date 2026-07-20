package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/cliclient"
)

func TestChatNewSession(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	out, err := run(t, "what is the answer?\n/exit\n", "chat")
	if err != nil {
		t.Fatalf("chat: %v\n%s", err, out)
	}
	if !strings.Contains(out, "The answer is 42.") || !strings.Contains(out, "Conversation saved") {
		t.Fatalf("missing answer/save hint: %s", out)
	}
	// A non-empty chat persists (created, not deleted).
	if f.created != 1 || f.deleted != 0 {
		t.Fatalf("non-empty chat must persist: created=%d deleted=%d", f.created, f.deleted)
	}
}

func TestChatEmptySessionDiscarded(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	out, err := run(t, "/exit\n", "chat")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if strings.Contains(out, "Conversation saved") {
		t.Fatalf("an empty session should not be saved: %s", out)
	}
	if f.created != 1 || f.deleted != 1 {
		t.Fatalf("empty chat must be discarded: created=%d deleted=%d", f.created, f.deleted)
	}
}

// TestChatInterruptedFirstTurnKeepsChat guards the fix for the silent-delete
// bug: pressing Ctrl-C partway through the first answer of a NEW chat must NOT
// discard it — the server has already persisted the submitted question. The
// deferred cleanup skips the delete when a question was submitted, and endChat
// still prints the "Conversation saved" hint so the user can recover it.
func TestChatInterruptedFirstTurnKeepsChat(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)

	client := cliclient.New(f.URL, "nexus_pat_x")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel (as Ctrl-C/SIGTERM would) once the stream is mid-answer.
	go func() {
		<-f.hang
		cancel()
	}()

	var out, errOut bytes.Buffer
	// Content contains "hang" so the fake server blocks mid-answer.
	err := runChat(ctx, client, strings.NewReader("hang on my real question\n"), &out, &errOut, chatOptions{})
	if err != nil {
		t.Fatalf("an interrupted turn is a graceful stop, not an error: %v", err)
	}
	if f.created != 1 || f.deleted != 0 {
		t.Fatalf("interrupted first turn must keep the chat: created=%d deleted=%d", f.created, f.deleted)
	}
	if !strings.Contains(errOut.String(), "Conversation saved") {
		t.Fatalf("a kept chat should print the save/resume hint: %q", errOut.String())
	}
}

func TestChatMultiTurnSkipsBlankLines(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	// A blank line between turns is ignored; both questions get answered.
	out, err := run(t, "q1\n\nq2\n/exit\n", "chat")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if n := strings.Count(out, "The answer is 42."); n != 2 {
		t.Fatalf("expected 2 answers, got %d:\n%s", n, out)
	}
	if f.created != 1 {
		t.Fatalf("multi-turn must reuse one chat: created=%d", f.created)
	}
}

func TestChatEOFExits(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	// No /exit — EOF ends the session after answering.
	out, err := run(t, "just one question\n", "chat")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !strings.Contains(out, "The answer is 42.") || !strings.Contains(out, "Conversation saved") {
		t.Fatalf("EOF should still answer + save: %s", out)
	}
}

func TestChatResume(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	out, err := run(t, "follow up\n/exit\n", "chat", "00000000-0000-0000-0000-0000000000aa")
	if err != nil {
		t.Fatalf("chat resume: %v\n%s", err, out)
	}
	// Piped resume emits only the new answer — the prior history ("A chat") is
	// suppressed so the output composes cleanly in scripts.
	if !strings.Contains(out, "The answer is 42.") {
		t.Fatalf("resume should answer the follow-up: %s", out)
	}
	if strings.Contains(out, "A chat") {
		t.Fatalf("piped resume should not dump prior history: %s", out)
	}
	if f.created != 0 || f.deleted != 0 {
		t.Fatalf("resume must not create or delete: created=%d deleted=%d", f.created, f.deleted)
	}
}

func TestChatTurnErrorRecovers(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	// "boom" errors but the session continues; a later good turn succeeds, so the
	// command exits 0 and the chat persists.
	out, err := run(t, "boom\nwhat is the answer?\n/exit\n", "chat")
	if err != nil {
		t.Fatalf("a recoverable per-turn error should not abort: %v\n%s", err, out)
	}
	if !strings.Contains(out, "model exploded") || !strings.Contains(out, "The answer is 42.") {
		t.Fatalf("expected the error then a recovery answer: %s", out)
	}
	if f.created != 1 || f.deleted != 0 {
		t.Fatalf("a session with a successful turn must persist: created=%d deleted=%d", f.created, f.deleted)
	}
}

func TestChatAllErrorsExitNonZero(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	// A session where no turn ever succeeds must exit non-zero so a piped caller
	// can detect failure. The chat is still KEPT, though: the question was
	// submitted and the server persisted the user message, so discarding it would
	// throw away real content (same rationale as the interrupted-turn case).
	out, err := run(t, "boom\n/exit\n", "chat")
	if err == nil || !strings.Contains(err.Error(), "model exploded") {
		t.Fatalf("an all-error session should exit non-zero: %v\n%s", err, out)
	}
	if f.deleted != 0 {
		t.Fatalf("a chat with a submitted question must be kept, not discarded: deleted=%d", f.deleted)
	}
	if !strings.Contains(out, "Conversation saved") {
		t.Fatalf("a kept chat should print the save hint: %s", out)
	}
}
