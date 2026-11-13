package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/muty/nexus/internal/llm"
	"go.uber.org/zap"
)

const (
	reasoningItem = `{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"gAAAA-opaque-blob","status":"completed"}`
	preambleItem  = `{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Let me look.","annotations":[]}]}`
	searchCall    = `{"id":"fc_1","type":"function_call","call_id":"call_1","name":"nexus_search","arguments":"{\"query\":\"foo\"}","status":"completed"}`
	openCall      = `{"id":"fc_2","type":"function_call","call_id":"call_2","name":"nexus_open","arguments":"{\"id\":\"d1\"}","status":"completed"}`
)

var searchTool = llm.Tool{
	Name: "nexus_search", Description: "search the corpus",
	Schema: map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
		"required":   []string{"query"},
	},
}

func TestGenerate_RejectsUnusableRequests(t *testing.T) {
	c := New("test-key", "", zap.NewNop())
	cases := map[string]llm.GenerateRequest{
		"no model":    {Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}},
		"no messages": {Model: "gpt-6-luna"},
		"only system": {Model: "gpt-6-luna", Messages: []llm.Message{{Role: llm.RoleSystem, Content: "be brief"}}},
	}
	for name, req := range cases {
		if _, err := c.Generate(context.Background(), req); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestGenerate_StreamsTextStatelessly(t *testing.T) {
	f := newFake(t, []string{
		`{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`,
		textDelta("Hello "),
		textDelta("world."),
		completed(`{"input_tokens":120,"input_tokens_details":{"cached_tokens":100},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":4},"total_tokens":127}`),
	})

	res := generate(t, context.Background(), f.URL, llm.GenerateRequest{
		Model:  "gpt-6-luna",
		System: "You are helpful.",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "duplicate of System"},
			{Role: llm.RoleUser, Content: "first"},
			{Role: llm.RoleAssistant, Content: "answer"},
			{Role: llm.RoleUser, Content: "second"},
		},
	})

	if res.last.Kind != llm.EventDone || res.last.StopReason != llm.StopEnd {
		t.Fatalf("terminal = %+v, want Done/end_turn", res.last)
	}
	if res.text != "Hello world." {
		t.Errorf("text = %q", res.text)
	}
	if want := (llm.Usage{InputTokens: 120, OutputTokens: 7, CacheReadTokens: 100}); res.last.Usage == nil || *res.last.Usage != want {
		t.Errorf("usage = %+v, want %+v", res.last.Usage, want)
	}
	if res.last.Replay != nil {
		t.Errorf("Replay must only be set when stopping for tool use, got %#v", res.last.Replay)
	}

	req := f.request(t, 0)
	if req["store"] != false {
		t.Errorf("store = %v, want false", req["store"])
	}
	if !reflect.DeepEqual(req["include"], []any{"reasoning.encrypted_content"}) {
		t.Errorf("include = %v", req["include"])
	}
	if req["stream"] != true {
		t.Errorf("stream = %v, want true", req["stream"])
	}
	if req["instructions"] != "You are helpful." {
		t.Errorf("instructions = %q", req["instructions"])
	}
	if _, ok := req["tools"]; ok {
		t.Errorf("no tools were requested, got %v", req["tools"])
	}
	want := []map[string]any{
		{"role": "user", "content": "first"},
		{"role": "assistant", "content": "answer"},
		{"role": "user", "content": "second"},
	}
	got := inputItems(t, req)
	for _, it := range got {
		delete(it, "type") // "message" is optional on the wire
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("input = %v\nwant    %v", got, want)
	}
}

