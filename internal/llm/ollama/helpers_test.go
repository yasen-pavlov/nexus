package ollama

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/llm"
)

func TestBuildDocumentsBlock_FormatsAsXML(t *testing.T) {
	got := buildDocumentsBlock([]llm.Document{
		{ID: "chunk-1", Source: "imap", Title: "subj", Date: "2026-01-02", Content: "body"},
	})
	if !strings.Contains(got, `<document index="1" id="chunk-1"`) {
		t.Errorf("missing index attr: %s", got)
	}
	for _, want := range []string{`source="imap"`, `title="subj"`, `date="2026-01-02"`, "body", "</document>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestMapDoneReason_CoversAllVariants(t *testing.T) {
	cases := map[string]llm.StopReason{
		"stop":       llm.StopEnd,
		"length":     llm.StopMaxTokens,
		"tool_calls": llm.StopToolUse,
		"weird":      llm.StopEnd,
	}
	for in, want := range cases {
		if got := mapDoneReason(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestEncodeImage_ProducesBase64(t *testing.T) {
	got := EncodeImage(llm.Image{MediaType: "image/png", Data: []byte("abc")})
	if got == "" {
		t.Fatal("empty encoding")
	}
	// Base64 of "abc" → "YWJj".
	if got != "YWJj" {
		t.Errorf("got %q", got)
	}
}

func TestBuildBody_AssemblesSystemAndDocs(t *testing.T) {
	temp := float32(0.4)
	body, err := buildBody(llm.GenerateRequest{
		Model:       "qwen3:14b",
		System:      "be helpful",
		Documents:   []llm.Document{{ID: "d1", Content: "x"}},
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Tools:       []llm.Tool{{Name: "search", Description: "s", Schema: map[string]any{"type": "object"}}},
		MaxTokens:   1024,
		Temperature: &temp,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	var got chatRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Stream != true {
		t.Error("expected stream=true")
	}
	if len(got.Messages) < 2 {
		t.Errorf("expected system + user message; got %d", len(got.Messages))
	}
	if got.Messages[0].Role != "system" || !strings.Contains(got.Messages[0].Content, "be helpful") {
		t.Errorf("system message wrong: %+v", got.Messages[0])
	}
	if !strings.Contains(got.Messages[0].Content, `<document index="1" id="d1"`) {
		t.Error("expected docs block in system message")
	}
	if len(got.Tools) != 1 {
		t.Errorf("tools = %d", len(got.Tools))
	}
	gotTemp, _ := got.Options["temperature"].(float64)
	if gotTemp < 0.39 || gotTemp > 0.41 {
		t.Errorf("temperature = %v", gotTemp)
	}
	if got.Options["num_predict"] != float64(1024) {
		t.Errorf("num_predict = %v", got.Options["num_predict"])
	}
}

func TestBuildBody_HandlesAllRoles(t *testing.T) {
	body, err := buildBody(llm.GenerateRequest{
		Model: "qwen3:14b",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleAssistant, Content: "ans", ToolCalls: []llm.ToolCall{{ID: "tc1", Name: "search", ArgsJSON: `{"q":"x"}`}}},
			{Role: llm.RoleTool, Content: "result", ToolCallID: "tc1"},
			{Role: llm.RoleSystem, Content: "ignored"},
		},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	var got chatRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	roles := make([]string, 0, len(got.Messages))
	for _, m := range got.Messages {
		roles = append(roles, m.Role)
	}
	// system role from messages array is dropped (system goes via .System
	// field on the request); user/assistant/tool all flow through.
	if len(got.Messages) != 3 {
		t.Errorf("expected 3 mapped messages, got %d (%v)", len(got.Messages), roles)
	}
	for _, m := range got.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) != 1 {
			t.Error("assistant tool calls not mapped")
		}
	}
}

func TestBuildBody_EmptyMessages(t *testing.T) {
	if _, err := buildBody(llm.GenerateRequest{Model: "x"}, true); err == nil {
		t.Fatal("expected error")
	}
}
