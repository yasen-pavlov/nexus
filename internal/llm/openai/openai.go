// Package openai implements the llm.Generator for OpenAI chat-completion
// models via the official openai-go SDK.
//
// OpenAI has no native citation API; the orchestrator's [N] parser is what
// turns inline markers into llm.Citation events. This adapter only emits
// EventText, EventToolCall, EventDone, and EventError.
package openai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/muty/nexus/internal/llm"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"go.uber.org/zap"
)

const defaultMaxTokens = 4096

// Client is an llm.Generator backed by openai-go.
type Client struct {
	api sdk.Client
	log *zap.Logger
}

// New constructs a client. Pass empty baseURL for production.
func New(apiKey, baseURL string, log *zap.Logger) *Client {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Client{api: sdk.NewClient(opts...), log: log}
}

// Generate kicks off a streaming chat-completion request.
func (c *Client) Generate(ctx context.Context, req llm.GenerateRequest) (<-chan llm.Event, error) {
	if req.Model == "" {
		return nil, errors.New("openai: model required")
	}

	params, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}

	out := make(chan llm.Event, 16)
	go c.run(ctx, params, out)
	return out, nil
}

func (c *Client) buildParams(req llm.GenerateRequest) (sdk.ChatCompletionNewParams, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	params := sdk.ChatCompletionNewParams{
		Model:               shared.ChatModel(req.Model),
		MaxCompletionTokens: sdk.Int(int64(maxTokens)),
	}
	if req.Temperature != nil {
		params.Temperature = sdk.Float(float64(*req.Temperature))
	}
	// Surface usage stats in the stream's terminal chunk.
	params.StreamOptions = sdk.ChatCompletionStreamOptionsParam{
		IncludeUsage: sdk.Bool(true),
	}

	messages := buildMessages(req)
	if len(messages) == 0 {
		return sdk.ChatCompletionNewParams{}, errors.New("openai: at least one message required")
	}
	params.Messages = messages

	if tools := buildTools(req.Tools); len(tools) > 0 {
		params.Tools = tools
	}

	return params, nil
}

// buildMessages assembles the full message list: an optional system message
// carrying the retrieved-context document block, followed by the mapped
// conversation history.
//
// The orchestrator's [N] parser pulls citations out of generated text by
// mapping back to the order documents appear in the system block.
func buildMessages(req llm.GenerateRequest) []sdk.ChatCompletionMessageParamUnion {
	messages := make([]sdk.ChatCompletionMessageParamUnion, 0, len(req.Messages)+2)

	if req.System != "" || len(req.Documents) > 0 {
		systemBody := req.System
		if len(req.Documents) > 0 {
			systemBody += "\n\n" + llm.RenderDocumentsBlock(req.Documents)
		}
		messages = append(messages, sdk.SystemMessage(systemBody))
	}

	// Multi-modal: images + PDFs ride on the LAST user message as content
	// parts (image_url / file), so they sit with the question the model is
	// answering. The orchestrator only attaches these for capable models.
	images := llm.CollectImages(req.Documents)
	pdfs := llm.CollectPDFs(req.Documents)
	lastUser := llm.LastUserIndex(req.Messages)
	for i, m := range req.Messages {
		switch m.Role {
		case llm.RoleSystem:
			// Already collapsed into the synthetic system message above.
		case llm.RoleUser:
			messages = append(messages, userMessage(m, i == lastUser, images, pdfs))
		case llm.RoleAssistant:
			messages = append(messages, assistantMessage(m))
		case llm.RoleTool:
			messages = append(messages, sdk.ToolMessage(m.Content, m.ToolCallID))
		}
	}
	return messages
}

// userMessage builds a user message. When this is the last user turn and media
// is attached, the content becomes a text+image+file content-part array;
// otherwise it's a plain text message.
func userMessage(m llm.Message, isLast bool, images []llm.Image, pdfs []llm.PDF) sdk.ChatCompletionMessageParamUnion {
	if !isLast || (len(images) == 0 && len(pdfs) == 0) {
		return sdk.UserMessage(m.Content)
	}
	parts := make([]sdk.ChatCompletionContentPartUnionParam, 0, len(images)+len(pdfs)+1)
	parts = append(parts, sdk.TextContentPart(m.Content))
	for _, img := range images {
		parts = append(parts, ImageContentBlock(img))
	}
	for _, pdf := range pdfs {
		parts = append(parts, PDFContentBlock(pdf))
	}
	return sdk.UserMessage(parts)
}

// buildTools maps llm.Tool definitions to OpenAI tool params.
func buildTools(in []llm.Tool) []sdk.ChatCompletionToolUnionParam {
	if len(in) == 0 {
		return nil
	}
	tools := make([]sdk.ChatCompletionToolUnionParam, 0, len(in))
	for _, t := range in {
		fn := shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: sdk.String(t.Description),
			Parameters:  shared.FunctionParameters(t.Schema),
		}
		tools = append(tools, sdk.ChatCompletionFunctionTool(fn))
	}
	return tools
}

// run drains the SDK stream into the unified event channel.
func (c *Client) run(ctx context.Context, params sdk.ChatCompletionNewParams, out chan<- llm.Event) {
	defer close(out)

	stream := c.api.Chat.Completions.NewStreaming(ctx, params)
	acc := sdk.ChatCompletionAccumulator{}
	stopReason := llm.StopEnd
	var usage *llm.Usage

	for stream.Next() {
		chunk := stream.Current()
		if !acc.AddChunk(chunk) {
			out <- llm.Event{Kind: llm.EventError, Err: errors.New("openai: chunk mismatch")}
			return
		}

		if !emitTextDelta(ctx, chunk, &stopReason, out) {
			return
		}
		if !emitFinishedToolCall(ctx, &acc, out) {
			return
		}
	}

	if err := stream.Err(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			out <- llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
			return
		}
		out <- llm.Event{Kind: llm.EventError, Err: fmt.Errorf("openai: stream: %w", err)}
		return
	}

	if acc.Usage.TotalTokens > 0 {
		usage = &llm.Usage{
			InputTokens:     int(acc.Usage.PromptTokens),
			OutputTokens:    int(acc.Usage.CompletionTokens),
			CacheReadTokens: int(acc.Usage.PromptTokensDetails.CachedTokens),
		}
	}
	out <- llm.Event{Kind: llm.EventDone, StopReason: stopReason, Usage: usage}
}

