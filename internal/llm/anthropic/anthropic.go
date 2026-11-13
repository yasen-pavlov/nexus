// Package anthropic implements the llm.Generator for Claude models via the
// official anthropic-sdk-go.
//
// The adapter is the only one that emits llm.EventCitation natively, mapping
// the SDK's citations_delta events to Citation events tied to the Document.ID
// the orchestrator originally provided.
//
// Claude models from the 5 generation on reason ("think") by default and bind
// that reasoning to everything sent before it. Two rules follow, and both are
// enforced here rather than left to callers:
//
//   - Requests within one turn are append-only. Documents a tool call adds
//     ride in that tool's result instead of being inserted at the front, and
//     tool use is switched off with tool_choice rather than by dropping the
//     tool definitions.
//   - A turn that stopped for tool use is sent back exactly as the API
//     returned it (llm.Event.Replay), thinking blocks included.
package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/muty/nexus/internal/llm"
	"go.uber.org/zap"
)

// Default cap when callers don't set MaxTokens — Anthropic requires the field.
const defaultMaxTokens = 4096

// max_tokens covers thinking as well as the visible answer, so a request that
// may think gets headroom on top of the caller's answer cap. Without it a
// model spends a small cap on thinking and returns no text at all.
const (
	// answerThinkingReserve covers default-level requests. Thinking at medium
	// effort stayed under ~1,000 tokens on multi-step document questions.
	answerThinkingReserve = 4096
	// minimalThinkingReserve covers minimal requests on models that cannot
	// switch thinking off (Opus 5.5, Fable 5.2).
	minimalThinkingReserve = 1024
)

// Client is an llm.Generator backed by anthropic-sdk-go.
type Client struct {
	api sdk.Client
	log *zap.Logger
}

// New constructs a client with the API key and optional base-URL override
// (used by tests). Pass empty baseURL for production.
func New(apiKey, baseURL string, log *zap.Logger) *Client {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Client{api: sdk.NewClient(opts...), log: log}
}

// Generate kicks off a streaming Messages request and returns a channel of
// llm.Events. The channel closes after exactly one EventDone or EventError.
func (c *Client) Generate(ctx context.Context, req llm.GenerateRequest) (<-chan llm.Event, error) {
	if req.Model == "" {
		return nil, errors.New("anthropic: model required")
	}

	params, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}

	out := make(chan llm.Event, 16)
	go c.run(ctx, params, req, out)
	return out, nil
}

// buildParams maps llm.GenerateRequest to MessageNewParams.
func (c *Client) buildParams(req llm.GenerateRequest) (sdk.MessageNewParams, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	params := sdk.MessageNewParams{Model: sdk.Model(req.Model)}
	if thinksByDefault(req.Model) {
		maxTokens += applyReasoning(&params, req.Model, req.Reasoning)
	} else if req.Temperature != nil {
		// Models that think by default reject sampling parameters.
		params.Temperature = sdk.Float(float64(*req.Temperature))
	}
	params.MaxTokens = int64(maxTokens)
	if sys := buildSystem(req); sys != nil {
		params.System = sys
	}

	// Conversation history: replay messages in order. The initial documents
	// are merged into the *first* user message; documents a tool call added
	// stay with that tool's result (see mapMessages).
	messages := mapMessages(req)
	if leadingDocBlocks := buildLeadingDocBlocks(req); len(leadingDocBlocks) > 0 {
		messages = prependDocs(messages, leadingDocBlocks)
	}
	if len(messages) == 0 {
		return sdk.MessageNewParams{}, errors.New("anthropic: at least one user message required")
	}
	params.Messages = messages

	if tools := buildTools(req.Tools); len(tools) > 0 {
		params.Tools = tools
		if req.ToolChoice == llm.ToolChoiceNone {
			none := sdk.NewToolChoiceNoneParam()
			params.ToolChoice = sdk.ToolChoiceUnionParam{OfNone: &none}
		}
	}

	return params, nil
}

// thinksByDefault reports whether the model reasons unless told otherwise and
// takes its steering through output_config.effort: every Claude model from
// the 5 generation on, including the Fable and Mythos lines. Claude 3.x and
// 4.x answer without thinking by default and partly reject these settings, so
// they are sent none of them. Unknown ids count as current: new models follow
// the current rules, the legacy list is closed.
func thinksByDefault(model string) bool {
	for _, legacy := range []string{"claude-opus-4", "claude-sonnet-4", "claude-haiku-4", "claude-3", "claude-2", "claude-instant"} {
		if strings.HasPrefix(model, legacy) {
			return false
		}
	}
	return true
}

