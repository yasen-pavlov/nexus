// Package anthropic implements the llm.Generator for Claude models via the
// official anthropic-sdk-go.
//
// The adapter is the only one that emits llm.EventCitation natively, mapping
// the SDK's citations_delta events to Citation events tied to the Document.ID
// the orchestrator originally provided.
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

	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: int64(maxTokens),
	}
	if req.Temperature != nil {
		params.Temperature = sdk.Float(float64(*req.Temperature))
	}

	// System prompt as a single text block; the orchestrator decides where
	// to put cache breakpoints.
	if req.System != "" {
		sys := sdk.TextBlockParam{Text: req.System}
		if req.EnableCache {
			sys.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		params.System = []sdk.TextBlockParam{sys}
	}

	// Documents are passed as a *user* message with custom-content document
	// blocks so Anthropic can emit citations bound to chunk titles. We
	// prepend them to the conversation as a synthetic first user turn —
	// this matches how the API expects retrieved-context to flow.
	//
	// Anthropic caps requests at 4 cache_control breakpoints. We only set
	// cache_control on the LAST document — that single breakpoint marks the
	// end of the cacheable docs prefix; everything before it gets cached too.
	var leadingDocBlocks []sdk.ContentBlockParamUnion
	for i, doc := range req.Documents {
		isLast := i == len(req.Documents)-1
		block := docToBlock(doc, req.EnableCache && isLast)
		leadingDocBlocks = append(leadingDocBlocks, block)
	}

	// Conversation history: replay messages in order. Documents (if any)
	// are merged into the *first* user message so the citation index
	// reflects document_index 0..N-1 from the start.
	messages := mapMessages(req.Messages)
	if len(leadingDocBlocks) > 0 {
		messages = prependDocs(messages, leadingDocBlocks)
	}
	if len(messages) == 0 {
		return sdk.MessageNewParams{}, errors.New("anthropic: at least one user message required")
	}
	params.Messages = messages

	if len(req.Tools) > 0 {
		tools := make([]sdk.ToolUnionParam, 0, len(req.Tools))
		for _, t := range req.Tools {
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
		params.Tools = tools
	}

	return params, nil
}

// run drains the SDK stream and forwards events.
func (c *Client) run(ctx context.Context, params sdk.MessageNewParams, req llm.GenerateRequest, out chan<- llm.Event) {
	defer close(out)

	stream := c.api.Messages.NewStreaming(ctx, params)

	// Per-block accumulators for tool-use blocks so we know each tool
	// call's id+name and can mark Final on content_block_stop.
	type toolState struct {
		id   string
		name string
	}
	tools := make(map[int64]toolState)

	stopReason := llm.StopEnd
	var usage *llm.Usage

	for stream.Next() {
		ev := stream.Current()
		switch v := ev.AsAny().(type) {
		case sdk.MessageStartEvent:
			// Usage start is reported again in MessageDeltaEvent; nothing to do.
			_ = v
		case sdk.ContentBlockStartEvent:
			if cb, ok := v.ContentBlock.AsAny().(sdk.ToolUseBlock); ok {
				tools[v.Index] = toolState{id: cb.ID, name: cb.Name}
				if !sendOrCancel(ctx, out, llm.Event{
					Kind: llm.EventToolCall,
					ToolCall: &llm.ToolCallDelta{
						ID:       cb.ID,
						Name:     cb.Name,
						ArgsJSON: "",
					},
				}) {
					return
				}
			}
		case sdk.ContentBlockDeltaEvent:
			switch d := v.Delta.AsAny().(type) {
			case sdk.TextDelta:
				if d.Text != "" {
					if !sendOrCancel(ctx, out, llm.Event{Kind: llm.EventText, TextDelta: d.Text}) {
						return
					}
				}
			case sdk.InputJSONDelta:
				if t, ok := tools[v.Index]; ok && d.PartialJSON != "" {
					if !sendOrCancel(ctx, out, llm.Event{
						Kind: llm.EventToolCall,
						ToolCall: &llm.ToolCallDelta{
							ID:       t.id,
							Name:     t.name,
							ArgsJSON: d.PartialJSON,
						},
					}) {
						return
					}
				}
			case sdk.CitationsDelta:
				if cit := mapCitation(d, req.Documents); cit != nil {
					if !sendOrCancel(ctx, out, llm.Event{Kind: llm.EventCitation, Citation: cit}) {
						return
					}
				}
			}
		case sdk.ContentBlockStopEvent:
			if t, ok := tools[v.Index]; ok {
				if !sendOrCancel(ctx, out, llm.Event{
					Kind: llm.EventToolCall,
					ToolCall: &llm.ToolCallDelta{
						ID:    t.id,
						Name:  t.name,
						Final: true,
					},
				}) {
					return
				}
				delete(tools, v.Index)
			}
		case sdk.MessageDeltaEvent:
			stopReason = mapStopReason(v.Delta.StopReason)
			usage = &llm.Usage{
				InputTokens:      int(v.Usage.InputTokens),
				OutputTokens:     int(v.Usage.OutputTokens),
				CacheReadTokens:  int(v.Usage.CacheReadInputTokens),
				CacheWriteTokens: int(v.Usage.CacheCreationInputTokens),
			}
		case sdk.MessageStopEvent:
			// Terminal; loop exits naturally.
			_ = v
		}
	}

	if err := stream.Err(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			out <- llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
			return
		}
		out <- llm.Event{Kind: llm.EventError, Err: fmt.Errorf("anthropic: stream: %w", err)}
		return
	}

	out <- llm.Event{Kind: llm.EventDone, StopReason: stopReason, Usage: usage}
}

// docToBlock wraps a Document as a citation-enabled custom-content document
// block so Claude emits structured citations tied to Document.ID.
func docToBlock(doc llm.Document, enableCache bool) sdk.ContentBlockParamUnion {
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
	return sdk.ContentBlockParamUnion{OfDocument: &doc1}
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
func mapMessages(in []llm.Message) []sdk.MessageParam {
	out := make([]sdk.MessageParam, 0, len(in))
	for _, m := range in {
		switch m.Role {
		case llm.RoleUser:
			blocks := []sdk.ContentBlockParamUnion{}
			if m.Content != "" {
				blocks = append(blocks, sdk.NewTextBlock(m.Content))
			}
			out = append(out, sdk.NewUserMessage(blocks...))
		case llm.RoleAssistant:
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
			out = append(out, sdk.NewAssistantMessage(blocks...))
		case llm.RoleTool:
			// Tool turns are rendered as user messages on Anthropic with
			// a single tool_result block.
			block := sdk.NewToolResultBlock(m.ToolCallID, m.Content, false)
			out = append(out, sdk.NewUserMessage(block))
		case llm.RoleSystem:
			// System content goes through GenerateRequest.System; ignore here.
		}
	}
	return out
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
	case sdk.StopReasonMaxTokens:
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

// sendOrCancel writes ev to out unless ctx is cancelled. Returns false to
// signal the goroutine should bail out and close the channel.
func sendOrCancel(ctx context.Context, out chan<- llm.Event, ev llm.Event) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		out <- llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
		return false
	}
}

// ImageContentBlock builds an image content block from raw bytes. The
// orchestrator calls this in Phase 6 when attaching cached images to a turn.
// Exposed (rather than inlined) so the Phase 6 multimodal selection logic
// can map llm.Image → SDK block without re-importing base64.
func ImageContentBlock(img llm.Image) sdk.ContentBlockParamUnion {
	return sdk.NewImageBlockBase64(img.MediaType, base64.StdEncoding.EncodeToString(img.Data))
}
