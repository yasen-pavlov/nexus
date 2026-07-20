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

	// The sources enum must include every live source type so the model can
	// target calendar (ical) the same way it targets email/telegram/etc.
	sources, ok := props["sources"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing 'sources' property")
	}
	items, ok := sources["items"].(map[string]any)
	if !ok {
		t.Fatalf("sources.items missing or wrong type")
	}
	enum, ok := items["enum"].([]string)
	if !ok {
		t.Fatalf("sources.items.enum missing or wrong type")
	}
	for _, want := range []string{"filesystem", "imap", "telegram", "paperless", "ical"} {
		found := false
		for _, e := range enum {
			if e == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("sources enum %v missing %q", enum, want)
		}
	}
}

func TestRenderToolResultDocuments_EscapesAttributes(t *testing.T) {
	docs := []llm.Document{{
		ID:      "id-1",
		Source:  "imap",
		Title:   `Re: "urgent" <script>`,
		Date:    "2026-01-01",
		Content: "body text",
	}}
	got := renderToolResultDocuments("query", docs)

	if !strings.Contains(got, "Found 1 result(s)") {
		t.Errorf("missing per-query summary line:\n%s", got)
	}
	// The angle brackets and quotes in the title must be HTML-escaped so they
	// can't break out of the attribute and corrupt the block structure.
	if !strings.Contains(got, "title=\"Re: &quot;urgent&quot; &lt;script&gt;\"") {
		t.Errorf("title attribute not escaped:\n%s", got)
	}
	if strings.Contains(got, `title="Re: "urgent"`) {
		t.Errorf("raw unescaped quote leaked into attribute:\n%s", got)
	}
	// Content between the tags stays verbatim.
	if !strings.Contains(got, "body text") {
		t.Errorf("content missing:\n%s", got)
	}
}

func TestRenderToolResultDocuments_NoResults(t *testing.T) {
	if got := renderToolResultDocuments("q", nil); got != `No results for "q".` {
		t.Errorf("got %q", got)
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
		{Document: model.Document{
			ID: uuid.New(), Title: "Wolt Receipt", SourceType: "paperless", Content: "€8.40 total",
			SourceName: "paperless", SourceID: "7", Size: 4096,
			URL:      "https://paperless.local/documents/7/details",
			Metadata: map[string]any{"correspondent": "Wolt", "message_lines": []any{"drop"}},
		}},
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
	// The agentic search path must carry the rich fields the Ask cards render,
	// with heavy metadata keys stripped.
	if c := out.Chunks[0]; c.SourceName != "paperless" || c.SourceID != "7" ||
		c.Size != 4096 || c.URL != "https://paperless.local/documents/7/details" {
		t.Errorf("search-tool preview missing rich fields: %+v", c)
	}
	if c := out.Chunks[0]; c.Metadata["correspondent"] != "Wolt" {
		t.Errorf("allowlisted metadata missing: %+v", c.Metadata)
	} else if _, ok := c.Metadata["message_lines"]; ok {
		t.Errorf("heavy key should be stripped: %+v", c.Metadata)
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

func TestAppendUniqueDocs_MergesMediaOntoExisting(t *testing.T) {
	// The open_attachment case: "y" is already present as text-only, and the
	// incoming duplicate carries an image. The media must be merged onto the
	// existing entry (not discarded), preserving order x,y,z.
	a := []llm.Document{{ID: "x"}, {ID: "y"}}
	b := []llm.Document{
		{ID: "y", Images: []llm.Image{{SourceID: "y", MediaType: "image/png", Data: []byte{1}}}},
		{ID: "z"},
	}
	got := appendUniqueDocs(a, b)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, want := range []string{"x", "y", "z"} {
		if got[i].ID != want {
			t.Errorf("got[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}
	if len(got[1].Images) != 1 {
		t.Errorf("expected media merged onto existing 'y', got %d images", len(got[1].Images))
	}
}

func TestAppendUniqueDocs_NoDoubleAttachSameSourceID(t *testing.T) {
	// "y" already has an image with SourceID "y"; re-supplying the same
	// SourceID must not double-attach it.
	a := []llm.Document{{ID: "y", Images: []llm.Image{{SourceID: "y", MediaType: "image/png", Data: []byte{1}}}}}
	b := []llm.Document{{ID: "y", Images: []llm.Image{{SourceID: "y", MediaType: "image/png", Data: []byte{1}}}}}
	got := appendUniqueDocs(a, b)
	if len(got) != 1 || len(got[0].Images) != 1 {
		t.Errorf("expected single doc with a single image, got %d docs / %d images", len(got), len(got[0].Images))
	}

	// A distinct SourceID DOES attach.
	c := []llm.Document{{ID: "y", Images: []llm.Image{{SourceID: "y-att", MediaType: "image/png", Data: []byte{2}}}}}
	got = appendUniqueDocs(got, c)
	if len(got[0].Images) != 2 {
		t.Errorf("distinct SourceID should attach; got %d images", len(got[0].Images))
	}
}

func TestAppendUniqueDocs_MergesPDFs(t *testing.T) {
	a := []llm.Document{{ID: "doc"}}
	b := []llm.Document{{ID: "doc", PDFs: []llm.PDF{{SourceID: "doc", MediaType: "application/pdf", Data: []byte("%PDF")}}}}
	got := appendUniqueDocs(a, b)
	if len(got) != 1 || len(got[0].PDFs) != 1 {
		t.Errorf("expected PDF merged onto existing doc, got %d docs / %d pdfs", len(got), len(got[0].PDFs))
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
