package llm

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSendOrCancel_Delivers(t *testing.T) {
	out := make(chan Event, 1)
	if !SendOrCancel(context.Background(), out, Event{Kind: EventText, TextDelta: "hi"}) {
		t.Fatal("expected delivery to succeed")
	}
	ev := <-out
	if ev.TextDelta != "hi" {
		t.Errorf("got %q", ev.TextDelta)
	}
}

func TestSendOrCancel_CancelledDoesNotBlock(t *testing.T) {
	// Unbuffered channel with no reader + cancelled ctx. The best-effort
	// cancelled-Done send must NOT block (the goroutine-leak fix): if this
	// blocks, the test times out / deadlocks.
	out := make(chan Event)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan bool, 1)
	go func() {
		done <- SendOrCancel(ctx, out, Event{Kind: EventText, TextDelta: "x"})
	}()

	select {
	case ok := <-done:
		if ok {
			t.Error("expected false on cancelled context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendOrCancel blocked on a cancelled context with no reader (goroutine leak)")
	}
}

func TestRenderDocumentsBlock_EscapesAttributes(t *testing.T) {
	got := RenderDocumentsBlock([]Document{
		{ID: `id"1`, Source: "email", Title: `the <big> "subj" & co`, Date: "2026-01-02", Content: "body"},
	})
	// Attribute values must be escaped so they can't break out of the quotes.
	if !strings.Contains(got, `id="id&quot;1"`) {
		t.Errorf("id not escaped: %s", got)
	}
	if !strings.Contains(got, `title="the &lt;big&gt; &quot;subj&quot; &amp; co"`) {
		t.Errorf("title not escaped: %s", got)
	}
	// Content is verbatim between the tags.
	if !strings.Contains(got, "\nbody\n</document>") {
		t.Errorf("content missing/altered: %s", got)
	}
}

func TestEscapeAttr(t *testing.T) {
	cases := map[string]string{
		`a"b`:   `a&quot;b`,
		`a<b>c`: `a&lt;b&gt;c`,
		`a&b`:   `a&amp;b`,
		`plain`: `plain`,
	}
	for in, want := range cases {
		if got := escapeAttr(in); got != want {
			t.Errorf("escapeAttr(%q) = %q, want %q", in, got, want)
		}
	}
}
