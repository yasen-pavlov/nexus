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

// nexusOpenAttachmentToolName is the flag-gated tool that pulls a single
// attachment (by chunk id) into the next round — as an image for vision
// models, or its extracted text otherwise. Master plan §5.4.
const nexusOpenAttachmentToolName = "nexus_open_attachment"

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

// nexusOpenAttachmentTool lets the model fetch one attachment's content by
// chunk id. Flag-gated (admin enable_open_attachment) because it widens
// what the model can pull into context; results are still ownership-checked
// against the calling user.
var nexusOpenAttachmentTool = llm.Tool{
	Name: nexusOpenAttachmentToolName,
	Description: "Open a specific attachment or document by its chunk id and include its content in the next round. " +
		"For images this attaches the picture itself (when the model can see images); otherwise it returns the extracted text. " +
		"Use after nexus_search when you need the full content of a particular result. The id is the chunk's id from a search result.",
	Schema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chunk_id": map[string]any{
				"type":        "string",
				"description": "The id of the chunk/document to open (from a search result's id field).",
			},
		},
		"required": []string{"chunk_id"},
	},
}

// BuildToolList returns the tools to expose for one orchestrator round.
// Empty when the model can't drive tools (Ollama vision-only models,
// future text-only models) OR when MaxToolRounds is 0 (admin-disabled)
// OR when the orchestrator has hit the round cap and is forcing the
// model to answer without further tool use. nexus_open_attachment is
// appended only when the admin has enabled the flag.
func BuildToolList(info llm.ModelInfo, maxToolRounds int, enableOpenAttachment bool) []llm.Tool {
	if !info.SupportsTools || maxToolRounds <= 0 {
		return nil
	}
	tools := []llm.Tool{nexusSearchTool}
	if enableOpenAttachment {
		tools = append(tools, nexusOpenAttachmentTool)
	}
	return tools
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
	// chunks resolves a single chunk by id for nexus_open_attachment.
	// binaries fetches its cached binary (cache-only). Both may be nil
	// when the host didn't wire multimodal deps — open_attachment then
	// returns a graceful "unavailable" outcome instead of panicking.
	chunks         AttachmentResolver
	binaries       Getter
	userID         uuid.UUID
	supportsVision bool
	supportsPDF    bool
	limit          int
	log            *zap.Logger
}

// newSearchToolDispatcher constructs a dispatcher for one user turn. The
// userID is baked in so every dispatched call (search OR open-attachment)
// stays inside the calling user's permissions. supportsVision / supportsPDF
// decide whether nexus_open_attachment returns an image block, a native PDF
// block, or just extracted text.
func newSearchToolDispatcher(search SearchProvider, chunks AttachmentResolver, binaries Getter, userID uuid.UUID, supportsVision, supportsPDF bool, log *zap.Logger) *searchToolDispatcher {
	return &searchToolDispatcher{
		search:         search,
		chunks:         chunks,
		binaries:       binaries,
		userID:         userID,
		supportsVision: supportsVision,
		supportsPDF:    supportsPDF,
		limit:          nexusSearchToolResultLimit,
		log:            log,
	}
}

// Dispatch executes one tool call. Strict-but-tolerant: malformed args
// or backend failures produce a ToolOutcome with ResultText = error
// message so the LLM can self-correct on the next round. The
// orchestrator never re-raises a Go error for tool dispatch — the round
// loop stays alive and the model gets a clean turn-completion path.
func (d *searchToolDispatcher) Dispatch(ctx context.Context, call llm.ToolCall) ToolOutcome {
	switch call.Name {
	case nexusSearchToolName:
		return d.dispatchSearch(ctx, call)
	case nexusOpenAttachmentToolName:
		return d.dispatchOpenAttachment(ctx, call)
	default:
		return ToolOutcome{
			ResultText: fmt.Sprintf("error: unknown tool %q", call.Name),
			Summary:    fmt.Sprintf("Unknown tool: %s", call.Name),
		}
	}
}

