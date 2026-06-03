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

func TestPDFContentBlock_BuildsFileDataURI(t *testing.T) {
	block := PDFContentBlock(llm.PDF{Data: []byte("%PDF-1.7"), Filename: "report.pdf"})
	if block.OfFile == nil {
		t.Fatal("expected file content part")
	}
	if !strings.HasPrefix(block.OfFile.File.FileData.Value, "data:application/pdf;base64,") {
		t.Errorf("unexpected file_data: %q", block.OfFile.File.FileData.Value)
	}
	if block.OfFile.File.Filename.Value != "report.pdf" {
		t.Errorf("filename = %q, want report.pdf", block.OfFile.File.Filename.Value)
	}
}

func TestBuildParams_AttachesPDFToUserMessage(t *testing.T) {
	c := New("k", "", nil)
	params, err := c.buildParams(llm.GenerateRequest{
		Model:     "gpt-4.1",
		Documents: []llm.Document{{ID: "d1", Content: "x", PDFs: []llm.PDF{{Data: []byte("%PDF"), Filename: "f.pdf"}}}},
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "what's in the file?"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawFile bool
	for _, m := range params.Messages {
		if m.OfUser == nil {
			continue
		}
		for _, part := range m.OfUser.Content.OfArrayOfContentParts {
			if part.OfFile != nil {
				sawFile = true
			}
		}
	}
	if !sawFile {
		t.Error("expected a file content part on the user message")
	}
}

func TestBuildParams_AssistantEmitsToolCalls(t *testing.T) {
	c := New("k", "", nil)
	params, err := c.buildParams(llm.GenerateRequest{
		Model: "gpt-4.1",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "find X"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "nexus_search", ArgsJSON: `{"query":"x"}`},
			}},
			{Role: llm.RoleTool, Content: "results", ToolCallID: "tc1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The assistant message MUST re-emit tool_calls; otherwise OpenAI 400s
	// the following tool message ("must be a response to a preceding
	// message with 'tool_calls'").
	var found bool
	for _, m := range params.Messages {
		if m.OfAssistant == nil || len(m.OfAssistant.ToolCalls) != 1 {
			continue
		}
		fn := m.OfAssistant.ToolCalls[0].OfFunction
		if fn != nil && fn.ID == "tc1" && fn.Function.Name == "nexus_search" {
			found = true
		}
	}
	if !found {
		t.Error("assistant message must carry tool_calls so the tool result has a referent")
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
