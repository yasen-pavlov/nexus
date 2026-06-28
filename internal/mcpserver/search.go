package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/muty/nexus/internal/cliclient"
	"github.com/muty/nexus/internal/model"
)

// searchToolName is the MCP tool id. It matches the rag orchestrator's internal
// tool name so a model trained against the in-app ask flow sees a familiar
// primitive name and schema.
const searchToolName = "nexus_search"

// searchResultLimit caps how many hits one nexus_search call returns. Mirrors
// the rag orchestrator's per-call cap so an MCP host gets the same focused
// top-k an in-app ask would, not a 20-row dump that floods the model's context.
const searchResultLimit = 5

// snippetMaxRunes bounds a hit's preview text so a tool result stays compact.
const snippetMaxRunes = 280

// indentedLine is the format for an indented continuation line in the text
// rendering of a hit (meta, snippet, url).
const indentedLine = "   %s\n"

// searchInput is the nexus_search argument schema. The SDK infers the JSON
// Schema (and which fields are required) from these struct tags: Query has no
// omitempty so it becomes a required property; the rest are optional filters.
// Mirrors model.SearchRequest / the rag nexus_search tool.
type searchInput struct {
	Query    string   `json:"query" jsonschema:"natural-language search query; be specific"`
	Sources  []string `json:"sources,omitempty" jsonschema:"optional source types to restrict to; any of: filesystem, imap, telegram, paperless, ical"`
	DateFrom string   `json:"date_from,omitempty" jsonschema:"optional ISO date (YYYY-MM-DD); only return documents created on or after this date"`
	DateTo   string   `json:"date_to,omitempty" jsonschema:"optional ISO date (YYYY-MM-DD); only return documents created on or before this date"`
}

// searchHit is one mapped result in the structured tool output. It flattens the
// per-source fields the web UI cards surface into a shape an LLM host can read.
type searchHit struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	SourceType string  `json:"source_type"`
	SourceName string  `json:"source_name"`
	Date       string  `json:"date,omitempty"`
	URL        string  `json:"url,omitempty"`
	MimeType   string  `json:"mime_type,omitempty"`
	Snippet    string  `json:"snippet,omitempty"`
	Rank       float64 `json:"rank"`
}

// searchOutput is the structured tool result. It marshals to a JSON object, as
// CallToolResult.StructuredContent requires.
type searchOutput struct {
	Query      string      `json:"query"`
	TotalCount int         `json:"total_count"`
	Returned   int         `json:"returned"`
	Results    []searchHit `json:"results"`
}

// addSearchTool registers nexus_search on srv, backed by client.
func addSearchTool(srv *mcp.Server, client *cliclient.Client) {
	// nexus_search reads a bounded, owned corpus and never mutates it: read-only,
	// and a closed world (the user's own index) rather than the open web — closer
	// to the spec's "memory" example than to a web-search tool. OpenWorldHint
	// defaults to true, so closed-world must be stated explicitly.
	openWorld := false
	mcp.AddTool(srv, &mcp.Tool{
		Name: searchToolName,
		Description: "Search the user's personal Nexus knowledge base (files, email, chat, " +
			"documents) and return the most relevant excerpts. Results are scoped to the " +
			"authenticated user plus any shared sources. Use this to ground answers in the " +
			"user's own data instead of guessing.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, searchHandler(client))
}

// searchHandler closes over client and runs one nexus_search call. Backend and
// transport failures come back as a tool-level error result (IsError) so the
// host's model can see and adapt to them, never as an MCP protocol-level error
// (which would abort the call instead of feeding the model a recoverable
// message).
func searchHandler(client *cliclient.Client) mcp.ToolHandlerFor[searchInput, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
		query := strings.TrimSpace(in.Query)
		if query == "" {
			return errorResult("query is required and must be non-empty"), nil, nil
		}
		res, err := client.Search(ctx, cliclient.SearchParams{
			Query:    query,
			Limit:    searchResultLimit,
			Sources:  in.Sources,
			DateFrom: in.DateFrom,
			DateTo:   in.DateTo,
		})
		if err != nil {
			return errorResult(searchErrorMessage(err)), nil, nil
		}
		out := mapSearchOutput(query, res)
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: renderSearchText(out)}},
			StructuredContent: out,
		}, nil, nil
	}
}

