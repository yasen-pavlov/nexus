package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/llm"
	"go.uber.org/zap"
)

func TestNew_ProducesClient(t *testing.T) {
	c := New("http://localhost:11434", zap.NewNop())
	if c == nil {
		t.Fatal("expected client")
	}
}

func TestGenerate_RequiresModel(t *testing.T) {
	c := New("http://localhost:11434", zap.NewNop())
	_, err := c.Generate(context.Background(), llm.GenerateRequest{})
	if err == nil {
		t.Fatal("expected error when model is empty")
	}
}

func TestGenerate_RequiresMessages(t *testing.T) {
	c := New("http://localhost:11434", zap.NewNop())
	_, err := c.Generate(context.Background(), llm.GenerateRequest{Model: "qwen3:14b"})
	if err == nil {
		t.Fatal("expected error when no messages")
	}
}

// fakeNDJSON serves a fixed sequence of NDJSON chunks back as a chat response.
func fakeNDJSON(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/chat") {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("flusher unavailable")
		}
		for _, f := range frames {
			_, _ = fmt.Fprintln(w, f)
			flusher.Flush()
		}
	}))
}

func TestGenerate_StreamsText(t *testing.T) {
	frames := []string{
		`{"model":"gemma3:12b","message":{"role":"assistant","content":"Hello "},"done":false}`,
		`{"model":"gemma3:12b","message":{"role":"assistant","content":"world."},"done":false}`,
		`{"model":"gemma3:12b","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":12,"eval_count":3}`,
	}
	srv := fakeNDJSON(t, frames)
	defer srv.Close()

	c := New(srv.URL, zap.NewNop())

	ch, err := c.Generate(context.Background(), llm.GenerateRequest{
		Model:    "gemma3:12b",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var text strings.Builder
	var done llm.Event
	for ev := range ch {
		switch ev.Kind {
		case llm.EventText:
			text.WriteString(ev.TextDelta)
		case llm.EventDone:
			done = ev
		case llm.EventError:
			t.Fatalf("err: %v", ev.Err)
		}
	}

	if got := text.String(); got != "Hello world." {
		t.Errorf("text = %q", got)
	}
	if done.StopReason != llm.StopEnd {
		t.Errorf("stop = %q", done.StopReason)
	}
	if done.Usage == nil || done.Usage.OutputTokens != 3 {
		t.Errorf("usage missing or wrong: %+v", done.Usage)
	}
}

func TestGenerate_StreamsToolCall(t *testing.T) {
	frames := []string{
		`{"model":"gemma3:12b","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"nexus_search","arguments":{"query":"foo"}}}]},"done":true,"done_reason":"tool_calls","prompt_eval_count":1,"eval_count":2}`,
	}
	srv := fakeNDJSON(t, frames)
	defer srv.Close()

	c := New(srv.URL, zap.NewNop())

	ch, err := c.Generate(context.Background(), llm.GenerateRequest{
		Model:    "gemma3:12b",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "search"}},
		Tools:    []llm.Tool{{Name: "nexus_search", Description: "search", Schema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var args string
	var sawFinal bool
	var stop llm.StopReason
	for ev := range ch {
		switch ev.Kind {
		case llm.EventToolCall:
			if ev.ToolCall.Final {
				sawFinal = true
			}
			args = ev.ToolCall.ArgsJSON
		case llm.EventDone:
			stop = ev.StopReason
		case llm.EventError:
			t.Fatalf("err: %v", ev.Err)
		}
	}

	if !sawFinal {
		t.Error("expected Final tool delta")
	}
	if !strings.Contains(args, `"query":"foo"`) {
		t.Errorf("args = %q", args)
	}
	if stop != llm.StopToolUse {
		t.Errorf("stop = %q", stop)
	}
}

func TestGenerate_NonStreamingFallback_OnKnownBrokenModel(t *testing.T) {
	// qwen3:14b is in knownToolStreamingBroken; tools must trigger fallback.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify we asked for non-streaming.
		var body chatRequest
		if err := readJSON(r, &body); err != nil {
			t.Fatal(err)
		}
		if body.Stream {
			t.Fatal("expected stream=false on fallback")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"qwen3:14b","message":{"role":"assistant","content":"answer","tool_calls":[{"function":{"name":"nexus_search","arguments":{"query":"foo"}}}]},"done":true,"done_reason":"tool_calls","prompt_eval_count":5,"eval_count":2}`)
	}))
	defer srv.Close()

	c := New(srv.URL, zap.NewNop())

	ch, err := c.Generate(context.Background(), llm.GenerateRequest{
		Model:    "qwen3:14b",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "search"}},
		Tools:    []llm.Tool{{Name: "nexus_search", Schema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var sawText, sawTool, sawDone bool
	for ev := range ch {
		switch ev.Kind {
		case llm.EventText:
			sawText = true
		case llm.EventToolCall:
			sawTool = true
		case llm.EventDone:
			sawDone = true
		}
	}
	if !sawText || !sawTool || !sawDone {
		t.Errorf("expected text+tool+done; text=%v tool=%v done=%v", sawText, sawTool, sawDone)
	}
}

func TestGenerate_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, zap.NewNop())
	_, err := c.Generate(context.Background(), llm.GenerateRequest{
		Model:    "gemma3:12b",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Error("expected error on 500")
	}
}

// readJSON decodes a request body into v.
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	defer r.Body.Close() //nolint:errcheck // test
	return dec.Decode(v)
}
