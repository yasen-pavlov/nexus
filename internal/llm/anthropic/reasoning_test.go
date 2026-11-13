package anthropic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/llm"
	"go.uber.org/zap"
)

// wire marshals built params the way the SDK sends them, so tests assert on
// what the API receives rather than on SDK struct layout.
func wire(t *testing.T, req llm.GenerateRequest) map[string]any {
	t.Helper()
	params, err := New("k", "", zap.NewNop()).buildParams(req)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	return out
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// Claude models from the 5 generation on think by default and count thinking
// against max_tokens. Checked against the live API on 2026-11-13: at the API
// default Haiku 5.5 spent the rewriter's whole 80-token cap on thinking and
// returned no text, Sonnet 5.5 answers "disabled" with a 400, and every one
// of them rejects temperature.
func TestBuildParams_ReasoningPerModelAndLevel(t *testing.T) {
	temp := float32(0.2)
	cases := []struct {
		name          string
		model         string
		level         llm.Reasoning
		maxTokens     int
		wantMax       float64
		wantEffort    string // "" = no output_config sent
		wantThinking  string // "" = no thinking field sent
		wantSamplings bool   // temperature forwarded
	}{
		{"sonnet 5.5 answer", "claude-sonnet-5-5", llm.ReasoningDefault, 4096, 4096 + answerThinkingReserve, "medium", "", false},
		{"sonnet 5.5 rewrite", "claude-sonnet-5-5", llm.ReasoningMinimal, 80, 80, "low", "between_tools", false},
		{"haiku 5.5 rewrite", "claude-haiku-5-5", llm.ReasoningMinimal, 80, 80, "low", "disabled", false},
		{"haiku 5.5 answer", "claude-haiku-5-5", llm.ReasoningDefault, 4096, 4096 + answerThinkingReserve, "medium", "", false},
		// Always-on models have no off switch: low effort plus room to think.
		{"opus 5.5 title", "claude-opus-5-5", llm.ReasoningMinimal, 24, 24 + minimalThinkingReserve, "low", "", false},
		{"fable 5.2 rewrite", "claude-fable-5-2", llm.ReasoningMinimal, 80, 80 + minimalThinkingReserve, "low", "", false},
		{"fable 5.2 answer", "claude-fable-5-2", llm.ReasoningDefault, 4096, 4096 + answerThinkingReserve, "medium", "", false},
		// A model this table has never heard of follows the current rules.
		{"future model", "claude-sonnet-6", llm.ReasoningMinimal, 80, 80 + minimalThinkingReserve, "low", "", false},
		// 4.x models don't think by default and are sent none of this.
		{"sonnet 4.6 answer", "claude-sonnet-4-6", llm.ReasoningDefault, 4096, 4096, "", "", true},
		{"haiku 4.5 rewrite", "claude-haiku-4-5", llm.ReasoningMinimal, 80, 80, "", "", true},
		{"opus 4.7 answer", "claude-opus-4-7", llm.ReasoningDefault, 4096, 4096, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wire(t, llm.GenerateRequest{
				Model:       tc.model,
				Reasoning:   tc.level,
				MaxTokens:   tc.maxTokens,
				Temperature: &temp,
				Messages:    []llm.Message{{Role: llm.RoleUser, Content: "q"}},
			})
			if got["max_tokens"] != tc.wantMax {
				t.Errorf("max_tokens = %v, want %v", got["max_tokens"], tc.wantMax)
			}
			effort := ""
			if oc, ok := got["output_config"].(map[string]any); ok {
				effort, _ = oc["effort"].(string)
			}
			if effort != tc.wantEffort {
				t.Errorf("effort = %q, want %q", effort, tc.wantEffort)
			}
			thinking := ""
			if th, ok := got["thinking"].(map[string]any); ok {
				thinking, _ = th["type"].(string)
			}
			if thinking != tc.wantThinking {
				t.Errorf("thinking.type = %q, want %q", thinking, tc.wantThinking)
			}
			if _, sent := got["temperature"]; sent != tc.wantSamplings {
				t.Errorf("temperature sent = %v, want %v", sent, tc.wantSamplings)
			}
		})
	}
}