// searchErrorMessage renders a Search failure for the model. A *cliclient.APIError
// is the server's own structured error (status + {"error"} message) and is safe
// and useful to pass through. Any other error is transport-level: its Error()
// wraps a *url.Error that embeds the full resolved URL + query string, which is
// noise to the model and gratuitously echoes internal topology — collapse those
// to a generic message.
func searchErrorMessage(err error) string {
	var apiErr *cliclient.APIError
	if errors.As(err, &apiErr) {
		return "search failed: " + apiErr.Error()
	}
	return "search backend unreachable"
}

// errorResult builds a tool result flagged IsError with msg as its text. Used
// for in-tool failures the model should see and react to.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "error: " + msg}},
		IsError: true,
	}
}

// mapSearchOutput projects a SearchResult onto the structured tool output,
// capping the hit slice at searchResultLimit (defence in depth — the server
// already requested that limit).
func mapSearchOutput(query string, res *model.SearchResult) searchOutput {
	out := searchOutput{Query: query}
	if res == nil {
		return out
	}
	out.TotalCount = res.TotalCount
	hits := res.Documents
	if len(hits) > searchResultLimit {
		hits = hits[:searchResultLimit]
	}
	out.Results = make([]searchHit, 0, len(hits))
	for i := range hits {
		h := &hits[i]
		date := ""
		if !h.CreatedAt.IsZero() {
			date = h.CreatedAt.Format("2006-01-02")
		}
		out.Results = append(out.Results, searchHit{
			ID:         h.ID.String(),
			Title:      strings.TrimSpace(h.Title),
			SourceType: h.SourceType,
			SourceName: h.SourceName,
			Date:       date,
			URL:        h.URL,
			MimeType:   h.MimeType,
			Snippet:    snippet(h),
			Rank:       h.Rank,
		})
	}
	out.Returned = len(out.Results)
	return out
}

// renderSearchText is the human/LLM-readable rendering of the mapped output: a
// numbered list of hits the host's model can scan, plus a trailing count. The
// structured payload carries the same data for clients that consume it.
func renderSearchText(out searchOutput) string {
	if len(out.Results) == 0 {
		return fmt.Sprintf("No results for %q.", out.Query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d result(s) for %q (of %d total):\n", out.Returned, out.Query, out.TotalCount)
	for i := range out.Results {
		h := &out.Results[i]
		title := h.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "\n%d. %s\n", i+1, title)
		meta := h.SourceType
		if h.SourceName != "" {
			meta += " · " + h.SourceName
		}
		if h.Date != "" {
			meta += " · " + h.Date
		}
		fmt.Fprintf(&b, indentedLine, meta)
		fmt.Fprintf(&b, "   id: %s\n", h.ID)
		if h.Snippet != "" {
			fmt.Fprintf(&b, indentedLine, h.Snippet)
		}
		if h.URL != "" {
			fmt.Fprintf(&b, indentedLine, h.URL)
		}
	}
	return b.String()
}

// tagPattern matches the <mark> highlight wrappers (and any other markup)
// OpenSearch embeds in a headline.
var tagPattern = regexp.MustCompile(`<[^>]+>`)

// snippetWorkingBytes caps the raw text fed into the unescape/collapse pipeline.
// A document's Content can be multi-megabyte, yet we only ever keep
// snippetMaxRunes; bound the working slice first so we don't unescape, split,
// join and rune-convert the whole body once per hit. 4 bytes/rune is the max a
// UTF-8 rune occupies, so this never starves the final truncation.
const snippetWorkingBytes = snippetMaxRunes * 4

// snippet builds a one-line preview from a hit: the highlighted headline when
// present, else the head of the content. Markup is stripped, HTML entities
// decoded (e.g. &#x2F;), whitespace collapsed, and the text truncated on a rune
// boundary so multibyte content is never split mid-character.
func snippet(hit *model.DocumentHit) string {
	text := tagPattern.ReplaceAllString(hit.Headline, "")
	if strings.TrimSpace(text) == "" {
		text = hit.Content
	}
	if len(text) > snippetWorkingBytes {
		// Cap on a byte bound, then drop any partial trailing rune the cut may
		// have left so the downstream steps see only valid UTF-8.
		text = strings.ToValidUTF8(text[:snippetWorkingBytes], "")
	}
	text = strings.Join(strings.Fields(html.UnescapeString(text)), " ")
	r := []rune(text)
	if len(r) > snippetMaxRunes {
		return string(r[:snippetMaxRunes]) + "…"
	}
	return text
}