// applyReasoning sets effort and thinking for a model that thinks by default
// and returns the max_tokens headroom the request needs.
//
// Answers run adaptive thinking at medium effort: the model skips thinking on
// simple lookups (same time to first token as with thinking off) and spends
// ~3s on multi-step questions. Minimal requests use low effort plus the
// model's off switch where it has one; measured against the live API on
// 2026-11-13, Haiku 5.5 otherwise spends the rewriter's whole 80-token cap on
// thinking and returns nothing.
func applyReasoning(params *sdk.MessageNewParams, model string, level llm.Reasoning) (reserve int) {
	if level != llm.ReasoningMinimal {
		params.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffortMedium}
		return answerThinkingReserve
	}
	params.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffortLow}
	switch {
	case strings.HasPrefix(model, "claude-sonnet-5-5"):
		// Sonnet 5.5 rejects "disabled"; between_tools is its off switch.
		off := sdk.NewThinkingConfigBetweenToolsParam()
		params.Thinking = sdk.ThinkingConfigParamUnion{OfBetweenTools: &off}
		return 0
	case strings.HasPrefix(model, "claude-haiku-5-5"):
		off := sdk.NewThinkingConfigDisabledParam()
		params.Thinking = sdk.ThinkingConfigParamUnion{OfDisabled: &off}
		return 0
	default:
		// Always-on models (Opus 5.5, Fable 5.2) and ids this table doesn't
		// know: low effort is the only lever, so leave room to think.
		return minimalThinkingReserve
	}
}

// buildSystem renders the system prompt as a single text block, or nil when
// there's no system prompt. The orchestrator decides where to put cache
// breakpoints.
func buildSystem(req llm.GenerateRequest) []sdk.TextBlockParam {
	if req.System == "" {
		return nil
	}
	sys := sdk.TextBlockParam{Text: req.System}
	if req.EnableCache {
		sys.CacheControl = sdk.NewCacheControlEphemeralParam()
	}
	return []sdk.TextBlockParam{sys}
}

// buildLeadingDocBlocks assembles the document, image, and PDF content blocks
// that get prepended to the first user message as a synthetic retrieved-context
// turn.
//
// Anthropic caps requests at 4 cache_control breakpoints. We only set
// cache_control on the LAST document — that single breakpoint marks the end of
// the cacheable docs prefix; everything before it gets cached too.
//
// Multi-modal images + PDFs are appended in the same synthetic user message so
// Claude reads them alongside the cited text. The orchestrator only populates
// these for capable models, so no capability check here.
func buildLeadingDocBlocks(req llm.GenerateRequest) []sdk.ContentBlockParamUnion {
	var blocks []sdk.ContentBlockParamUnion
	for i, doc := range req.Documents {
		isLast := i == len(req.Documents)-1
		block := docToBlock(doc, req.EnableCache && isLast)
		blocks = append(blocks, sdk.ContentBlockParamUnion{OfDocument: &block})
	}
	for _, doc := range req.Documents {
		for _, img := range doc.Images {
			blocks = append(blocks, ImageContentBlock(img))
		}
		for _, pdf := range doc.PDFs {
			blocks = append(blocks, PDFContentBlock(pdf))
		}
	}
	return blocks
}

// buildTools maps llm.Tool definitions to the SDK's tool params.
func buildTools(in []llm.Tool) []sdk.ToolUnionParam {
	if len(in) == 0 {
		return nil
	}
	tools := make([]sdk.ToolUnionParam, 0, len(in))
	for _, t := range in {
		tools = append(tools, sdk.ToolUnionParam{
			OfTool: &sdk.ToolParam{
				Name:        t.Name,
				Description: sdk.String(t.Description),
				InputSchema: sdk.ToolInputSchemaParam{
					Properties: schemaProperties(t.Schema),
					Required:   schemaRequired(t.Schema),
				},
			},
		})
	}
	return tools
}

// toolState tracks a streaming tool-use block's id+name so we know each tool
// call's identity and can mark Final on content_block_stop.
type toolState struct {
	id   string
	name string
}

