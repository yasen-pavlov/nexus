package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/muty/nexus/internal/llm"
	"go.uber.org/zap"
)

func TestNew_ProducesClient(t *testing.T) {
	c := New("test-key", "", zap.NewNop())
	if c == nil {
		t.Fatal("expected client")
	}
}

func TestGenerate_RequiresModel(t *testing.T) {
	c := New("test-key", "", zap.NewNop())
	_, err := c.Generate(context.Background(), llm.GenerateRequest{})
	if err == nil {
		t.Fatal("expected error when model is empty")
	}
}

func TestGenerate_RequiresMessages(t *testing.T) {
	c := New("test-key", "", zap.NewNop())
	_, err := c.Generate(context.Background(), llm.GenerateRequest{Model: "gpt-x"})
	if err == nil {
		t.Fatal("expected error when no messages")
	}
}

// fakeSSE serves a fixed sequence of OpenAI SSE chunks back. Each entry is one
// "data: {...}\n\n" frame (no event: prefix — OpenAI doesn't use one).
func fakeSSE(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("flusher unavailable")
		}
		for _, f := range frames {
			_, _ = fmt.Fprint(w, f)
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

func TestGenerate_StreamsText(t *testing.T) {
	frames := []string{
		"data: {\"id\":\"cmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello \"},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"cmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"world.\"},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"cmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"total_tokens\":15}}\n\n",
	}
	srv := fakeSSE(t, frames)
	defer srv.Close()

	c := New("test-key", srv.URL, zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := c.Generate(ctx, llm.GenerateRequest{
		Model:    "gpt-x",
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
		"data: {\"id\":\"cmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-x\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"nexus_search\",\"arguments\":\"{\\\"query\\\":\"}}]},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"cmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-x\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"foo\\\"}\"}}]},\"finish_reason\":null}]}\n\n",
		"data: {\"id\":\"cmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n",
	}
	srv := fakeSSE(t, frames)
	defer srv.Close()

	c := New("test-key", srv.URL, zap.NewNop())

	ch, err := c.Generate(context.Background(), llm.GenerateRequest{
		Model:    "gpt-x",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "search"}},
		Tools: []llm.Tool{{
			Name: "nexus_search", Description: "search",
			Schema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// The adapter emits each tool call once, fully assembled, on the Final
	// delta (id + name + complete arguments) — see run().
	var args, name, id string
	var sawFinal bool
	var stop llm.StopReason
	for ev := range ch {
		switch ev.Kind {
		case llm.EventToolCall:
			if ev.ToolCall.Final {
				sawFinal = true
				args = ev.ToolCall.ArgsJSON
				name = ev.ToolCall.Name
				id = ev.ToolCall.ID
			}
		case llm.EventDone:
			stop = ev.StopReason
		case llm.EventError:
			t.Fatalf("err: %v", ev.Err)
		}
	}

	if !sawFinal {
		t.Error("expected a Final tool delta")
	}
	if name != "nexus_search" || id != "call_1" {
		t.Errorf("final tool call id/name = %q/%q, want call_1/nexus_search", id, name)
	}
	if !strings.Contains(args, `"query":"foo"`) {
		t.Errorf("args = %q", args)
	}
	if stop != llm.StopToolUse {
		t.Errorf("stop = %q", stop)
	}
}

func TestGenerate_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"oops"}}`))
	}))
	defer srv.Close()

	c := New("test-key", srv.URL, zap.NewNop())
	ch, err := c.Generate(context.Background(), llm.GenerateRequest{
		Model:    "gpt-x",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("generate (channel-error path): %v", err)
	}
	for ev := range ch {
		if ev.Kind == llm.EventError {
			return
		}
	}
	t.Error("expected EventError on 500")
}

func TestBuildDocumentsBlock_FormatsAsXML(t *testing.T) {
	got := llm.RenderDocumentsBlock([]llm.Document{
		{ID: "chunk-1", Source: "email", Title: "Subject", Date: "2026-01-02", Content: "body"},
	})
	if !strings.Contains(got, `<document index="1" id="chunk-1"`) {
		t.Errorf("missing index=1 attr: %s", got)
	}
	if !strings.Contains(got, "body") {
		t.Errorf("missing content: %s", got)
	}
	if !strings.Contains(got, `source="email"`) {
		t.Errorf("missing source attr: %s", got)
	}
}
