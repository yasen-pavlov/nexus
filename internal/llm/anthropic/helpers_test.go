package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/muty/nexus/internal/llm"
)

func TestMetaLine_AssemblesParts(t *testing.T) {
	if got := metaLine(llm.Document{Source: "imap", Title: "subj", Date: "2026-01-02"}); got != "source=imap title=subj date=2026-01-02" {
		t.Errorf("got %q", got)
	}
	if got := metaLine(llm.Document{Source: "imap"}); got != "source=imap" {
		t.Errorf("source-only got %q", got)
	}
	if got := metaLine(llm.Document{}); got != "" {
		t.Errorf("empty got %q", got)
	}
}

func TestDocToBlock_AddsCachingAndContext(t *testing.T) {
	block := docToBlock(llm.Document{
		ID: "chunk-1", Title: "S", Source: "imap", Date: "2026-01-02", Content: "body",
	}, true)
	if block.OfDocument == nil {
		t.Fatal("expected document content block")
	}
	if string(block.OfDocument.CacheControl.Type) == "" {
		t.Error("expected cache_control to be set when EnableCache is true")
	}
	if got := block.OfDocument.Title; got.Value != "chunk-1" {
		t.Errorf("title = %v, want chunk-1", got)
	}

	// EnableCache = false drops the breakpoint.
	plain := docToBlock(llm.Document{ID: "chunk-2", Content: "x"}, false)
	if string(plain.OfDocument.CacheControl.Type) != "" {
		t.Error("expected no cache_control when EnableCache is false")
	}
}

func TestMapMessages_HandlesAllRoles(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleSystem, Content: "skipped"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi", ToolCalls: []llm.ToolCall{{ID: "tc1", Name: "search", ArgsJSON: `{"q":"x"}`}}},
		{Role: llm.RoleTool, Content: "result", ToolCallID: "tc1"},
	}
	out := mapMessages(in)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages (system dropped), got %d", len(out))
	}
	if out[0].Role != sdk.MessageParamRoleUser {
		t.Errorf("first = %s", out[0].Role)
	}
	if out[1].Role != sdk.MessageParamRoleAssistant {
		t.Errorf("second = %s", out[1].Role)
	}
	if out[2].Role != sdk.MessageParamRoleUser {
		t.Errorf("tool turn should map to user, got %s", out[2].Role)
	}
}

// Regression: tool_use.input must json.Marshal as a dictionary, not a
// base64-encoded string. The Anthropic API rejects ([]byte) encoded
// inputs with "messages.N.content.0.tool_use.input: Input should be a
// valid dictionary" — wire failure mode is silent until round 2 of any
// tool-use turn. mapMessages must therefore wrap ArgsJSON in
// json.RawMessage (not []byte) when constructing the tool_use block.
func TestMapMessages_ToolUseInputEncodesAsDictionary(t *testing.T) {
	out := mapMessages([]llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "tc1", Name: "search", ArgsJSON: `{"q":"x","n":3}`,
		}}},
	})
	if len(out) != 1 {
		t.Fatalf("got %d messages, want 1", len(out))
	}
	encoded, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The JSON-encoded message must contain the input as a real object,
	// preserving the keys. A base64 string would look like
	// "input":"eyJxIjoieCJ9" and FAIL the substring check below.
	if !strings.Contains(string(encoded), `"input":{"q":"x","n":3}`) {
		t.Errorf("tool_use.input did not encode as a dictionary; got: %s", encoded)
	}
}