var searchTool = llm.Tool{
	Name:        "nexus_search",
	Description: "search",
	Schema:      map[string]any{"properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}},
}

// The model's thinking is bound to everything sent before it: Fable 5.2
// answers a changed prefix with a 400 ("the block is bound to a different
// conversation"). So round 2 of a tool loop must be round 1 plus new material
// at the end — the documents a tool call fetched go in its tool_result, not in
// front, and forbidding further tool calls must not remove the tools.
func TestBuildParams_ToolRoundIsAppendOnly(t *testing.T) {
	initial := []llm.Document{{ID: "march", Title: "Invoice March", Content: "84.20 EUR"}}
	round1 := llm.GenerateRequest{
		Model: "claude-fable-5-2", System: "sys", EnableCache: true,
		Documents: initial,
		Tools:     []llm.Tool{searchTool},
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "compare february and march"}},
	}
	round2 := round1
	round2.ToolChoice = llm.ToolChoiceNone
	round2.Messages = append([]llm.Message{}, round1.Messages...)
	round2.Messages = append(round2.Messages,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "tu_1", Name: "nexus_search", ArgsJSON: `{"query":"february"}`}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "tu_1", Content: "Found 2 result(s).", Documents: []llm.Document{
			{ID: "feb", Title: "Invoice February", Content: "97.65 EUR"},
			{ID: "march", Title: "Invoice March", Content: "84.20 EUR"}, // already shown
		}},
	)

	w1, w2 := wire(t, round1), wire(t, round2)

	for _, key := range []string{"system", "tools"} {
		if a, b := jsonOf(t, w1[key]), jsonOf(t, w2[key]); a != b {
			t.Errorf("%s changed between rounds:\n round 1: %s\n round 2: %s", key, a, b)
		}
	}
	m1, m2 := w1["messages"].([]any), w2["messages"].([]any)
	if a, b := jsonOf(t, m1[0]), jsonOf(t, m2[0]); a != b {
		t.Errorf("first message changed between rounds:\n round 1: %s\n round 2: %s", a, b)
	}

	// The fetched document is in the tool result, once; the repeat is not.
	toolResult := jsonOf(t, m2[len(m2)-1])
	if !strings.Contains(toolResult, `"tool_result"`) || !strings.Contains(toolResult, `"title":"feb"`) {
		t.Errorf("tool result should carry the fetched document: %s", toolResult)
	}
	if strings.Contains(toolResult, `"title":"march"`) {
		t.Errorf("tool result re-sent a document the model already has: %s", toolResult)
	}
	if !strings.Contains(toolResult, `"citations":{"enabled":true}`) {
		t.Errorf("tool-result documents must be citable like the initial ones: %s", toolResult)
	}

	if choice, _ := w2["tool_choice"].(map[string]any); choice["type"] != "none" {
		t.Errorf("tool_choice = %v, want none with the tools still defined", w2["tool_choice"])
	}
	if _, set := w1["tool_choice"]; set {
		t.Errorf("round 1 should leave tool_choice to the API default, got %v", w1["tool_choice"])
	}
}

// nexus_open_attachment returns a chunk the model already has as text, now
// with its media. Only the media is new, and it must not be sent twice when
// a later call opens the same attachment again.
func TestBuildParams_ToolResultAddsOnlyNewMedia(t *testing.T) {
	photo := llm.Image{MediaType: "image/png", Data: []byte{1, 2, 3}, SourceID: "photo-1"}
	scan := llm.PDF{Data: []byte("%PDF"), Filename: "scan.pdf", SourceID: "scan-1"}
	opened := llm.Document{ID: "chunk", Content: "holiday.png", Images: []llm.Image{photo}, PDFs: []llm.PDF{scan}}

	got := wire(t, llm.GenerateRequest{
		Model:     "claude-sonnet-5-5",
		Documents: []llm.Document{{ID: "chunk", Content: "holiday.png"}},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "what is in the photo?"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "tu_1", Name: "nexus_open_attachment", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "tu_1", Content: "opened", Documents: []llm.Document{opened}},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "tu_2", Name: "nexus_open_attachment", ArgsJSON: `{}`}}},
			{Role: llm.RoleTool, ToolCallID: "tu_2", Content: "opened again", Documents: []llm.Document{opened}},
		},
	})
	msgs := got["messages"].([]any)
	first, second := jsonOf(t, msgs[2]), jsonOf(t, msgs[4])

	if strings.Count(first, `"type":"image"`) != 1 || strings.Count(first, `"media_type":"application/pdf"`) != 1 {
		t.Errorf("first open should attach the image and the PDF once each: %s", first)
	}
	if strings.Contains(first, `"type":"content"`) {
		t.Errorf("the chunk's text is already in the initial documents and must not be re-sent: %s", first)
	}
	if strings.Contains(second, `"type":"image"`) || strings.Contains(second, `"application/pdf"`) {
		t.Errorf("second open re-sent media the model already has: %s", second)
	}
	if strings.Contains(jsonOf(t, msgs[0]), `"type":"image"`) {
		t.Errorf("media a tool call fetched leaked into the first message: %s", jsonOf(t, msgs[0]))
	}
}

func TestBuildParams_ToolChoiceNoneWithoutToolsSendsNothing(t *testing.T) {
	got := wire(t, llm.GenerateRequest{
		Model:      "claude-sonnet-5-5",
		ToolChoice: llm.ToolChoiceNone,
		Messages:   []llm.Message{{Role: llm.RoleUser, Content: "q"}},
	})
	// The API rejects tool_choice on a request that defines no tools.
	if _, set := got["tool_choice"]; set {
		t.Errorf("tool_choice sent without tools: %v", got["tool_choice"])
	}
}

