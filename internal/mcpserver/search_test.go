package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muty/nexus/internal/cliclient"
	"github.com/muty/nexus/internal/model"
)

// backend spins up an httptest server whose /api/search handler is `h`, returns
// a cliclient pointed at it, and registers cleanup.
func backend(t *testing.T, h http.HandlerFunc) *cliclient.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return cliclient.New(srv.URL, "nexus_pat_test")
}

// connect builds an MCP server backed by `client` and returns an initialized,
// in-memory-connected client session.
func connect(t *testing.T, client *cliclient.Client) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	srv := New(client, "test")
	ss, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := c.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// writeSearch wraps a SearchResult in the server's success envelope.
func writeSearch(w http.ResponseWriter, res model.SearchResult) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": res})
}

func TestListToolsExposesSearchWithSchema(t *testing.T) {
	cs := connect(t, backend(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSearch(w, model.SearchResult{})
	}))

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "nexus_search" {
		t.Fatalf("want exactly nexus_search, got %+v", res.Tools)
	}
	// The input schema must be inferred from searchInput: query required, the
	// filters optional. Marshal the schema back to JSON and assert on it.
	schema, err := json.Marshal(res.Tools[0].InputSchema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	s := string(schema)
	for _, want := range []string{`"query"`, `"sources"`, `"date_from"`, `"date_to"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("schema missing %s: %s", want, s)
		}
	}
	if !strings.Contains(s, `"required":["query"]`) {
		t.Fatalf("query must be the only required field: %s", s)
	}

	// The annotations are part of the tool's public contract: nexus_search reads
	// a bounded, owned corpus and never mutates it.
	ann := res.Tools[0].Annotations
	if ann == nil || !ann.ReadOnlyHint {
		t.Fatalf("ReadOnlyHint must be true, got %+v", ann)
	}
	if ann.OpenWorldHint == nil || *ann.OpenWorldHint {
		t.Fatalf("OpenWorldHint must be explicitly false (closed corpus), got %+v", ann.OpenWorldHint)
	}
}

func TestSearchMapsHitsToTextAndStructured(t *testing.T) {
	id := uuid.New()
	created := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	var gotQuery string
	cs := connect(t, backend(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeSearch(w, model.SearchResult{
			Query:      "invoice",
			TotalCount: 7,
			Documents: []model.DocumentHit{{
				Document: model.Document{
					ID:         id,
					Title:      "March invoice",
					SourceType: "paperless",
					SourceName: "Paperless",
					MimeType:   "application/pdf",
					URL:        "https://paperless.local/documents/42",
					CreatedAt:  created,
				},
				Rank:     1.5,
				Headline: "the <mark>invoice</mark> total is &#x2F;1000",
			}},
		})
	}))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "nexus_search",
		Arguments: map[string]any{"query": "invoice", "sources": []string{"paperless"}, "date_from": "2026-01-01"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res.Content)
	}

	// The server requested the per-call limit and forwarded the filters.
	for _, want := range []string{"q=invoice", "limit=5", "sources=paperless", "date_from=2026-01-01"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("backend query %q missing %q", gotQuery, want)
		}
	}

	// Text content: title, source/date line, id, a cleaned snippet (tags
	// stripped, entity decoded), the url, and the total count.
	text := textOf(t, res)
	for _, want := range []string{
		"March invoice", "paperless · Paperless · 2026-06-01", id.String(),
		"the invoice total is /1000", "https://paperless.local/documents/42", "of 7 total",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<mark>") {
		t.Fatalf("highlight markup leaked into text:\n%s", text)
	}

	// Structured content round-trips as a JSON object.
	sc, _ := res.StructuredContent.(map[string]any)
	if sc == nil {
		t.Fatalf("structured content is not an object: %T", res.StructuredContent)
	}
	if sc["query"] != "invoice" || sc["total_count"].(float64) != 7 || sc["returned"].(float64) != 1 {
		t.Fatalf("unexpected structured envelope: %+v", sc)
	}
	hits, _ := sc["results"].([]any)
	if len(hits) != 1 {
		t.Fatalf("want 1 structured hit, got %d", len(hits))
	}
	hit := hits[0].(map[string]any)
	if hit["id"] != id.String() || hit["source_type"] != "paperless" || hit["mime_type"] != "application/pdf" {
		t.Fatalf("unexpected structured hit: %+v", hit)
	}
}

func TestSearchEmptyQueryIsToolError(t *testing.T) {
	called := false
	cs := connect(t, backend(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		writeSearch(w, model.SearchResult{})
	}))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "nexus_search",
		Arguments: map[string]any{"query": "   "},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !res.IsError {
		t.Fatal("blank query must be a tool error")
	}
	if called {
		t.Fatal("backend must not be hit for a blank query")
	}
	if !strings.Contains(textOf(t, res), "query is required") {
		t.Fatalf("unexpected error text: %s", textOf(t, res))
	}
}

func TestSearchBackendErrorIsToolError(t *testing.T) {
	cs := connect(t, backend(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "opensearch down"})
	}))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "nexus_search",
		Arguments: map[string]any{"query": "anything"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !res.IsError {
		t.Fatal("backend 500 must surface as a tool error, not a protocol error")
	}
	if !strings.Contains(textOf(t, res), "opensearch down") {
		t.Fatalf("error text should carry the backend message: %s", textOf(t, res))
	}
}

func TestSearchErrorMessage(t *testing.T) {
	// An *APIError is the server's own structured error and passes through.
	apiMsg := searchErrorMessage(&cliclient.APIError{StatusCode: 500, Message: "opensearch down"})
	if !strings.Contains(apiMsg, "opensearch down") {
		t.Fatalf("APIError message should pass through, got %q", apiMsg)
	}

	// A transport error (here a *url.Error-shaped string carrying the resolved
	// URL + query) collapses to a generic message that leaks neither.
	transport := errors.New(`Get "http://nexus.local:8080/api/search?q=tax+return&date_from=2026-01-01": dial tcp: connect: connection refused`)
	got := searchErrorMessage(transport)
	if got != "search backend unreachable" {
		t.Fatalf("transport error should be generic, got %q", got)
	}
	for _, leak := range []string{"nexus.local", "/api/search", "tax+return"} {
		if strings.Contains(got, leak) {
			t.Fatalf("generic message leaked %q: %s", leak, got)
		}
	}
}

func TestSnippetBoundsHugeMultibyteContent(t *testing.T) {
	// A large multibyte body must be processed without panic and yield exactly
	// snippetMaxRunes runes + the ellipsis, all valid UTF-8 — the byte cap lands
	// mid-rune here (1120 is not a multiple of 3), so the partial trailing rune
	// must be dropped rather than surfaced as U+FFFD.
	huge := strings.Repeat("中", 100000) // 3-byte runes, 300KB
	s := snippet(&model.DocumentHit{Document: model.Document{Content: huge}})
	if n := len([]rune(s)); n != snippetMaxRunes+1 {
		t.Fatalf("snippet = %d runes, want %d", n, snippetMaxRunes+1)
	}
	if !utf8.ValidString(s) {
		t.Fatalf("snippet is not valid UTF-8: %q", s)
	}
	if !strings.HasSuffix(s, "…") {
		t.Fatalf("want ellipsis suffix, got %q", s[len(s)-6:])
	}
}

func TestSearchUnauthenticatedIsToolError(t *testing.T) {
	// Empty-token client pointed at an unreachable URL: the Authenticated()
	// short-circuit must return the login hint WITHOUT any network call (a real
	// call would yield "search backend unreachable" instead).
	cs := connect(t, cliclient.New("http://nexus.invalid", ""))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "nexus_search",
		Arguments: map[string]any{"query": "anything"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !res.IsError {
		t.Fatal("an unauthenticated search must be a tool error")
	}
	if !strings.Contains(textOf(t, res), "not authenticated") {
		t.Fatalf("want the login hint, got: %s", textOf(t, res))
	}
}

func TestSearch401MapsToLoginHint(t *testing.T) {
	// A present-but-rejected token (backend 401) bypasses the short-circuit but
	// must still map to the actionable login hint, not a raw "(status 401)".
	cs := connect(t, backend(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "token expired"})
	}))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "nexus_search",
		Arguments: map[string]any{"query": "anything"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !res.IsError {
		t.Fatal("a 401 must be a tool error")
	}
	text := textOf(t, res)
	if !strings.Contains(text, "not authenticated") {
		t.Fatalf("401 should map to the login hint, got: %s", text)
	}
	if strings.Contains(text, "token expired") || strings.Contains(text, "401") {
		t.Fatalf("401 mapping should replace the raw server message: %s", text)
	}
}

func TestServerAdvertisesInstructions(t *testing.T) {
	cs := connect(t, backend(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSearch(w, model.SearchResult{})
	}))
	instr := cs.InitializeResult().Instructions
	if instr == "" {
		t.Fatal("server must advertise instructions for portable auto-search guidance")
	}
	if !strings.Contains(instr, "nexus_search") {
		t.Fatalf("instructions should name the tool: %q", instr)
	}
}

func TestSearchNoResults(t *testing.T) {
	cs := connect(t, backend(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSearch(w, model.SearchResult{Query: "ghost", TotalCount: 0})
	}))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "nexus_search",
		Arguments: map[string]any{"query": "ghost"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatal("an empty result set is not an error")
	}
	if !strings.Contains(textOf(t, res), `No results for "ghost"`) {
		t.Fatalf("unexpected text: %s", textOf(t, res))
	}
}

func TestMapSearchOutputCapsAtLimit(t *testing.T) {
	// Defence in depth: even if the backend ignored our limit and returned more
	// than searchResultLimit hits, the tool result is capped.
	docs := make([]model.DocumentHit, searchResultLimit+3)
	for i := range docs {
		docs[i] = model.DocumentHit{Document: model.Document{ID: uuid.New(), SourceType: "filesystem"}}
	}
	out := mapSearchOutput("q", &model.SearchResult{TotalCount: 99, Documents: docs})
	if out.Returned != searchResultLimit || len(out.Results) != searchResultLimit {
		t.Fatalf("returned %d hits, want capped at %d", out.Returned, searchResultLimit)
	}
	if out.TotalCount != 99 {
		t.Fatalf("total_count should reflect the backend total, got %d", out.TotalCount)
	}
}

func TestMapSearchOutputNilResult(t *testing.T) {
	out := mapSearchOutput("q", nil)
	if out.Query != "q" || out.TotalCount != 0 || out.Returned != 0 || len(out.Results) != 0 {
		t.Fatalf("nil result should map to an empty output: %+v", out)
	}
}

func TestRenderAndMapHandleSparseHit(t *testing.T) {
	// A hit with no title, source name, date, or url: mapSearchOutput must leave
	// date empty (zero CreatedAt) and renderSearchText must show "(untitled)" and
	// omit the missing meta/url lines without panicking.
	out := mapSearchOutput("q", &model.SearchResult{
		TotalCount: 1,
		Documents:  []model.DocumentHit{{Document: model.Document{SourceType: "filesystem"}}},
	})
	if out.Results[0].Date != "" {
		t.Fatalf("zero CreatedAt should map to empty date, got %q", out.Results[0].Date)
	}
	text := renderSearchText(out)
	if !strings.Contains(text, "(untitled)") {
		t.Fatalf("missing untitled fallback:\n%s", text)
	}
	if strings.Contains(text, " · ") {
		t.Fatalf("no source name/date should mean no separator:\n%s", text)
	}
	if strings.Contains(text, "http") {
		t.Fatalf("no url should mean no url line:\n%s", text)
	}
}

func TestSnippetFallsBackToContentAndTruncates(t *testing.T) {
	// Empty headline → fall back to content; over-long content → truncated on a
	// rune boundary with an ellipsis.
	long := strings.Repeat("word ", 200) // 1000 runes, well over the cap
	s := snippet(&model.DocumentHit{Document: model.Document{Content: long}})
	if !strings.HasSuffix(s, "…") {
		t.Fatalf("long content should be ellipsized: %q", s)
	}
	if n := len([]rune(s)); n != snippetMaxRunes+1 { // +1 for the ellipsis rune
		t.Fatalf("snippet length = %d runes, want %d", n, snippetMaxRunes+1)
	}

	// Short content passes through untouched (no ellipsis).
	short := snippet(&model.DocumentHit{Document: model.Document{Content: "just a line"}})
	if short != "just a line" {
		t.Fatalf("short content snippet = %q", short)
	}
}

// textOf concatenates the TextContent blocks of a tool result.
func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