// emitTextDelta forwards the chunk's text delta (if any) and updates stopReason
// from the finish_reason. Returns false if a send was cancelled.
//
// NB: we deliberately do NOT forward the raw streamed tool-call fragments here.
// OpenAI streams parallel tool calls with the id+name only on the FIRST
// fragment per `index` and empty-id continuation fragments carrying just
// argument text — forwarding those makes the orchestrator (which buffers by id)
// synthesise phantom calls with empty names. Instead we let the SDK accumulator
// reassemble each call and emit it once, complete, in emitFinishedToolCall.
func emitTextDelta(ctx context.Context, chunk sdk.ChatCompletionChunk, stopReason *llm.StopReason, out chan<- llm.Event) bool {
	if len(chunk.Choices) == 0 {
		return true
	}
	ch := chunk.Choices[0]
	if ch.Delta.Content != "" {
		if !llm.SendOrCancel(ctx, out, llm.Event{Kind: llm.EventText, TextDelta: ch.Delta.Content}) {
			return false
		}
	}
	if string(ch.FinishReason) != "" {
		*stopReason = mapFinishReason(string(ch.FinishReason))
	}
	return true
}

// emitFinishedToolCall emits each tool call exactly once, fully assembled (id +
// name + complete arguments), the moment the accumulator reports it done.
// Returns false if a send was cancelled.
func emitFinishedToolCall(ctx context.Context, acc *sdk.ChatCompletionAccumulator, out chan<- llm.Event) bool {
	tc, ok := acc.JustFinishedToolCall()
	if !ok {
		return true
	}
	return llm.SendOrCancel(ctx, out, llm.Event{
		Kind: llm.EventToolCall,
		ToolCall: &llm.ToolCallDelta{
			ID:       tc.ID,
			Name:     tc.Name,
			ArgsJSON: tc.Arguments,
			Final:    true,
		},
	})
}

// buildDocumentsBlock renders retrieved chunks as XML-ish blocks the model can
// reason over. Includes a numeric index so [N] citations map back; a metadata
// line with source/title/date; and the chunk content. The orchestrator owns
// turning [N] back into a Citation tied to Document.ID.
// mapFinishReason maps OpenAI's finish_reason strings to llm.StopReason.
func mapFinishReason(r string) llm.StopReason {
	switch r {
	case "stop":
		return llm.StopEnd
	case "tool_calls":
		return llm.StopToolUse
	case "length":
		return llm.StopMaxTokens
	case "content_filter":
		return llm.StopFiltered
	default:
		return llm.StopEnd
	}
}

// ImageContentBlock is exposed for Phase 6 multi-modal use. OpenAI accepts
// images as image_url with a data URI; the orchestrator builds a content-part
// array that mixes text + image parts.
func ImageContentBlock(img llm.Image) sdk.ChatCompletionContentPartUnionParam {
	dataURI := fmt.Sprintf("data:%s;base64,%s", img.MediaType, base64.StdEncoding.EncodeToString(img.Data))
	return sdk.ChatCompletionContentPartUnionParam{
		OfImageURL: &sdk.ChatCompletionContentPartImageParam{
			ImageURL: sdk.ChatCompletionContentPartImageImageURLParam{URL: dataURI},
		},
	}
}

// assistantMessage builds an assistant message, carrying tool_calls when
// the turn issued any. OpenAI rejects a request where a `tool` message
// doesn't follow an assistant message that declares the matching
// tool_calls ("messages with role 'tool' must be a response to a
// preceding message with 'tool_calls'"), so a multi-round tool turn MUST
// re-emit the assistant's tool_calls — sdk.AssistantMessage(content) alone
// drops them.
func assistantMessage(m llm.Message) sdk.ChatCompletionMessageParamUnion {
	if len(m.ToolCalls) == 0 {
		return sdk.AssistantMessage(m.Content)
	}
	asst := sdk.ChatCompletionAssistantMessageParam{}
	if m.Content != "" {
		asst.Content.OfString = sdk.String(m.Content)
	}
	for _, tc := range m.ToolCalls {
		asst.ToolCalls = append(asst.ToolCalls, sdk.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &sdk.ChatCompletionMessageFunctionToolCallParam{
				ID: tc.ID,
				Function: sdk.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      tc.Name,
					Arguments: tc.ArgsJSON,
				},
			},
		})
	}
	return sdk.ChatCompletionMessageParamUnion{OfAssistant: &asst}
}

// PDFContentBlock builds a native-PDF file content part (Phase 6b). OpenAI
// accepts a base64 `file_data` data URI plus a filename; the model extracts
// the text and renders pages so visual content is visible.
func PDFContentBlock(pdf llm.PDF) sdk.ChatCompletionContentPartUnionParam {
	dataURI := fmt.Sprintf("data:application/pdf;base64,%s", base64.StdEncoding.EncodeToString(pdf.Data))
	filename := pdf.Filename
	if filename == "" {
		filename = "document.pdf"
	}
	return sdk.FileContentPart(sdk.ChatCompletionContentPartFileFileParam{
		FileData: sdk.String(dataURI),
		Filename: sdk.String(filename),
	})
}
