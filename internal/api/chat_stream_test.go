package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/muty/nexus/internal/rag"
)

// A reasoning model can go quiet for longer than a reverse proxy's idle
// timeout. The stream has to keep sending bytes through that silence, and
// the frames around it have to arrive unchanged and in order.
func TestStreamRagEvents_KeepsAliveThroughSilence(t *testing.T) {
	events := make(chan rag.Event)
	go func() {
		defer close(events)
		events <- rag.Event{Kind: rag.EvText, TextDelta: "before"}
		time.Sleep(60 * time.Millisecond) // the model is thinking
		events <- rag.Event{Kind: rag.EvText, TextDelta: "after"}
	}()

	rec := httptest.NewRecorder()
	streamRagEvents(rec, events, 5*time.Millisecond)

	body := rec.Body.String()
	before := strings.Index(body, `"delta":"before"`)
	after := strings.Index(body, `"delta":"after"`)
	if before < 0 || after < before {
		t.Fatalf("frames missing or out of order:\n%s", body)
	}
	if !strings.Contains(body[before:after], ": keepalive\n\n") {
		t.Errorf("nothing was sent during the silence:\n%s", body)
	}
	// A comment must never land inside a frame, or the client would see a
	// truncated event.
	for _, frame := range strings.Split(strings.TrimSuffix(body, "\n\n"), "\n\n") {
		if frame != ": keepalive" && !strings.HasPrefix(frame, "event: text\ndata: {") {
			t.Errorf("malformed frame %q", frame)
		}
	}
}