// The round trip the Replay contract exists for: the model reasons, says a
// few words, and calls two tools; the next request must carry those output
// items back unchanged, followed by the tool results.
func TestGenerate_ToolRoundTripReplaysOutputItems(t *testing.T) {
	f := newFake(t,
		[]string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[]}}`,
			itemDone(0, reasoningItem),
			textDelta("Let me look."),
			itemDone(1, preambleItem),
			// Argument fragments must not surface as tool-call events.
			`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"{\"query\":"}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"\"foo\"}"}`,
			// Parallel calls may finish out of output order.
			itemDone(3, openCall),
			itemDone(2, searchCall),
			completed(`{"input_tokens":50,"input_tokens_details":{"cached_tokens":0},"output_tokens":30,"output_tokens_details":{"reasoning_tokens":20},"total_tokens":80}`),
		},
		[]string{textDelta("Found it [2]."), completed("")},
	)

	req := llm.GenerateRequest{
		Model:     "gpt-6.1-sol",
		System:    "sys",
		Documents: []llm.Document{{ID: "d1", Title: "Initial", Content: "one"}},
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "find foo"}},
		Tools:     []llm.Tool{searchTool},
	}
	first := generate(t, context.Background(), f.URL, req)

	if first.last.StopReason != llm.StopToolUse {
		t.Fatalf("terminal = %+v, want Done/tool_use", first.last)
	}
	wantCalls := []llm.ToolCallDelta{
		{ID: "call_2", Name: "nexus_open", ArgsJSON: `{"id":"d1"}`, Final: true},
		{ID: "call_1", Name: "nexus_search", ArgsJSON: `{"query":"foo"}`, Final: true},
	}
	if !reflect.DeepEqual(first.calls, wantCalls) {
		t.Fatalf("tool calls = %+v\nwant         %+v", first.calls, wantCalls)
	}
	if first.last.Replay == nil {
		t.Fatal("a tool_use stop must carry a Replay")
	}

	// Second round, as the orchestrator builds it.
	req.Messages = append(req.Messages,
		llm.Message{
			Role: llm.RoleAssistant, Content: first.text, Replay: first.last.Replay,
			ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "nexus_search", ArgsJSON: `{"query":"foo"}`}, {ID: "call_2", Name: "nexus_open", ArgsJSON: `{"id":"d1"}`}},
		},
		llm.Message{Role: llm.RoleTool, ToolCallID: "call_1", Content: "1 hit", Documents: []llm.Document{{ID: "d2", Title: "From tool", Content: "two"}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "call_2", Content: "opened", Documents: []llm.Document{{ID: "d1", Title: "Initial", Content: "one"}}},
	)
	second := generate(t, context.Background(), f.URL, req)
	if second.text != "Found it [2]." || second.last.StopReason != llm.StopEnd {
		t.Fatalf("second round = %q / %+v", second.text, second.last)
	}

	body := f.request(t, 1)
	want := []map[string]any{
		{"role": "user", "content": "find foo"},
		decode(t, reasoningItem),
		decode(t, preambleItem),
		decode(t, searchCall),
		decode(t, openCall),
		{"type": "function_call_output", "call_id": "call_1", "output": "1 hit"},
		{"type": "function_call_output", "call_id": "call_2", "output": "opened"},
	}
	got := inputItems(t, body)
	delete(got[0], "type")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("second request input:\n got  %v\n want %v", got, want)
	}

	// Tool-turn documents extend the numbered block after the initial ones;
	// a document the tool returned again keeps its original number.
	instr, _ := body["instructions"].(string)
	if !strings.HasPrefix(instr, "sys\n\n") {
		t.Errorf("instructions must start with the system prompt: %q", instr)
	}
	i1 := strings.Index(instr, `<document index="1" id="d1"`)
	i2 := strings.Index(instr, `<document index="2" id="d2"`)
	if i1 < 0 || i2 < i1 || strings.Count(instr, "<document ") != 2 {
		t.Errorf("documents block not numbered d1=1, d2=2:\n%s", instr)
	}
	if firstInstr, _ := f.request(t, 0)["instructions"].(string); strings.Contains(firstInstr, `id="d2"`) {
		t.Errorf("first round must only list the initial documents:\n%s", firstInstr)
	}
}

// History loaded from the database (or produced by another provider) has no
// usable Replay; the turn is rebuilt so every tool result still has its call.
func TestGenerate_RebuildsAssistantTurnWithoutReplay(t *testing.T) {
	for name, replay := range map[string]any{"nil": nil, "foreign type": []string{"anthropic-block"}} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, []string{completed("")})
			generate(t, context.Background(), f.URL, llm.GenerateRequest{
				Model: "gpt-6-luna",
				Messages: []llm.Message{
					{Role: llm.RoleUser, Content: "find foo"},
					{Role: llm.RoleAssistant, Content: "Looking.", Replay: replay, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "nexus_search", ArgsJSON: `{"query":"foo"}`}}},
					{Role: llm.RoleTool, ToolCallID: "call_1", Content: "1 hit"},
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_2", Name: "nexus_search", ArgsJSON: `{}`}}},
					{Role: llm.RoleTool, ToolCallID: "call_2", Content: "none"},
				},
			})
			got := inputItems(t, f.request(t, 0))
			for _, it := range got {
				if _, isMsg := it["role"]; isMsg {
					delete(it, "type")
				}
			}
			want := []map[string]any{
				{"role": "user", "content": "find foo"},
				{"role": "assistant", "content": "Looking."},
				{"type": "function_call", "call_id": "call_1", "name": "nexus_search", "arguments": `{"query":"foo"}`},
				{"type": "function_call_output", "call_id": "call_1", "output": "1 hit"},
				{"type": "function_call", "call_id": "call_2", "name": "nexus_search", "arguments": `{}`},
				{"type": "function_call_output", "call_id": "call_2", "output": "none"},
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("input:\n got  %v\n want %v", got, want)
			}
		})
	}
}

func TestGenerate_ToolChoice(t *testing.T) {
	cases := []struct {
		name       string
		tools      []llm.Tool
		choice     llm.ToolChoice
		wantTools  int
		wantChoice any // nil = key absent
	}{
		{"auto leaves the choice to the model", []llm.Tool{searchTool, {Name: "nexus_now"}}, llm.ToolChoiceAuto, 2, nil},
		{"none keeps the definitions", []llm.Tool{searchTool, {Name: "nexus_now"}}, llm.ToolChoiceNone, 2, "none"},
		{"none without tools sends neither", nil, llm.ToolChoiceNone, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, []string{completed("")})
			req := userReq("gpt-6-luna")
			req.Tools, req.ToolChoice = tc.tools, tc.choice
			generate(t, context.Background(), f.URL, req)

			body := f.request(t, 0)
			tools, _ := body["tools"].([]any)
			if len(tools) != tc.wantTools {
				t.Fatalf("tools = %v, want %d", body["tools"], tc.wantTools)
			}
			if got := body["tool_choice"]; got != tc.wantChoice {
				t.Errorf("tool_choice = %v, want %v", got, tc.wantChoice)
			}
			if tc.wantTools == 0 {
				return
			}
			wantFirst := map[string]any{
				"type": "function", "name": "nexus_search", "description": "search the corpus", "strict": false,
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"query": map[string]any{"type": "string"}},
					"required":   []any{"query"},
				},
			}
			if !reflect.DeepEqual(tools[0], wantFirst) {
				t.Errorf("tool[0] = %v\nwant      %v", tools[0], wantFirst)
			}
			// A schema-less tool still needs a parameters object.
			if params, _ := tools[1].(map[string]any)["parameters"].(map[string]any); params["type"] != "object" {
				t.Errorf("tool[1] parameters = %v", tools[1])
			}
		})
	}
}

func TestGenerate_ReasoningAndTokenBudgetPerModel(t *testing.T) {
	temp := float32(0.2)
	cases := []struct {
		model      string
		reasoning  llm.Reasoning
		maxTokens  int
		wantEffort string // "" = no reasoning settings at all
		wantMax    float64
		wantTemp   bool
	}{
		// Non-reasoning models: no settings, no headroom, temperature allowed.
		{"gpt-4.1", llm.ReasoningDefault, 1000, "", 1000, true},
		{"gpt-4.1-mini", llm.ReasoningMinimal, 80, "", 80, true},
		{"gpt-4o-mini", llm.ReasoningMinimal, 24, "", 24, true},
		{"gpt-4o", llm.ReasoningDefault, 0, "", 4096, true},
		// Default level: provider default effort, room to think.
		{"gpt-6-astra", llm.ReasoningDefault, 2000, "", 2000 + 4096, false},
		{"gpt-6-luna", llm.ReasoningDefault, 0, "", 4096 + 4096, false},
		{"gpt-5-mini", llm.ReasoningDefault, 1000, "", 1000 + 4096, false},
		// Minimal: the lowest effort each model accepts.
		{"gpt-6.1-terra", llm.ReasoningMinimal, 80, "none", 80, false},
		{"gpt-6-luna", llm.ReasoningMinimal, 24, "none", 24, false},
		{"gpt-6-sol", llm.ReasoningMinimal, 80, "none", 80, false},
		{"gpt-6-astra", llm.ReasoningMinimal, 80, "low", 80 + 1024, false},
		{"gpt-6.1-sol", llm.ReasoningMinimal, 24, "low", 24 + 1024, false},
		{"gpt-5", llm.ReasoningMinimal, 80, "minimal", 80 + 1024, false},
		{"gpt-5-mini", llm.ReasoningMinimal, 80, "minimal", 80 + 1024, false},
		{"gpt-5-nano", llm.ReasoningMinimal, 80, "minimal", 80 + 1024, false},
		{"gpt-5-mini-2025-08-07", llm.ReasoningMinimal, 80, "minimal", 80 + 1024, false},
		// Not in the table: assume a reasoning model, don't guess an effort.
		{"gpt-5.1", llm.ReasoningMinimal, 80, "", 80 + 1024, false},
		{"gpt-7-nova", llm.ReasoningMinimal, 80, "", 80 + 1024, false},
		{"gpt-7-nova", llm.ReasoningDefault, 500, "", 500 + 4096, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/%s/%d", tc.model, tc.reasoning, tc.maxTokens), func(t *testing.T) {
			f := newFake(t, []string{completed("")})
			req := userReq(tc.model)
			req.Reasoning, req.MaxTokens, req.Temperature = tc.reasoning, tc.maxTokens, &temp
			generate(t, context.Background(), f.URL, req)

			body := f.request(t, 0)
			if body["model"] != tc.model {
				t.Errorf("model = %v", body["model"])
			}
			if tc.wantEffort == "" {
				if r, ok := body["reasoning"]; ok {
					t.Errorf("reasoning = %v, want none sent", r)
				}
			} else if !reflect.DeepEqual(body["reasoning"], map[string]any{"effort": tc.wantEffort}) {
				t.Errorf("reasoning = %v, want effort %q", body["reasoning"], tc.wantEffort)
			}
			if body["max_output_tokens"] != tc.wantMax {
				t.Errorf("max_output_tokens = %v, want %v", body["max_output_tokens"], tc.wantMax)
			}
			if _, got := body["temperature"]; got != tc.wantTemp {
				t.Errorf("temperature sent = %v, want %v", got, tc.wantTemp)
			}
		})
	}
}

func TestGenerate_OmitsUnsetTemperature(t *testing.T) {
	f := newFake(t, []string{completed("")})
	generate(t, context.Background(), f.URL, userReq("gpt-4.1"))
	if v, ok := f.request(t, 0)["temperature"]; ok {
		t.Errorf("temperature = %v, want omitted", v)
	}
}

func TestGenerate_StopReasons(t *testing.T) {
	const usage = `"usage":{"input_tokens":9,"input_tokens_details":{"cached_tokens":0},"output_tokens":16,"output_tokens_details":{"reasoning_tokens":16},"total_tokens":25}`
	cases := []struct {
		name      string
		events    []string
		want      llm.StopReason
		wantUsage bool
		wantCalls int
	}{
		{"completed", []string{textDelta("ok"), completed("")}, llm.StopEnd, false, 0},
		{
			"out of output tokens",
			[]string{`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},` + usage + `}}`},
			llm.StopMaxTokens, true, 0,
		},
		{
			"content filter",
			[]string{textDelta("I"), `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"content_filter"}}}`},
			llm.StopFiltered, false, 0,
		},
		{
			"incomplete for another reason",
			[]string{`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_messages"}}}`},
			llm.StopEnd, false, 0,
		},
		{"function call", []string{itemDone(0, searchCall), completed("")}, llm.StopToolUse, false, 1},
		{
			// A call cut off by the token limit has unusable arguments.
			"truncated function call is not a tool call",
			[]string{
				itemDone(0, `{"id":"fc_1","type":"function_call","call_id":"call_1","name":"nexus_search","arguments":"{\"que","status":"incomplete"}`),
				`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`,
			},
			llm.StopMaxTokens, false, 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, tc.events)
			res := generate(t, context.Background(), f.URL, userReq("gpt-6-luna"))
			if res.last.Kind != llm.EventDone || res.last.StopReason != tc.want {
				t.Fatalf("terminal = %+v, want Done/%s", res.last, tc.want)
			}
			if (res.last.Usage != nil) != tc.wantUsage {
				t.Errorf("usage = %+v, wantUsage %v", res.last.Usage, tc.wantUsage)
			}
			if tc.wantUsage && res.last.Usage.OutputTokens != 16 {
				t.Errorf("output tokens = %d, want 16 (reasoning included)", res.last.Usage.OutputTokens)
			}
			if len(res.calls) != tc.wantCalls {
				t.Errorf("tool calls = %+v, want %d", res.calls, tc.wantCalls)
			}
			if (res.last.Replay != nil) != (tc.want == llm.StopToolUse) {
				t.Errorf("Replay = %#v for stop %s", res.last.Replay, tc.want)
			}
		})
	}
}

