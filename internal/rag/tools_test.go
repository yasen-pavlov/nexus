package rag

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

func TestNexusSearchTool_SchemaShape(t *testing.T) {
	if nexusSearchTool.Name != "nexus_search" {
		t.Errorf("name = %q, want nexus_search", nexusSearchTool.Name)
	}
	props, ok := nexusSearchTool.Schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing or wrong type")
	}
	if _, ok := props["query"]; !ok {
		t.Error("schema missing 'query' property")
	}
	required, ok := nexusSearchTool.Schema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "query" {
		t.Errorf("required = %v, want [query]", required)
	}
}

func TestBuildToolList_HonoursModelAndCap(t *testing.T) {
	cases := []struct {
		name          string
		supportsTools bool
		max           int
		wantTools     bool
	}{
		{"tools-supported max>0", true, 3, true},
		{"tools-unsupported", false, 3, false},
		{"max=0 disables", true, 0, false},
		{"negative max", true, -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tools := BuildToolList(llm.ModelInfo{SupportsTools: tc.supportsTools}, tc.max, false)
			if (len(tools) > 0) != tc.wantTools {
				t.Errorf("got tools=%d, want non-empty=%v", len(tools), tc.wantTools)
			}
		})
	}
}

func TestSearchToolDispatcher_HappyPath(t *testing.T) {
	owner := uuid.New()
	docs := []model.DocumentHit{
		{Document: model.Document{ID: uuid.New(), Title: "Wolt Receipt", SourceType: "imap", Content: "€8.40 total"}},
		{Document: model.Document{ID: uuid.New(), Title: "Wolt Order", SourceType: "imap", Content: "Pizza"}},
	}
	srch := &fakeSearch{docs: docs}
	d := newSearchToolDispatcher(srch, nil, nil, owner, false, false, zap.NewNop())

	out := d.Dispatch(context.Background(), llm.ToolCall{
		ID:       "tc1",
		Name:     "nexus_search",
		ArgsJSON: `{"query":"wolt"}`,
	})
	if !strings.Contains(out.ResultText, "Wolt Receipt") {
		t.Errorf("ResultText missing first doc title: %q", out.ResultText)
	}
	if len(out.Chunks) != 2 {
		t.Errorf("Chunks=%d, want 2", len(out.Chunks))
	}
	if len(out.Docs) != 2 {
		t.Errorf("Docs=%d, want 2", len(out.Docs))
	}
	if !strings.Contains(out.Summary, "wolt") || !strings.Contains(out.Summary, "2 results") {
		t.Errorf("Summary = %q, want mention of query + count", out.Summary)
	}

	// Per-user OwnerID baked in — non-negotiable.
	if got := srch.lastRequest().OwnerID; got != owner.String() {
		t.Errorf("OwnerID = %q, want %q", got, owner.String())
	}
	if got := srch.lastRequest().Limit; got != nexusSearchToolResultLimit {
		t.Errorf("Limit = %d, want %d", got, nexusSearchToolResultLimit)
	}
}

func TestSearchToolDispatcher_PassesFilters(t *testing.T) {
	srch := &fakeSearch{docs: nil}
	d := newSearchToolDispatcher(srch, nil, nil, uuid.New(), false, false, zap.NewNop())
	d.Dispatch(context.Background(), llm.ToolCall{
		Name:     "nexus_search",
		ArgsJSON: `{"query":"q","sources":["imap","telegram"],"date_from":"2026-01-01","date_to":"2026-01-31"}`,
	})
	req := srch.lastRequest()
	if len(req.Sources) != 2 || req.Sources[0] != "imap" {
		t.Errorf("Sources = %v", req.Sources)
	}
	if req.DateFrom != "2026-01-01" || req.DateTo != "2026-01-31" {
		t.Errorf("DateFrom/To = %q/%q", req.DateFrom, req.DateTo)
	}
}

func TestSearchToolDispatcher_UnknownToolName(t *testing.T) {
	d := newSearchToolDispatcher(&fakeSearch{}, nil, nil, uuid.New(), false, false, zap.NewNop())
	out := d.Dispatch(context.Background(), llm.ToolCall{Name: "made_up_tool", ArgsJSON: `{}`})
	if !strings.Contains(out.ResultText, "unknown tool") {
		t.Errorf("ResultText = %q, want unknown-tool error", out.ResultText)
	}
}