// replay is this adapter's llm.Event.Replay payload: the content blocks of an
// assistant turn exactly as the API returned them, thinking blocks included.
type replay struct {
	blocks []sdk.ContentBlockParamUnion
}

// run drains the SDK stream and forwards events.
func (c *Client) run(ctx context.Context, params sdk.MessageNewParams, req llm.GenerateRequest, out chan<- llm.Event) {
	defer close(out)

	stream := c.api.Messages.NewStreaming(ctx, params)

	tools := make(map[int64]toolState)
	stopReason := llm.StopEnd
	var usage *llm.Usage
	// The accumulated message is what a tool round has to send back. Thinking
	// blocks arrive as a signature with no text, so they can't be rebuilt
	// from the events forwarded to the caller.
	var turn sdk.Message

	for stream.Next() {
		event := stream.Current()
		if err := turn.Accumulate(event); err != nil {
			out <- llm.Event{Kind: llm.EventError, Err: fmt.Errorf("anthropic: stream: %w", err)}
			return
		}
		switch v := event.AsAny().(type) {
		case sdk.ContentBlockStartEvent:
			if !handleBlockStart(ctx, v, tools, out) {
				return
			}
		case sdk.ContentBlockDeltaEvent:
			if !handleBlockDelta(ctx, v, tools, req.Documents, out) {
				return
			}
		case sdk.ContentBlockStopEvent:
			if !handleBlockStop(ctx, v, tools, out) {
				return
			}
		case sdk.MessageDeltaEvent:
			stopReason = mapStopReason(v.Delta.StopReason)
			usage = &llm.Usage{
				InputTokens:      int(v.Usage.InputTokens),
				OutputTokens:     int(v.Usage.OutputTokens),
				CacheReadTokens:  int(v.Usage.CacheReadInputTokens),
				CacheWriteTokens: int(v.Usage.CacheCreationInputTokens),
			}
		}
	}

	if err := stream.Err(); err != nil {
		out <- streamErrEvent(ctx, err)
		return
	}

	done := llm.Event{Kind: llm.EventDone, StopReason: stopReason, Usage: usage}
	if stopReason == llm.StopToolUse {
		done.Replay = replay{blocks: turn.ToParam().Content}
	}
	out <- done
}

// streamErrEvent maps a stream error to the terminal event: a cancelled Done
// when the context was cancelled or timed out, otherwise a wrapped Error.
func streamErrEvent(ctx context.Context, err error) llm.Event {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
	}
	return llm.Event{Kind: llm.EventError, Err: fmt.Errorf("anthropic: stream: %w", err)}
}

// handleBlockStart records a new tool-use block and emits its opening
// EventToolCall. Returns false if the send was cancelled.
func handleBlockStart(ctx context.Context, v sdk.ContentBlockStartEvent, tools map[int64]toolState, out chan<- llm.Event) bool {
	cb, ok := v.ContentBlock.AsAny().(sdk.ToolUseBlock)
	if !ok {
		return true
	}
	tools[v.Index] = toolState{id: cb.ID, name: cb.Name}
	return llm.SendOrCancel(ctx, out, llm.Event{
		Kind: llm.EventToolCall,
		ToolCall: &llm.ToolCallDelta{
			ID:       cb.ID,
			Name:     cb.Name,
			ArgsJSON: "",
		},
	})
}

// handleBlockDelta forwards a text, tool-arg, or citation delta. Returns false
// if the send was cancelled.
func handleBlockDelta(ctx context.Context, v sdk.ContentBlockDeltaEvent, tools map[int64]toolState, docs []llm.Document, out chan<- llm.Event) bool {
	switch d := v.Delta.AsAny().(type) {
	case sdk.TextDelta:
		if d.Text != "" {
			return llm.SendOrCancel(ctx, out, llm.Event{Kind: llm.EventText, TextDelta: d.Text})
		}
	case sdk.InputJSONDelta:
		if t, ok := tools[v.Index]; ok && d.PartialJSON != "" {
			return llm.SendOrCancel(ctx, out, llm.Event{
				Kind: llm.EventToolCall,
				ToolCall: &llm.ToolCallDelta{
					ID:       t.id,
					Name:     t.name,
					ArgsJSON: d.PartialJSON,
				},
			})
		}
	case sdk.CitationsDelta:
		if cit := mapCitation(d, docs); cit != nil {
			return llm.SendOrCancel(ctx, out, llm.Event{Kind: llm.EventCitation, Citation: cit})
		}
	}
	return true
}

