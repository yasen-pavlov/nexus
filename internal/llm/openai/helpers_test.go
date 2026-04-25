package openai

import (
	"strings"
	"testing"

	"github.com/muty/nexus/internal/llm"
)

func TestEscapeAttr_EscapesSpecialChars(t *testing.T) {
	cases := map[string]string{
		`a"b`:   `a&quot;b`,
		`a<b>c`: `a&lt;b&gt;c`,
		`a&b`:   `a&amp;b`,
		`plain`: `plain`,
		`"<&>"`: `&quot;&lt;&amp;&gt;&quot;`,
	}
	for in, want := range cases {
		if got := escapeAttr(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestMapFinishReason_CoversAllVariants(t *testing.T) {
	cases := map[string]llm.StopReason{
		"stop":           llm.StopEnd,
		"tool_calls":     llm.StopToolUse,
		"length":         llm.StopMaxTokens,
		"content_filter": llm.StopFiltered,
		"weird":          llm.StopEnd,
	}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestImageContentBlock_BuildsDataURI(t *testing.T) {
	img := llm.Image{MediaType: "image/png", Data: []byte("abc")}
	block := ImageContentBlock(img)
	if block.OfImageURL == nil {
		t.Fatal("expected image URL block")
	}
	if !strings.HasPrefix(block.OfImageURL.ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("unexpected data URI: %q", block.OfImageURL.ImageURL.URL)
	}
}

func TestBuildParams_AppliesTemperatureAndDefaultMaxTokens(t *testing.T) {
	c := New("k", "", nil)
	temp := float32(0.7)
	params, err := c.buildParams(llm.GenerateRequest{
		Model:       "gpt-x",
		System:      "you are helpful",
		Documents:   []llm.Document{{ID: "d1", Content: "x"}},
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Temperature: &temp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !params.MaxCompletionTokens.Valid() {
		t.Error("expected MaxCompletionTokens default to be set")
	}
	if !params.Temperature.Valid() {
		t.Error("expected temperature to be set")
	}
}

func TestBuildParams_RequiresMessages(t *testing.T) {
	c := New("k", "", nil)
	if _, err := c.buildParams(llm.GenerateRequest{Model: "gpt-x"}); err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestBuildParams_HandlesAllRoles(t *testing.T) {
	c := New("k", "", nil)
	params, err := c.buildParams(llm.GenerateRequest{
		Model: "gpt-x",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "ignored"},
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleAssistant, Content: "ans"},
			{Role: llm.RoleTool, Content: "result", ToolCallID: "tc1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(params.Messages) < 3 {
		t.Errorf("expected at least 3 messages, got %d", len(params.Messages))
	}
}