func TestGenerate_Failures(t *testing.T) {
	cases := []struct {
		name     string
		events   []string
		wantText string // text streamed before the failure still arrives
		wantErr  string
	}{
		{
			"failed response",
			[]string{textDelta("par"), `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"model overloaded"}}}`},
			"par", "openai: response failed: server_error: model overloaded",
		},
		{
			"failed response without details",
			[]string{`{"type":"response.failed","response":{"status":"failed"}}`},
			"", "openai: response failed",
		},
		{
			"error event",
			[]string{`{"type":"error","code":"rate_limit_exceeded","message":"slow down","param":null}`},
			"", "openai: stream: rate_limit_exceeded: slow down",
		},
		{
			// A dropped connection must not pass for a finished answer.
			"stream ends without a terminal event",
			[]string{textDelta("half an ans")},
			"half an ans", "openai: stream: ended before the response completed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, tc.events)
			res := generate(t, context.Background(), f.URL, userReq("gpt-6-luna"))
			if res.last.Kind != llm.EventError {
				t.Fatalf("terminal = %+v, want EventError", res.last)
			}
			if res.last.Err.Error() != tc.wantErr {
				t.Errorf("err = %q, want %q", res.last.Err, tc.wantErr)
			}
			if res.text != tc.wantText {
				t.Errorf("text = %q, want %q", res.text, tc.wantText)
			}
		})
	}
}

