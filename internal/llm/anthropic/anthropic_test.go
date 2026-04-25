package anthropic

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
	_, err := c.Generate(context.Background(), llm.GenerateRequest{Model: "claude-x"})
	if err == nil {
		t.Fatal("expected error when no messages or documents")
	}
}

// fakeSSE serves a fixed sequence of stream events back as Anthropic SSE.
// Each entry becomes one event: line + data: line.
func fakeSSE(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
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
	}))
}

func TestGenerate_StreamsTextAndCitations(t *testing.T) {
	frames := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-x\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":0,\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello \"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"world.\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"citations_delta\",\"citation\":{\"type\":\"char_location\",\"cited_text\":\"world\",\"document_index\":0,\"document_title\":\"chunk-abc\",\"start_char_index\":0,\"end_char_index\":5,\"file_id\":\"\"}}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":10,\"output_tokens\":3,\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	srv := fakeSSE(t, frames)
	defer srv.Close()

	c := New("test-key", srv.URL, zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := c.Generate(ctx, llm.GenerateRequest{
		Model:    "claude-x",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var text strings.Builder
	var gotCitation bool
	var done llm.Event
	for ev := range ch {
		switch ev.Kind {
		case llm.EventText:
			text.WriteString(ev.TextDelta)
		case llm.EventCitation:
			if ev.Citation == nil {
				t.Fatal("nil citation")
			}
			if ev.Citation.DocID != "chunk-abc" {
				t.Errorf("citation doc id = %q, want chunk-abc", ev.Citation.DocID)
			}
			gotCitation = true
		case llm.EventDone:
			done = ev
		case llm.EventError:
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}

	if got := text.String(); got != "Hello world." {
		t.Errorf("text = %q, want %q", got, "Hello world.")
	}
	if !gotCitation {
		t.Error("expected at least one citation")
	}
	if done.StopReason != llm.StopEnd {
		t.Errorf("stop reason = %q, want end_turn", done.StopReason)
	}
	if done.Usage == nil || done.Usage.OutputTokens != 3 {
		t.Errorf("usage missing or wrong: %+v", done.Usage)
	}
}

func TestGenerate_StreamsToolCall(t *testing.T) {
	frames := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-x\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0,\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"nexus_search\",\"input\":{}}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"foo\\\"}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	srv := fakeSSE(t, frames)
	defer srv.Close()

	c := New("test-key", srv.URL, zap.NewNop())

	ch, err := c.Generate(context.Background(), llm.GenerateRequest{
		Model:    "claude-x",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "search foo"}},
		Tools: []llm.Tool{{
			Name:        "nexus_search",
			Description: "search index",
			Schema:      map[string]any{"properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var args strings.Builder
	var sawStart, sawFinal bool
	var stop llm.StopReason
	for ev := range ch {
		switch ev.Kind {
		case llm.EventToolCall:
			if ev.ToolCall == nil {
				t.Fatal("nil tool call")
			}
			if ev.ToolCall.ID != "toolu_1" {
				t.Errorf("tool id = %q", ev.ToolCall.ID)
			}
			args.WriteString(ev.ToolCall.ArgsJSON)
			if ev.ToolCall.Final {
				sawFinal = true
			} else if !sawFinal {
				sawStart = true
			}
		case llm.EventDone:
			stop = ev.StopReason
		case llm.EventError:
			t.Fatalf("err: %v", ev.Err)
		}
	}

	if !sawStart || !sawFinal {
		t.Errorf("expected start and final tool deltas; start=%v final=%v", sawStart, sawFinal)
	}
	if got := args.String(); !strings.Contains(got, `"query":"foo"`) {
		t.Errorf("tool args = %q, missing query", got)
	}
	if stop != llm.StopToolUse {
		t.Errorf("stop = %q, want tool_use", stop)
	}
}

func TestGenerate_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()

	c := New("test-key", srv.URL, zap.NewNop())
	ch, err := c.Generate(context.Background(), llm.GenerateRequest{
		Model:    "claude-x",
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