// Empty ArgsJSON must collapse to "{}" so the tool_use.input remains a
// valid (empty) dictionary — Anthropic still requires the field shape.
func TestMapMessages_ToolUseInputEmptyArgsBecomesEmptyDict(t *testing.T) {
	out := mapMessages([]llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "tc1", Name: "search", ArgsJSON: ""}}},
	})
	encoded, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"input":{}`) {
		t.Errorf("empty ArgsJSON should produce empty dict; got: %s", encoded)
	}
}

func TestPrependDocs_InsertsIntoFirstUserMessage(t *testing.T) {
	docs := []sdk.ContentBlockParamUnion{sdk.NewTextBlock("DOC")}
	msgs := []sdk.MessageParam{
		sdk.NewAssistantMessage(sdk.NewTextBlock("a")),
		sdk.NewUserMessage(sdk.NewTextBlock("u")),
	}
	out := prependDocs(msgs, docs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if out[1].Role != sdk.MessageParamRoleUser {
		t.Fatalf("user turn should be at index 1, got role %s", out[1].Role)
	}
	if len(out[1].Content) != 2 {
		t.Errorf("expected docs prepended into user content, got %d blocks", len(out[1].Content))
	}
}

func TestPrependDocs_EmptyMessages(t *testing.T) {
	out := prependDocs(nil, []sdk.ContentBlockParamUnion{sdk.NewTextBlock("doc")})
	if len(out) != 1 {
		t.Fatalf("expected 1 user message, got %d", len(out))
	}
	if out[0].Role != sdk.MessageParamRoleUser {
		t.Errorf("role = %s", out[0].Role)
	}
}

func TestMapStopReason_CoversAllVariants(t *testing.T) {
	cases := map[sdk.StopReason]llm.StopReason{
		sdk.StopReasonEndTurn:   llm.StopEnd,
		sdk.StopReasonToolUse:   llm.StopToolUse,
		sdk.StopReasonMaxTokens: llm.StopMaxTokens,
		sdk.StopReasonRefusal:   llm.StopFiltered,
		"weird-thing":           llm.StopEnd,
	}
	for in, want := range cases {
		if got := mapStopReason(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestSchemaProperties_HandlesNilAndShapes(t *testing.T) {
	if schemaProperties(nil) != nil {
		t.Error("nil schema should return nil")
	}
	props := map[string]any{"type": "string"}
	full := map[string]any{"properties": props}
	if got := schemaProperties(full); got == nil {
		t.Error("expected unwrapped properties")
	}
	flat := map[string]any{"type": "string"}
	if got := schemaProperties(flat); got == nil {
		t.Error("flat schema should pass through")
	}
}

func TestSchemaRequired_HandlesShapes(t *testing.T) {
	if r := schemaRequired(nil); r != nil {
		t.Error("nil should be nil")
	}
	if r := schemaRequired(map[string]any{"required": []string{"q"}}); len(r) != 1 || r[0] != "q" {
		t.Errorf("[]string shape: %v", r)
	}
	if r := schemaRequired(map[string]any{"required": []any{"a", "b"}}); len(r) != 2 {
		t.Errorf("[]any shape: %v", r)
	}
	if r := schemaRequired(map[string]any{}); r != nil {
		t.Errorf("missing key: %v", r)
	}
}

func TestImageContentBlock_BuildsBase64Image(t *testing.T) {
	img := llm.Image{MediaType: "image/png", Data: []byte{1, 2, 3}}
	block := ImageContentBlock(img)
	if block.OfImage == nil {
		t.Fatal("expected image content block")
	}
	if block.OfImage.Source.OfBase64 == nil {
		t.Fatal("expected base64 image source")
	}
}

func TestBuildParams_AssemblesSystemAndDocuments(t *testing.T) {
	c := New("test-key", "", nil)
	temp := float32(0.3)
	params, err := c.buildParams(llm.GenerateRequest{
		Model:       "claude-x",
		System:      "be helpful",
		Documents:   []llm.Document{{ID: "d1", Content: "x"}},
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		MaxTokens:   1024,
		Temperature: &temp,
		EnableCache: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if params.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d", params.MaxTokens)
	}
	if len(params.System) != 1 {
		t.Errorf("system blocks: %d", len(params.System))
	}
	if string(params.System[0].CacheControl.Type) == "" {
		t.Error("expected cache_control on system when EnableCache is true")
	}
	// Documents prepended into the user message → first message has > 1 block.
	if len(params.Messages) == 0 || len(params.Messages[0].Content) < 2 {
		t.Errorf("expected docs prepended; messages=%d blocks=%d", len(params.Messages), len(params.Messages[0].Content))
	}
}

func TestBuildParams_OnlyLastDocumentHasCacheControl(t *testing.T) {
	// Anthropic caps requests at 4 cache_control breakpoints. With many
	// retrieved documents we'd exceed that if every doc carried a
	// breakpoint — only the last document should be marked, acting as
	// the cacheable-prefix boundary.
	c := New("test-key", "", nil)
	docs := []llm.Document{
		{ID: "d1", Content: "a"},
		{ID: "d2", Content: "b"},
		{ID: "d3", Content: "c"},
		{ID: "d4", Content: "d"},
		{ID: "d5", Content: "e"},
	}
	params, err := c.buildParams(llm.GenerateRequest{
		Model:       "claude-x",
		System:      "sys",
		Documents:   docs,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "q"}},
		EnableCache: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// First message has [doc1, doc2, ..., docN, "q"]. Iterate the
	// document blocks (everything except the trailing user text).
	blocks := params.Messages[0].Content
	docCount := 0
	cached := 0
	var lastCachedIdx int
	for i, b := range blocks {
		if b.OfDocument == nil {
			continue
		}
		docCount++
		if string(b.OfDocument.CacheControl.Type) != "" {
			cached++
			lastCachedIdx = i
		}
	}
	if docCount != 5 {
		t.Fatalf("doc blocks = %d want 5", docCount)
	}
	if cached != 1 {
		t.Errorf("cache_control breakpoints on docs = %d want exactly 1", cached)
	}
	if lastCachedIdx != docCount-1 {
		t.Errorf("cache_control on doc index %d, want last (%d)", lastCachedIdx, docCount-1)
	}
}

func TestBuildParams_DefaultMaxTokens(t *testing.T) {
	c := New("test-key", "", nil)
	params, err := c.buildParams(llm.GenerateRequest{
		Model:    "claude-x",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if params.MaxTokens != defaultMaxTokens {
		t.Errorf("max_tokens default = %d", params.MaxTokens)
	}
}

func TestBuildParams_RequiresMessages(t *testing.T) {
	c := New("test-key", "", nil)
	if _, err := c.buildParams(llm.GenerateRequest{Model: "claude-x"}); err == nil {
		t.Fatal("expected error for empty request")
	}
}

func TestBuildParams_PassesTools(t *testing.T) {
	c := New("test-key", "", nil)
	params, err := c.buildParams(llm.GenerateRequest{
		Model:    "claude-x",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Tools: []llm.Tool{{
			Name:        "nexus_search",
			Description: "search",
			Schema:      map[string]any{"properties": map[string]any{"q": map[string]any{"type": "string"}}, "required": []string{"q"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(params.Tools) != 1 {
		t.Fatalf("tools = %d", len(params.Tools))
	}
}