// dispatchSearch handles the nexus_search tool.
func (d *searchToolDispatcher) dispatchSearch(ctx context.Context, call llm.ToolCall) ToolOutcome {
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

// nexusOpenAttachmentArgs is the parsed shape of the open-attachment args.
type nexusOpenAttachmentArgs struct {
	ChunkID string `json:"chunk_id"`
}

// dispatchOpenAttachment handles nexus_open_attachment: resolve the chunk
// by id, RE-CHECK ownership (the model supplies an arbitrary id, so this
// is the security boundary), then return the attachment as an image
// (vision models) or its extracted text. Cache-only for the binary;
// strict-but-tolerant like dispatchSearch — never returns a Go error.
func (d *searchToolDispatcher) dispatchOpenAttachment(ctx context.Context, call llm.ToolCall) ToolOutcome {
	if d.chunks == nil {
		return ToolOutcome{
			ResultText: "error: attachment lookup is not available",
			Summary:    "Attachment lookup unavailable",
		}
	}
	var args nexusOpenAttachmentArgs
	if call.ArgsJSON != "" {
		if err := json.Unmarshal([]byte(call.ArgsJSON), &args); err != nil {
			d.log.Warn("rag: open_attachment args parse failed",
				zap.String("raw", call.ArgsJSON), zap.Error(err))
			return ToolOutcome{
				ResultText: "error: invalid arguments — must be JSON with a 'chunk_id' string field",
				Summary:    "Open-attachment call had invalid arguments",
			}
		}
	}
	args.ChunkID = strings.TrimSpace(args.ChunkID)
	if args.ChunkID == "" {
		return ToolOutcome{
			ResultText: "error: 'chunk_id' is required and must be non-empty",
			Summary:    "Open-attachment call missing chunk_id",
		}
	}

	chunk, err := d.chunks.GetChunkByDocID(ctx, args.ChunkID)
	if err != nil || chunk == nil {
		return ToolOutcome{
			ResultText: fmt.Sprintf("error: no attachment found for id %q", args.ChunkID),
			Summary:    "Attachment not found",
		}
	}

	// Ownership boundary: the model can pass ANY id, so re-check that the
	// calling user may read this chunk (owner match OR shared). Mirrors the
	// /api/documents/{id}/content rule; a not-found-style message avoids
	// leaking existence of other users' docs (feedback_endpoint_auth.md).
	if chunk.OwnerID != d.userID.String() && !chunk.Shared {
		return ToolOutcome{
			ResultText: fmt.Sprintf("error: no attachment found for id %q", args.ChunkID),
			Summary:    "Attachment not found",
		}
	}

	date := ""
	if !chunk.CreatedAt.IsZero() {
		date = chunk.CreatedAt.Format("2006-01-02")
	}
	content := chunk.Content
	if content == "" {
		content = chunk.FullContent
	}
	// Use the caller-supplied doc id (a UUID, the same handle search
	// results carry) for the document and preview — NOT chunk.ID, which is
	// the composite OpenSearch _id and wouldn't resolve via
	// /api/documents/{id}/content for the FE thumbnail.
	doc := llm.Document{
		ID:      args.ChunkID,
		Title:   chunk.Title,
		Source:  chunk.SourceType,
		Date:    date,
		Content: content,
	}
	preview := ChunkPreview{
		DocID:    args.ChunkID,
		Title:    chunk.Title,
		Source:   chunk.SourceType,
		Date:     date,
		MimeType: chunk.MimeType,
	}

	title := chunk.Title
	if title == "" {
		title = args.ChunkID
	}
	attached := d.attachChunkMedia(ctx, &doc, chunk, args.ChunkID, title)

	return ToolOutcome{
		ResultText: openAttachmentResultText(attached, title, content),
		Summary:    fmt.Sprintf("Opened %q", title),
		Chunks:     []ChunkPreview{preview},
		Docs:       []llm.Document{doc},
	}
}

// attachChunkMedia attaches the picture (vision models) or native PDF
// (PDF-capable models) to doc when the binary is cached within the size
// cap, returning the attached kind ("image" | "pdf" | ""). Otherwise the
// extracted text already on the doc carries the content.
func (d *searchToolDispatcher) attachChunkMedia(ctx context.Context, doc *llm.Document, chunk *model.Chunk, citeID, title string) string {
	switch {
	case d.supportsVision && isImageMime(chunk.MimeType):
		if img, ok := loadCachedImage(ctx, d.binaries, chunk.SourceType, chunk.SourceName, chunk.SourceID, chunk.MimeType, citeID); ok {
			doc.Images = append(doc.Images, img)
			return "image"
		}
	case d.supportsPDF && isPDFMime(chunk.MimeType):
		if pdf, ok := loadCachedPDF(ctx, d.binaries, chunk.SourceType, chunk.SourceName, chunk.SourceID, title, citeID); ok {
			doc.PDFs = append(doc.PDFs, pdf)
			return "pdf"
		}
	}
	return ""
}

// openAttachmentResultText picks the tool_result prose for an opened
// attachment based on what (if anything) was attached.
func openAttachmentResultText(attached, title, content string) string {
	switch {
	case attached == "image":
		return fmt.Sprintf("Opened attachment %q (image attached below).", title)
	case attached == "pdf":
		return fmt.Sprintf("Opened attachment %q (PDF attached below).", title)
	case content != "":
		return fmt.Sprintf("Opened attachment %q:\n%s", title, content)
	default:
		return fmt.Sprintf("Opened attachment %q, but it has no viewable content or extractable text.", title)
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