// handleBlockStop marks a tool-use block Final and forgets it. Returns false if
// the send was cancelled.
func handleBlockStop(ctx context.Context, v sdk.ContentBlockStopEvent, tools map[int64]toolState, out chan<- llm.Event) bool {
	t, ok := tools[v.Index]
	if !ok {
		return true
	}
	if !llm.SendOrCancel(ctx, out, llm.Event{
		Kind: llm.EventToolCall,
		ToolCall: &llm.ToolCallDelta{
			ID:    t.id,
			Name:  t.name,
			Final: true,
		},
	}) {
		return false
	}
	delete(tools, v.Index)
	return true
}

// docToBlock wraps a Document as a citation-enabled custom-content document
// block so Claude emits structured citations tied to Document.ID.
func docToBlock(doc llm.Document, enableCache bool) sdk.DocumentBlockParam {
	// Each document is rendered as a custom-content block whose inner
	// content array is a single text block. Title carries Document.ID
	// (provider-prefixed by the orchestrator) so we can resolve citation
	// document_title back to the chunk handle.
	doc1 := sdk.DocumentBlockParam{
		Source: sdk.DocumentBlockParamSourceUnion{
			OfContent: &sdk.ContentBlockSourceParam{
				Content: sdk.ContentBlockSourceContentUnionParam{
					OfContentBlockSourceContent: []sdk.ContentBlockSourceContentItemUnionParam{
						sdk.ContentBlockSourceContentItemParamOfText(doc.Content),
					},
				},
			},
		},
		Title:     sdk.String(doc.ID),
		Citations: sdk.CitationsConfigParam{Enabled: sdk.Bool(true)},
	}
	if enableCache {
		doc1.CacheControl = sdk.NewCacheControlEphemeralParam()
	}
	if doc.Title != "" {
		// Anthropic's "context" is a free-form annotation Claude can read.
		// Pack source+title+date here for easy in-prompt provenance.
		doc1.Context = sdk.String(metaLine(doc))
	}
	return doc1
}

func metaLine(doc llm.Document) string {
	parts := make([]string, 0, 3)
	if doc.Source != "" {
		parts = append(parts, "source="+doc.Source)
	}
	if doc.Title != "" {
		parts = append(parts, "title="+doc.Title)
	}
	if doc.Date != "" {
		parts = append(parts, "date="+doc.Date)
	}
	return strings.Join(parts, " ")
}

// mapMessages maps llm.Messages → Anthropic MessageParams. Tool turns become
// user messages carrying tool_result blocks; assistant turns with tool_calls
// become assistant messages with tool_use blocks.
func mapMessages(req llm.GenerateRequest) []sdk.MessageParam {
	out := make([]sdk.MessageParam, 0, len(req.Messages))
	shown := newShownDocs(req.Documents)
	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleUser:
			out = append(out, mapUserMessage(m))
		case llm.RoleAssistant:
			out = append(out, mapAssistantMessage(m))
		case llm.RoleTool:
			// Tool turns are rendered as user messages on Anthropic with
			// a single tool_result block.
			out = append(out, sdk.NewUserMessage(toolResultBlock(m, shown)))
		case llm.RoleSystem:
			// System content goes through GenerateRequest.System; ignore here.
		}
	}
	return out
}

// shownDocs records which documents and media the request has already put in
// front of the model, so each tool result adds only what is new.
type shownDocs struct {
	docs  map[string]struct{}
	media map[string]struct{}
}

func newShownDocs(initial []llm.Document) *shownDocs {
	s := &shownDocs{docs: map[string]struct{}{}, media: map[string]struct{}{}}
	for _, d := range initial {
		s.docs[d.ID] = struct{}{}
		for _, img := range d.Images {
			s.firstMedia(d.ID, img.SourceID)
		}
		for _, pdf := range d.PDFs {
			s.firstMedia(d.ID, pdf.SourceID)
		}
	}
	return s
}

// firstMedia reports whether this is the first sighting of a document's
// media item. Media without a SourceID can't be told apart, so it always
// counts as new — the same rule llm.MergeDocuments applies.
func (s *shownDocs) firstMedia(docID, sourceID string) bool {
	if sourceID == "" {
		return true
	}
	key := docID + "\x00" + sourceID
	if _, seen := s.media[key]; seen {
		return false
	}
	s.media[key] = struct{}{}
	return true
}

