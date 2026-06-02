package rag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// nexusSearchToolName is the tool name exposed to the LLM. Kept as a
// constant so the orchestrator's dispatch switch and tests reference the
// same string.
const nexusSearchToolName = "nexus_search"

// nexusSearchToolResultLimit caps how many top-reranked chunks the
// dispatcher sends back to the LLM per call. Master plan §5.4.
const nexusSearchToolResultLimit = 5

// nexusSearchTool is the JSON-Schema-described function the LLM may
// invoke when the initial evidence isn't enough to answer cleanly.
// `query` is required; all filters are optional. Schema mirrors the
// fields supported by model.SearchRequest, so the dispatcher can map
// straight onto the existing search pipeline.
var nexusSearchTool = llm.Tool{
	Name: nexusSearchToolName,
	Description: "Search the user's personal knowledge base for additional context. " +
		"Use only when the documents already provided in the prompt are not sufficient to answer the user's question. " +
		"Results are scoped to the user's permissions automatically.",
	Schema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Natural-language search query. Be specific.",
			},
			"sources": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string", "enum": []string{"filesystem", "imap", "telegram", "paperless"}},
				"description": "Optional list of source types to restrict the search to.",
			},
			"date_from": map[string]any{
				"type":        "string",
				"description": "Optional ISO date (YYYY-MM-DD). Only return chunks created on or after this date.",
			},
			"date_to": map[string]any{
				"type":        "string",
				"description": "Optional ISO date (YYYY-MM-DD). Only return chunks created on or before this date.",
			},
		},
		"required": []string{"query"},
	},
}

// BuildToolList returns the tools to expose for one orchestrator round.
// Empty when the model can't drive tools (Ollama vision-only models,
// future text-only models) OR when MaxToolRounds is 0 (admin-disabled)
// OR when the orchestrator has hit the round cap and is forcing the
// model to answer without further tool use.
func BuildToolList(info llm.ModelInfo, maxToolRounds int) []llm.Tool {
	if !info.SupportsTools || maxToolRounds <= 0 {
		return nil
	}
	return []llm.Tool{nexusSearchTool}
}

// ToolDispatcher executes a finalized tool call. Implementations are
// stateful only insofar as they bake the per-turn user identity in at
// construction so visibility filters apply to every dispatched call.
type ToolDispatcher interface {
	Dispatch(ctx context.Context, call llm.ToolCall) ToolOutcome
}

// ToolOutcome is the result of one dispatched tool call. ResultText is
// what gets fed back to the LLM as a tool_result message; Summary and
// Chunks drive the FE collapsible trace + evidence-rail merge; Docs
// extend the cumulative GenerateRequest.Documents on the next round so
// the model can cite the new chunks via stable indices.
type ToolOutcome struct {
	ResultText string
	Summary    string
	Chunks     []ChunkPreview
	Docs       []llm.Document
}

// nexusSearchArgs is the parsed shape of the model's tool args. Tolerant
// to missing optional fields; the dispatcher decides what to do with
// each.
type nexusSearchArgs struct {
	Query    string   `json:"query"`
	Sources  []string `json:"sources,omitempty"`
	DateFrom string   `json:"date_from,omitempty"`
	DateTo   string   `json:"date_to,omitempty"`
}

// searchToolDispatcher implements ToolDispatcher by routing nexus_search
// calls through the existing SearchProvider. The userID is baked in at
// construction so the per-turn dispatcher can never accidentally search
// outside the calling user's permissions — hard constraint per master
// plan §2 and feedback_endpoint_auth.md.
type searchToolDispatcher struct {
	search SearchProvider
	userID uuid.UUID
	limit  int
	log    *zap.Logger
}

// newSearchToolDispatcher constructs a dispatcher for one user turn.
func newSearchToolDispatcher(search SearchProvider, userID uuid.UUID, log *zap.Logger) *searchToolDispatcher {
	return &searchToolDispatcher{
		search: search,
		userID: userID,
		limit:  nexusSearchToolResultLimit,
		log:    log,
	}
}