func TestSearchToolDispatcher_MalformedArgsTolerant(t *testing.T) {
	srch := &fakeSearch{}
	d := newSearchToolDispatcher(srch, nil, nil, uuid.New(), false, false, zap.NewNop())
	out := d.Dispatch(context.Background(), llm.ToolCall{
		Name:     "nexus_search",
		ArgsJSON: `{not-json`,
	})
	if !strings.Contains(out.ResultText, "invalid arguments") {
		t.Errorf("ResultText = %q, want invalid-args message", out.ResultText)
	}
	// Search must NOT have been called.
	if srch.callCount() != 0 {
		t.Errorf("search called %d times on bad args; want 0", srch.callCount())
	}
}

func TestSearchToolDispatcher_EmptyQuery(t *testing.T) {
	srch := &fakeSearch{}
	d := newSearchToolDispatcher(srch, nil, nil, uuid.New(), false, false, zap.NewNop())
	out := d.Dispatch(context.Background(), llm.ToolCall{
		Name:     "nexus_search",
		ArgsJSON: `{"query":"   "}`,
	})
	if !strings.Contains(out.ResultText, "required") {
		t.Errorf("ResultText = %q, want required-field message", out.ResultText)
	}
	if srch.callCount() != 0 {
		t.Errorf("search called %d times on empty query; want 0", srch.callCount())
	}
}

func TestSearchToolDispatcher_BackendErrorReturnedAsToolResultText(t *testing.T) {
	srch := &fakeSearch{err: errors.New("boom")}
	d := newSearchToolDispatcher(srch, nil, nil, uuid.New(), false, false, zap.NewNop())
	out := d.Dispatch(context.Background(), llm.ToolCall{
		Name:     "nexus_search",
		ArgsJSON: `{"query":"x"}`,
	})
	if !strings.Contains(out.ResultText, "search failed") || !strings.Contains(out.ResultText, "boom") {
		t.Errorf("ResultText = %q", out.ResultText)
	}
	if !strings.Contains(out.Summary, "failed") {
		t.Errorf("Summary = %q", out.Summary)
	}
}

func TestSearchToolDispatcher_NoResults(t *testing.T) {
	d := newSearchToolDispatcher(&fakeSearch{docs: nil}, nil, nil, uuid.New(), false, false, zap.NewNop())
	out := d.Dispatch(context.Background(), llm.ToolCall{
		Name:     "nexus_search",
		ArgsJSON: `{"query":"orphan"}`,
	})
	if !strings.Contains(out.ResultText, "No results") {
		t.Errorf("ResultText = %q, want No-results message", out.ResultText)
	}
	if len(out.Chunks) != 0 || len(out.Docs) != 0 {
		t.Errorf("Chunks=%d Docs=%d, both want 0", len(out.Chunks), len(out.Docs))
	}
}

func TestAppendUniqueDocs_Dedupes(t *testing.T) {
	a := []llm.Document{{ID: "x"}, {ID: "y"}}
	b := []llm.Document{{ID: "y"}, {ID: "z"}}
	got := appendUniqueDocs(a, b)
	if len(got) != 3 {
		t.Errorf("got %d, want 3", len(got))
	}
	for i, want := range []string{"x", "y", "z"} {
		if got[i].ID != want {
			t.Errorf("got[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}
}

func TestAppendUniqueChunks_Dedupes(t *testing.T) {
	a := []ChunkPreview{{DocID: "x"}, {DocID: "y"}}
	b := []ChunkPreview{{DocID: "y"}, {DocID: "z"}}
	got := appendUniqueChunks(a, b)
	if len(got) != 3 {
		t.Errorf("got %d, want 3", len(got))
	}
	for i, want := range []string{"x", "y", "z"} {
		if got[i].DocID != want {
			t.Errorf("got[%d].DocID = %q, want %q", i, got[i].DocID, want)
		}
	}
}

// Sanity timeout — dispatcher never blocks indefinitely on a slow ctx.
// Stub fakeSearch already returns synchronously, but adding a deadline
// catches future regressions if we plumb extra IO into the dispatcher.
func TestSearchToolDispatcher_RespectsContextDeadline(t *testing.T) {
	d := newSearchToolDispatcher(&fakeSearch{}, nil, nil, uuid.New(), false, false, zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	d.Dispatch(ctx, llm.ToolCall{Name: "nexus_search", ArgsJSON: `{"query":"x"}`})
}
