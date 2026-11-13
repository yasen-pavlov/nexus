package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muty/nexus/internal/llm"
	"go.uber.org/zap"
)

// fakeResponses is an httptest server speaking the Responses streaming
// protocol. Call N is answered with scripts[N] (the last script repeats);
// every request body is recorded.
type fakeResponses struct {
	*httptest.Server
	mu       sync.Mutex
	requests []map[string]any
}

// sse renders one Responses stream event as an SSE frame.
func sse(eventJSON string) string {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(eventJSON), &probe); err != nil || probe.Type == "" {
		panic("bad event JSON: " + eventJSON)
	}
	return "event: " + probe.Type + "\ndata: " + eventJSON + "\n\n"
}

// completed is a response.completed event with the given usage JSON ("" = none).
func completed(usage string) string {
	if usage == "" {
		return `{"type":"response.completed","response":{"id":"resp_1","status":"completed"}}`
	}
	return `{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":` + usage + `}}`
}

func textDelta(s string) string {
	b, _ := json.Marshal(s)
	return `{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":` + string(b) + `}`
}

func itemDone(index int, item string) string {
	return fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, index, item)
}

func newFake(t *testing.T, scripts ...[]string) *fakeResponses {
	t.Helper()
	f := &fakeResponses{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		n := len(f.requests)
		f.requests = append(f.requests, decoded)
		f.mu.Unlock()
		if n >= len(scripts) {
			n = len(scripts) - 1
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, ev := range scripts[n] {
			_, _ = fmt.Fprint(w, sse(ev))
			flusher.Flush()
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// request returns the i-th recorded request body.
func (f *fakeResponses) request(t *testing.T, i int) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.requests) {
		t.Fatalf("request %d not received (got %d)", i, len(f.requests))
	}
	return f.requests[i]
}

// result is everything one Generate call emitted.
type result struct {
	text  string
	calls []llm.ToolCallDelta
	last  llm.Event // the terminal event
}

// generate runs one request to completion and checks the channel contract:
// exactly one terminal event, and it is the last one.
func generate(t *testing.T, ctx context.Context, baseURL string, req llm.GenerateRequest) result {
	t.Helper()
	ch, err := New("test-key", baseURL, zap.NewNop()).Generate(ctx, req)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return drain(t, ch)
}

func drain(t *testing.T, ch <-chan llm.Event) result {
	t.Helper()
	var res result
	var text strings.Builder
	terminals := 0
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if terminals != 1 {
					t.Fatalf("channel closed after %d terminal events, want exactly 1", terminals)
				}
				res.text = text.String()
				return res
			}
			if terminals > 0 {
				t.Fatalf("event after terminal event: %+v", ev)
			}
			switch ev.Kind {
			case llm.EventText:
				text.WriteString(ev.TextDelta)
			case llm.EventToolCall:
				res.calls = append(res.calls, *ev.ToolCall)
			case llm.EventDone, llm.EventError:
				terminals++
				res.last = ev
			}
		case <-timeout:
			t.Fatal("timed out waiting for the event channel to close")
		}
	}
}

// userReq is a minimal one-question request.
func userReq(model string) llm.GenerateRequest {
	return llm.GenerateRequest{
		Model:    model,
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}
}

// inputItems returns the request's input array as generic objects.
func inputItems(t *testing.T, req map[string]any) []map[string]any {
	t.Helper()
	raw, ok := req["input"].([]any)
	if !ok {
		t.Fatalf("input is not an array: %#v", req["input"])
	}
	items := make([]map[string]any, len(raw))
	for i, it := range raw {
		items[i], ok = it.(map[string]any)
		if !ok {
			t.Fatalf("input[%d] is not an object: %#v", i, it)
		}
	}
	return items
}

// decode parses a JSON object literal for comparisons against request bodies.
func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return m
}