// thinkingToolTurn is a round that thinks, says a line, and calls a tool —
// the block order the live API produced for Sonnet 5.5 and Fable 5.2.
var thinkingToolTurn = []string{
	"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-5-5\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0,\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0}}}\n\n",
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\"}}\n\n",
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"\"}}\n\n",
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-opaque-123\"}}\n\n",
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Let me search.\"}}\n\n",
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"nexus_search\",\"input\":{}}}\n\n",
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"february\\\"}\"}}\n\n",
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\n",
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":1,\"output_tokens\":9,\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0}}\n\n",
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
}

func generateAndDrain(t *testing.T, frames []string, req llm.GenerateRequest) (text string, done llm.Event) {
	t.Helper()
	srv := fakeSSE(t, frames)
	defer srv.Close()
	ch, err := New("test-key", srv.URL, zap.NewNop()).Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for ev := range ch {
		switch ev.Kind {
		case llm.EventText:
			text += ev.TextDelta
		case llm.EventDone:
			done = ev
		case llm.EventError:
			t.Fatalf("stream error: %v", ev.Err)
		}
	}
	return text, done
}

// A turn that stopped for a tool call has to go back exactly as the API
// produced it: the thinking block (an opaque signature with no text) ahead of
// the text and the tool_use it led to. Rebuilding the turn from the text and
// tool calls alone drops the thinking, and the API then answers the tool
// result without the reasoning that asked for it.
func TestGenerate_ToolRoundReplaysThinkingVerbatim(t *testing.T) {
	req := llm.GenerateRequest{
		Model:    "claude-sonnet-5-5",
		Tools:    []llm.Tool{searchTool},
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "compare february and march"}},
	}
	text, done := generateAndDrain(t, thinkingToolTurn, req)
	if text != "Let me search." {
		t.Errorf("thinking must not leak into the answer text; got %q", text)
	}
	if done.StopReason != llm.StopToolUse || done.Replay == nil {
		t.Fatalf("stop = %q, replay = %v; want tool_use with a replay", done.StopReason, done.Replay)
	}

	req.Messages = append(req.Messages,
		llm.Message{Role: llm.RoleAssistant, Content: text, ToolCalls: []llm.ToolCall{{ID: "toolu_1", Name: "nexus_search", ArgsJSON: `{"query":"february"}`}}, Replay: done.Replay},
		llm.Message{Role: llm.RoleTool, ToolCallID: "toolu_1", Content: "Found 1 result(s)."},
	)
	msgs := wire(t, req)["messages"].([]any)
	blocks := msgs[1].(map[string]any)["content"].([]any)

	var types []string
	for _, b := range blocks {
		types = append(types, b.(map[string]any)["type"].(string))
	}
	if strings.Join(types, ",") != "thinking,text,tool_use" {
		t.Fatalf("assistant turn blocks = %v, want thinking,text,tool_use in the original order", types)
	}
	if sig := blocks[0].(map[string]any)["signature"]; sig != "sig-opaque-123" {
		t.Errorf("thinking signature = %v, want it passed back unchanged", sig)
	}
	if input := jsonOf(t, blocks[2].(map[string]any)["input"]); input != `{"query":"february"}` {
		t.Errorf("tool_use input = %s", input)
	}
}

// Replay belongs to the adapter that produced it. A turn replayed from
// history, or one another provider produced, is rebuilt from its text and
// tool calls.
func TestMapMessages_ForeignReplayFallsBackToRebuild(t *testing.T) {
	out := mapMessages(llm.GenerateRequest{Messages: []llm.Message{{
		Role: llm.RoleAssistant, Content: "Let me search.",
		ToolCalls: []llm.ToolCall{{ID: "tc1", Name: "nexus_search", ArgsJSON: `{"query":"x"}`}},
		Replay:    []json.RawMessage{json.RawMessage(`{"type":"reasoning"}`)},
	}}})
	got := jsonOf(t, out[0])
	if !strings.Contains(got, `"text":"Let me search."`) || !strings.Contains(got, `"input":{"query":"x"}`) {
		t.Errorf("foreign replay should rebuild from content + tool calls, got %s", got)
	}
	if strings.Contains(got, "reasoning") {
		t.Errorf("another provider's replay payload leaked into the request: %s", got)
	}
}

func TestGenerate_FinishedAnswerCarriesNoReplay(t *testing.T) {
	frames := []string{
		thinkingToolTurn[0],
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Done.\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	_, done := generateAndDrain(t, frames, llm.GenerateRequest{
		Model:    "claude-sonnet-5-5",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "q"}},
	})
	// A finished answer is stored as text; nothing replays it.
	if done.StopReason != llm.StopEnd || done.Replay != nil {
		t.Errorf("stop = %q, replay = %v; want end_turn without a replay", done.StopReason, done.Replay)
	}
}