// toolResultBlock renders a tool turn: its text, then the documents and media
// the call returned that the model hasn't been shown yet. They sit here, not
// with the initial documents at the front of the request, because inserting
// anything ahead of the model's earlier thinking invalidates that thinking
// (a 400 on Fable 5.2). Citations work the same from either place.
func toolResultBlock(m llm.Message, shown *shownDocs) sdk.ContentBlockParamUnion {
	var content []sdk.ToolResultBlockParamContentUnion
	if m.Content != "" {
		content = append(content, sdk.ToolResultBlockParamContentUnion{OfText: &sdk.TextBlockParam{Text: m.Content}})
	}
	for _, doc := range m.Documents {
		if _, seen := shown.docs[doc.ID]; !seen {
			shown.docs[doc.ID] = struct{}{}
			block := docToBlock(doc, false)
			content = append(content, sdk.ToolResultBlockParamContentUnion{OfDocument: &block})
		}
		for _, img := range doc.Images {
			if shown.firstMedia(doc.ID, img.SourceID) {
				content = append(content, sdk.ToolResultBlockParamContentUnion{OfImage: ImageContentBlock(img).OfImage})
			}
		}
		for _, pdf := range doc.PDFs {
			if shown.firstMedia(doc.ID, pdf.SourceID) {
				content = append(content, sdk.ToolResultBlockParamContentUnion{OfDocument: PDFContentBlock(pdf).OfDocument})
			}
		}
	}
	return sdk.ContentBlockParamUnion{OfToolResult: &sdk.ToolResultBlockParam{
		ToolUseID: m.ToolCallID,
		Content:   content,
	}}
}

// mapUserMessage maps a user turn to an Anthropic user message.
func mapUserMessage(m llm.Message) sdk.MessageParam {
	blocks := []sdk.ContentBlockParamUnion{}
	if m.Content != "" {
		blocks = append(blocks, sdk.NewTextBlock(m.Content))
	}
	return sdk.NewUserMessage(blocks...)
}

// mapAssistantMessage maps an assistant turn, including any tool_use blocks.
// A turn that carries this adapter's replay goes back verbatim.
func mapAssistantMessage(m llm.Message) sdk.MessageParam {
	if r, ok := m.Replay.(replay); ok && len(r.blocks) > 0 {
		return sdk.NewAssistantMessage(r.blocks...)
	}
	blocks := []sdk.ContentBlockParamUnion{}
	if m.Content != "" {
		blocks = append(blocks, sdk.NewTextBlock(m.Content))
	}
	for _, tc := range m.ToolCalls {
		input := tc.ArgsJSON
		if input == "" {
			input = "{}"
		}
		// NewToolUseBlock takes `input any` and the SDK runs it
		// through json.Marshal. Passing raw []byte would
		// base64-encode it as a string ("Input should be a
		// valid dictionary" 400 from Anthropic on round 2 of
		// any tool-use turn). json.RawMessage embeds the
		// already-formed JSON verbatim, which is what the
		// API expects for tool_use.input.
		blocks = append(blocks, sdk.NewToolUseBlock(tc.ID, json.RawMessage(input), tc.Name))
	}
	return sdk.NewAssistantMessage(blocks...)
}

// prependDocs inserts the document blocks into the first user message so the
// document_index referenced in citations is stable from index 0.
func prependDocs(messages []sdk.MessageParam, docs []sdk.ContentBlockParamUnion) []sdk.MessageParam {
	if len(messages) == 0 {
		return []sdk.MessageParam{sdk.NewUserMessage(docs...)}
	}
	for i, m := range messages {
		if m.Role == sdk.MessageParamRoleUser {
			merged := append([]sdk.ContentBlockParamUnion{}, docs...)
			merged = append(merged, m.Content...)
			messages[i] = sdk.MessageParam{Role: sdk.MessageParamRoleUser, Content: merged}
			return messages
		}
	}
	// No user message present — insert one at the front.
	prefix := []sdk.MessageParam{sdk.NewUserMessage(docs...)}
	return append(prefix, messages...)
}