func TestGenerate_APIErrorBecomesEventError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Unsupported value: 'none'","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	res := generate(t, context.Background(), srv.URL, userReq("gpt-6-astra"))
	if res.last.Kind != llm.EventError {
		t.Fatalf("terminal = %+v, want EventError", res.last)
	}
	msg := res.last.Err.Error()
	if !strings.HasPrefix(msg, "openai: ") || !strings.Contains(msg, "Unsupported value") {
		t.Errorf("err = %q, want an openai:-wrapped error carrying the API message", msg)
	}
}

// hangingServer streams one text delta and then stalls until the client
// goes away.
func hangingServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, sse(textDelta("thinking")))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGenerate_CancellationEndsWithCancelledDone(t *testing.T) {
	t.Run("caller cancels mid-stream", func(t *testing.T) {
		srv := hangingServer(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ch, err := New("test-key", srv.URL, zap.NewNop()).Generate(ctx, userReq("gpt-6-luna"))
		if err != nil {
			t.Fatal(err)
		}
		if ev := <-ch; ev.Kind != llm.EventText || ev.TextDelta != "thinking" {
			t.Fatalf("first event = %+v", ev)
		}
		cancel()
		res := drain(t, ch)
		if res.last.Kind != llm.EventDone || res.last.StopReason != llm.StopCancelled {
			t.Errorf("terminal = %+v, want Done/cancelled", res.last)
		}
	})

	t.Run("deadline passes mid-stream", func(t *testing.T) {
		srv := hangingServer(t)
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		res := generate(t, ctx, srv.URL, userReq("gpt-6-luna"))
		if res.last.Kind != llm.EventDone || res.last.StopReason != llm.StopCancelled {
			t.Errorf("terminal = %+v, want Done/cancelled", res.last)
		}
	})
}

// Media from the initial documents and from tool-turn documents rides on the
// last user message, after its text, images before PDFs.
func TestGenerate_AttachesMediaToLastUserMessage(t *testing.T) {
	png, jpg, pdf := []byte("png-bytes"), []byte("jpg-bytes"), []byte("%PDF-1.7")
	f := newFake(t, []string{completed("")})
	generate(t, context.Background(), f.URL, llm.GenerateRequest{
		Model: "gpt-6.1-terra",
		Documents: []llm.Document{{
			ID: "d1", Content: "scan",
			Images: []llm.Image{{MediaType: "image/png", Data: png, SourceID: "i1"}},
			PDFs:   []llm.PDF{{Data: pdf, SourceID: "p1"}},
		}},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "earlier question"},
			{Role: llm.RoleAssistant, Content: "earlier answer"},
			{Role: llm.RoleUser, Content: "what is in the scan?"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "nexus_open_attachment", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "call_1", Content: "attached", Documents: []llm.Document{{
				ID:     "d2",
				Images: []llm.Image{{MediaType: "image/jpeg", Data: jpg, SourceID: "i2"}},
				PDFs:   []llm.PDF{{Data: pdf, Filename: "report.pdf", SourceID: "p2"}},
			}}},
		},
	})

	items := inputItems(t, f.request(t, 0))
	if items[0]["content"] != "earlier question" {
		t.Errorf("earlier user turn must stay plain text, got %v", items[0]["content"])
	}
	b64 := base64.StdEncoding.EncodeToString
	want := []any{
		map[string]any{"type": "input_text", "text": "what is in the scan?"},
		map[string]any{"type": "input_image", "detail": "auto", "image_url": "data:image/png;base64," + b64(png)},
		map[string]any{"type": "input_image", "detail": "auto", "image_url": "data:image/jpeg;base64," + b64(jpg)},
		map[string]any{"type": "input_file", "filename": "document.pdf", "file_data": "data:application/pdf;base64," + b64(pdf)},
		map[string]any{"type": "input_file", "filename": "report.pdf", "file_data": "data:application/pdf;base64," + b64(pdf)},
	}
	if !reflect.DeepEqual(items[2]["content"], want) {
		t.Errorf("last user content:\n got  %v\n want %v", items[2]["content"], want)
	}
}