// Dispatch executes one tool call. Strict-but-tolerant: malformed args
// or backend failures produce a ToolOutcome with ResultText = error
// message so the LLM can self-correct on the next round. The
// orchestrator never re-raises a Go error for tool dispatch — the round
// loop stays alive and the model gets a clean turn-completion path.
func (d *searchToolDispatcher) Dispatch(ctx context.Context, call llm.ToolCall) ToolOutcome {
	if call.Name != nexusSearchToolName {
		return ToolOutcome{
			ResultText: fmt.Sprintf("error: unknown tool %q", call.Name),
			Summary:    fmt.Sprintf("Unknown tool: %s", call.Name),
		}
	}

	var args nexusSearchArgs
	if call.ArgsJSON != "" {
		if err := json.Unmarshal([]byte(call.ArgsJSON), &args); err != nil {
			d.log.Warn("rag: nexus_search args parse failed",
				zap.String("raw", call.ArgsJSON), zap.Error(err))
			return ToolOutcome{
				ResultText: "error: invalid arguments — must be JSON with a 'query' string field",
				Summary:    "Search call had invalid arguments",
			}
		}
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return ToolOutcome{
			ResultText: "error: 'query' is required and must be non-empty",
			Summary:    "Search call missing query",
		}
	}

	req := model.SearchRequest{
		Query:    args.Query,
		Limit:    d.limit,
		Sources:  args.Sources,
		DateFrom: args.DateFrom,
		DateTo:   args.DateTo,
		OwnerID:  d.userID.String(),
	}
	result, err := d.search.Run(ctx, req)
	if err != nil {
		d.log.Warn("rag: nexus_search dispatch failed", zap.Error(err))
		return ToolOutcome{
			ResultText: "error: search failed: " + err.Error(),
			Summary:    "Search failed",
		}
	}

	hits := result.Documents
	if len(hits) > d.limit {
		hits = hits[:d.limit]
	}

	docs := buildLLMDocs(hits, d.limit)
	chunks := buildPreviews(hits, d.limit)
	resultText := renderToolResultDocuments(args.Query, docs)

	summary := fmt.Sprintf("Searched %q — %d result", args.Query, len(chunks))
	if len(chunks) != 1 {
		summary = fmt.Sprintf("Searched %q — %d results", args.Query, len(chunks))
	}

	return ToolOutcome{
		ResultText: resultText,
		Summary:    summary,
		Chunks:     chunks,
		Docs:       docs,
	}
}

// renderToolResultDocuments formats the search hits as <document> blocks
// the LLM can read. Mirrors the shape used by the Ollama adapter's
// initial-context block (internal/llm/ollama/ollama.go buildDocumentsBlock)
// so models trained on either format get a familiar prompt structure.
// The leading line is a short prose summary so the model can decide
// whether the call helped before reading the bodies.
func renderToolResultDocuments(query string, docs []llm.Document) string {
	if len(docs) == 0 {
		return fmt.Sprintf("No results for %q.", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d result(s) for %q.\n", len(docs), query)
	for i, d := range docs {
		fmt.Fprintf(&b, "\n<document index=\"%d\" id=\"%s\"", i+1, d.ID)
		if d.Source != "" {
			fmt.Fprintf(&b, " source=\"%s\"", d.Source)
		}
		if d.Title != "" {
			fmt.Fprintf(&b, " title=\"%s\"", d.Title)
		}
		if d.Date != "" {
			fmt.Fprintf(&b, " date=\"%s\"", d.Date)
		}
		b.WriteString(">\n")
		b.WriteString(d.Content)
		b.WriteString("\n</document>\n")
	}
	return b.String()
}

// appendUniqueDocs extends `dst` with `src` entries whose DocID isn't
// already present in `dst`. Used by the orchestrator's round loop to
// keep the cumulative GenerateRequest.Documents slice deduped while
// preserving the original ordering — citation indices stay stable
// across rounds for non-Anthropic providers, and the Anthropic SDK's
// document_index behaviour matches.
func appendUniqueDocs(dst, src []llm.Document) []llm.Document {
	if len(src) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, d := range dst {
		seen[d.ID] = struct{}{}
	}
	for _, d := range src {
		if _, ok := seen[d.ID]; ok {
			continue
		}
		seen[d.ID] = struct{}{}
		dst = append(dst, d)
	}
	return dst
}

// appendUniqueChunks is the ChunkPreview twin of appendUniqueDocs —
// orchestrator uses it to grow the persisted evidence union (shown in
// the FE rail and re-rendered after page reload) without duplicating
// chunks that were already in the initial retrieval.
func appendUniqueChunks(dst, src []ChunkPreview) []ChunkPreview {
	if len(src) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, c := range dst {
		seen[c.DocID] = struct{}{}
	}
	for _, c := range src {
		if _, ok := seen[c.DocID]; ok {
			continue
		}
		seen[c.DocID] = struct{}{}
		dst = append(dst, c)
	}
	return dst
}

// ErrUnknownTool is returned only for orchestrator-side bugs that
// dispatch to nil — exposed so callers can errors.Is it. The
// dispatcher itself never returns this; it folds unknown tools into a
// ToolOutcome so the loop stays alive.
var ErrUnknownTool = errors.New("rag: unknown tool")