// mapCitation maps an Anthropic CitationsDelta to llm.Citation. Returns nil
// when the citation variant doesn't tie back to a Document we sent (e.g. web
// search results — not used in the Nexus RAG flow).
func mapCitation(d sdk.CitationsDelta, _ []llm.Document) *llm.Citation {
	switch v := d.Citation.AsAny().(type) {
	case sdk.CitationCharLocation:
		return &llm.Citation{
			DocID:     v.DocumentTitle, // Title carries Document.ID
			CitedText: v.CitedText,
			SpanStart: int(v.StartCharIndex),
			SpanEnd:   int(v.EndCharIndex),
		}
	case sdk.CitationPageLocation:
		return &llm.Citation{
			DocID:     v.DocumentTitle,
			CitedText: v.CitedText,
			SpanStart: int(v.StartPageNumber),
			SpanEnd:   int(v.EndPageNumber),
		}
	case sdk.CitationContentBlockLocation:
		return &llm.Citation{
			DocID:     v.DocumentTitle,
			CitedText: v.CitedText,
			SpanStart: int(v.StartBlockIndex),
			SpanEnd:   int(v.EndBlockIndex),
		}
	}
	return nil
}

func mapStopReason(r sdk.StopReason) llm.StopReason {
	switch r {
	case sdk.StopReasonEndTurn:
		return llm.StopEnd
	case sdk.StopReasonToolUse:
		return llm.StopToolUse
	case sdk.StopReasonMaxTokens, sdk.StopReasonModelContextWindowExceeded:
		// Both mean the answer was cut short by a token limit.
		return llm.StopMaxTokens
	case sdk.StopReasonRefusal:
		return llm.StopFiltered
	default:
		return llm.StopEnd
	}
}

// schemaProperties / schemaRequired pull the two fields the SDK needs out of
// a generic JSON-Schema map. The SDK marshals "type": "object" automatically.
func schemaProperties(s map[string]any) any {
	if s == nil {
		return nil
	}
	if p, ok := s["properties"]; ok {
		return p
	}
	return s
}

func schemaRequired(s map[string]any) []string {
	if s == nil {
		return nil
	}
	r, ok := s["required"].([]string)
	if ok {
		return r
	}
	if anyR, ok := s["required"].([]any); ok {
		out := make([]string, 0, len(anyR))
		for _, v := range anyR {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ImageContentBlock builds an image content block from raw bytes. The
// orchestrator calls this in Phase 6 when attaching cached images to a turn.
// Exposed (rather than inlined) so the Phase 6 multimodal selection logic
// can map llm.Image → SDK block without re-importing base64.
func ImageContentBlock(img llm.Image) sdk.ContentBlockParamUnion {
	return sdk.NewImageBlockBase64(img.MediaType, base64.StdEncoding.EncodeToString(img.Data))
}

// PDFContentBlock builds a native-PDF document block from raw bytes (Phase
// 6b). Claude reads the PDF's text AND renders its pages, so charts/scans/
// signatures that text extraction loses become visible. Title carries
// pdf.SourceID (the chunk handle) so a citation Claude emits against this PDF
// back-maps to its evidence chunk — mapCitation resolves DocID from the block
// Title, exactly as docToBlock does. The human-readable filename moves to the
// free-form Context annotation for provenance.
//
// Citations MUST be enabled to match the text document blocks: Anthropic
// rejects a request that mixes citations-enabled and citations-disabled
// `document` blocks ("Citations must be either enabled or disabled on all
// document blocks"). Since every retrieved-text block enables citations,
// the PDF block must too.
func PDFContentBlock(pdf llm.PDF) sdk.ContentBlockParamUnion {
	block := sdk.DocumentBlockParam{
		Source: sdk.DocumentBlockParamSourceUnion{
			OfBase64: &sdk.Base64PDFSourceParam{Data: base64.StdEncoding.EncodeToString(pdf.Data)},
		},
		Citations: sdk.CitationsConfigParam{Enabled: sdk.Bool(true)},
		// Chunk handle, mirroring docToBlock (Title=doc.ID). SourceID is always
		// populated on this path, so no guard — an empty Title would recreate
		// the unresolvable-citation-pill bug this fixes.
		Title: sdk.String(pdf.SourceID),
	}
	if pdf.Filename != "" {
		block.Context = sdk.String("filename=" + pdf.Filename)
	}
	return sdk.ContentBlockParamUnion{OfDocument: &block}
}
