// Package openai implements the llm.Generator for OpenAI models on the
// Responses API (streaming), via the official openai-go SDK.
//
// Requests are stateless (store: false): the conversation is resent every
// round, and reasoning survives a tool loop because the model's output items
// (including encrypted reasoning) travel back verbatim through
// llm.Event.Replay / llm.Message.Replay.
//
// OpenAI has no native citation API; the orchestrator's [N] parser is what
// turns inline markers into llm.Citation events. This adapter only emits
// EventText, EventToolCall, EventDone, and EventError.
package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/muty/nexus/internal/llm"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"go.uber.org/zap"
)

const (
	defaultMaxTokens = 4096

	// Reasoning tokens count against max_output_tokens, so a reasoning model
	// needs room to think on top of the caller's cap on the visible answer.
	reasoningHeadroomDefault = 4096
	// Minimal-effort calls (rewrites, titles) reason little but not zero
	// unless the effort resolves to "none"; without headroom their 24–80
	// token caps would be spent before any visible output.
	reasoningHeadroomMinimal = 1024
)

// replayItems is what this adapter puts in llm.Event.Replay: the raw JSON of
// every output item of a response that stopped for tool calls, in output
// order. It is sent back byte-for-byte as input items on the next round.
type replayItems []json.RawMessage

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

// Generate kicks off a streaming Responses API request.
func (c *Client) Generate(ctx context.Context, req llm.GenerateRequest) (<-chan llm.Event, error) {
	if req.Model == "" {
		return nil, errors.New("openai: model required")
	}

	params, err := buildParams(req)
	if err != nil {
		return nil, err
	}

	out := make(chan llm.Event, 16)
	go c.run(ctx, params, out)
	return out, nil
}

// modelReasoning describes how one model family takes reasoning settings.
type modelReasoning struct {
	// reasons is false for models without hidden reasoning; they take no
	// reasoning settings and are the only ones that accept temperature.
	reasons bool
	// minimal is the effort sent for llm.ReasoningMinimal ("" = send none).
	minimal shared.ReasoningEffort
}

// snapshotSuffix matches the dated-snapshot tail of a model id
// ("gpt-5-mini-2025-08-07").
var snapshotSuffix = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

// reasoningFor maps a bare model id to its reasoning behaviour.
//
// Source: OpenAI model pages and the latest-model guide
// (developers.openai.com/api/docs/guides/latest-model), checked 2026-11-13.
// The lowest effort each model accepts differs: GPT-6 Astra and GPT-6.1 Sol
// reject "none" (the guide says to use "low"), the GPT-5 family predates
// "none" and takes "minimal". ReasoningDefault sends no effort at all because
// the provider default is medium on every current model. Unknown ids are
// treated as reasoning models with no effort override, the safe choice for
// future models: no temperature (rejected unless effort is "none") and full
// headroom.
func reasoningFor(model string) modelReasoning {
	if strings.HasPrefix(model, "gpt-4.1") || strings.HasPrefix(model, "gpt-4o") {
		return modelReasoning{reasons: false}
	}
	switch snapshotSuffix.ReplaceAllString(model, "") {
	case "gpt-6.1-terra", "gpt-6-luna", "gpt-6-sol":
		return modelReasoning{reasons: true, minimal: shared.ReasoningEffortNone}
	case "gpt-6-astra", "gpt-6.1-sol":
		return modelReasoning{reasons: true, minimal: shared.ReasoningEffortLow}
	case "gpt-5", "gpt-5-mini", "gpt-5-nano":
		return modelReasoning{reasons: true, minimal: shared.ReasoningEffortMinimal}
	default:
		return modelReasoning{reasons: true}
	}
}

func buildParams(req llm.GenerateRequest) (responses.ResponseNewParams, error) {
	docs := llm.AllDocuments(req)

	input := buildInput(req, docs)
	if len(input) == 0 {
		return responses.ResponseNewParams{}, errors.New("openai: at least one message required")
	}

	params := responses.ResponseNewParams{
		Model: req.Model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		// Stateless: nothing is kept server-side, so reasoning has to come
		// back to us encrypted to survive a tool round.
		Store:   sdk.Bool(false),
		Include: []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},
	}

	// The orchestrator's [N] parser maps citations back to the order
	// documents appear in this block.
	instructions := req.System
	if len(docs) > 0 {
		instructions += "\n\n" + llm.RenderDocumentsBlock(docs)
	}
	if instructions != "" {
		params.Instructions = sdk.String(instructions)
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	mr := reasoningFor(req.Model)
	switch {
	case !mr.reasons:
		if req.Temperature != nil {
			params.Temperature = sdk.Float(float64(*req.Temperature))
		}
	case req.Reasoning == llm.ReasoningMinimal:
		if mr.minimal != "" {
			params.Reasoning = shared.ReasoningParam{Effort: mr.minimal}
		}
		if mr.minimal != shared.ReasoningEffortNone {
			maxTokens += reasoningHeadroomMinimal
		}
	default:
		maxTokens += reasoningHeadroomDefault
	}
	params.MaxOutputTokens = sdk.Int(int64(maxTokens))

	if tools := buildTools(req.Tools); len(tools) > 0 {
		params.Tools = tools
		if req.ToolChoice == llm.ToolChoiceNone {
			params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
				OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsNone),
			}
		}
	}

	return params, nil
}

// buildInput maps the conversation history to Responses input items.
func buildInput(req llm.GenerateRequest, docs []llm.Document) responses.ResponseInputParam {
	// Multi-modal: images + PDFs ride on the LAST user message as content
	// parts, so they sit with the question the model is answering. The
	// orchestrator only attaches these for capable models.
	images := llm.CollectImages(docs)
	pdfs := llm.CollectPDFs(docs)
	lastUser := llm.LastUserIndex(req.Messages)

	input := make(responses.ResponseInputParam, 0, len(req.Messages)+2)
	for i, m := range req.Messages {
		switch m.Role {
		case llm.RoleSystem:
			// Already in instructions.
		case llm.RoleUser:
			input = append(input, userItem(m, i == lastUser, images, pdfs))
		case llm.RoleAssistant:
			input = appendAssistantItems(input, m)
		case llm.RoleTool:
			item := responses.ResponseInputItemParamOfFunctionCallOutput(m.Content)
			item.OfFunctionCallOutput.CallID = sdk.String(m.ToolCallID)
			input = append(input, item)
		}
	}
	return input
}

// userItem builds a user message. When this is the last user turn and media
// is attached, the content becomes a text+image+file part list; otherwise
// it's a plain text message.
func userItem(m llm.Message, isLast bool, images []llm.Image, pdfs []llm.PDF) responses.ResponseInputItemUnionParam {
	if !isLast || (len(images) == 0 && len(pdfs) == 0) {
		return responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRoleUser)
	}
	parts := make(responses.ResponseInputMessageContentListParam, 0, len(images)+len(pdfs)+1)
	parts = append(parts, responses.ResponseInputContentParamOfInputText(m.Content))
	for _, img := range images {
		parts = append(parts, imagePart(img))
	}
	for _, pdf := range pdfs {
		parts = append(parts, pdfPart(pdf))
	}
	return responses.ResponseInputItemParamOfMessage(parts, responses.EasyInputMessageRoleUser)
}

// appendAssistantItems appends one assistant turn. A turn that carries this
// adapter's replay goes back exactly as the model produced it — reasoning
// items are only valid next to the items they were generated with. Anything
// else (persisted history, a turn another provider produced) is rebuilt from
// Content + ToolCalls; the API pairs each function_call_output with a
// function_call by call_id, so the calls must be re-sent.
func appendAssistantItems(input responses.ResponseInputParam, m llm.Message) responses.ResponseInputParam {
	if items, ok := m.Replay.(replayItems); ok && len(items) > 0 {
		for _, raw := range items {
			input = append(input, param.Override[responses.ResponseInputItemUnionParam](raw))
		}
		return input
	}
	if m.Content != "" {
		input = append(input, responses.ResponseInputItemParamOfMessage(m.Content, responses.EasyInputMessageRoleAssistant))
	}
	for _, tc := range m.ToolCalls {
		input = append(input, responses.ResponseInputItemParamOfFunctionCall(tc.ArgsJSON, tc.ID, tc.Name))
	}
	return input
}

// imagePart builds an input_image part; the API takes inline images as a
// base64 data URL.
func imagePart(img llm.Image) responses.ResponseInputContentUnionParam {
	return responses.ResponseInputContentUnionParam{OfInputImage: &responses.ResponseInputImageParam{
		ImageURL: sdk.String(fmt.Sprintf("data:%s;base64,%s", img.MediaType, base64.StdEncoding.EncodeToString(img.Data))),
		Detail:   responses.ResponseInputImageDetailAuto,
	}}
}

// pdfPart builds a native-PDF input_file part: a base64 data URL plus a
// filename. The model extracts the text and renders pages so visual content
// is visible.
func pdfPart(pdf llm.PDF) responses.ResponseInputContentUnionParam {
	filename := pdf.Filename
	if filename == "" {
		filename = "document.pdf"
	}
	return responses.ResponseInputContentUnionParam{OfInputFile: &responses.ResponseInputFileParam{
		FileData: sdk.String("data:application/pdf;base64," + base64.StdEncoding.EncodeToString(pdf.Data)),
		Filename: sdk.String(filename),
	}}
}

// buildTools maps llm.Tool definitions to function tools.
func buildTools(in []llm.Tool) []responses.ToolUnionParam {
	tools := make([]responses.ToolUnionParam, 0, len(in))
	for _, t := range in {
		schema := t.Schema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		// Responses defaults function tools to strict mode, which rejects
		// schemas with optional properties; ours have them.
		tool := responses.ToolParamOfFunction(t.Name, schema, false)
		if t.Description != "" {
			tool.OfFunction.Description = sdk.String(t.Description)
		}
		tools = append(tools, tool)
	}
	return tools
}

// run drains the SDK stream into the unified event channel.
func (c *Client) run(ctx context.Context, params responses.ResponseNewParams, out chan<- llm.Event) {
	defer close(out)

	stream := c.api.Responses.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	type doneItem struct {
		index int64
		raw   json.RawMessage
	}
	var items []doneItem
	sawToolCall := false

	for stream.Next() {
		ev := stream.Current()
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta == "" {
				continue
			}
			if !llm.SendOrCancel(ctx, out, llm.Event{Kind: llm.EventText, TextDelta: ev.Delta}) {
				return
			}

		case "response.output_item.done":
			// Completed items are the unit both for replay (the reasoning
			// item's encrypted content is only final here) and for tool
			// calls: argument deltas are deliberately not forwarded, since
			// the orchestrator buffers calls by id and a fragment without
			// one would become a phantom call. Each call is emitted once,
			// whole.
			items = append(items, doneItem{index: ev.OutputIndex, raw: json.RawMessage(ev.Item.RawJSON())})
			if ev.Item.Type != "function_call" || ev.Item.Status == "incomplete" {
				continue
			}
			call := ev.Item.AsFunctionCall()
			sawToolCall = true
			if !llm.SendOrCancel(ctx, out, llm.Event{
				Kind: llm.EventToolCall,
				ToolCall: &llm.ToolCallDelta{
					ID:       call.CallID,
					Name:     call.Name,
					ArgsJSON: call.Arguments,
					Final:    true,
				},
			}) {
				return
			}

		case "response.completed", "response.incomplete":
			done := llm.Event{
				Kind:       llm.EventDone,
				StopReason: stopReason(ev.Response, sawToolCall),
				Usage:      usageOf(ev.Response),
			}
			if done.StopReason == llm.StopToolUse {
				sort.SliceStable(items, func(i, j int) bool { return items[i].index < items[j].index })
				replay := make(replayItems, len(items))
				for i, it := range items {
					replay[i] = it.raw
				}
				done.Replay = replay
			}
			llm.SendOrCancel(ctx, out, done)
			return

		case "response.failed":
			llm.SendOrCancel(ctx, out, llm.Event{Kind: llm.EventError, Err: responseError(ev.Response.Error)})
			return

		case "error":
			llm.SendOrCancel(ctx, out, llm.Event{Kind: llm.EventError, Err: fmt.Errorf("openai: stream: %s: %s", ev.Code, ev.Message)})
			return
		}
	}

	llm.SendOrCancel(ctx, out, streamEndEvent(ctx, stream.Err()))
}

// streamEndEvent is the terminal event for a stream that stopped without a
// terminal response event. A cancelled or timed-out context is a normal
// cancellation, whatever error the transport surfaced for it; anything else
// is an error, including a clean EOF, which would otherwise pass a truncated
// answer off as complete.
func streamEndEvent(ctx context.Context, err error) llm.Event {
	if ctx.Err() != nil {
		return llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
	}
	if err == nil {
		err = errors.New("ended before the response completed")
	}
	// The SDK's error text is only the HTTP status; the API's message is
	// what says which parameter a 400 objected to.
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) && apiErr.Message != "" {
		return llm.Event{Kind: llm.EventError, Err: fmt.Errorf("openai: %w: %s", err, apiErr.Message)}
	}
	return llm.Event{Kind: llm.EventError, Err: fmt.Errorf("openai: stream: %w", err)}
}

// stopReason maps a finished response to llm.StopReason.
func stopReason(resp responses.Response, sawToolCall bool) llm.StopReason {
	if sawToolCall {
		return llm.StopToolUse
	}
	if resp.Status == responses.ResponseStatusIncomplete {
		switch resp.IncompleteDetails.Reason {
		case "max_output_tokens":
			return llm.StopMaxTokens
		case "content_filter":
			return llm.StopFiltered
		}
	}
	return llm.StopEnd
}

func usageOf(resp responses.Response) *llm.Usage {
	if resp.Usage.TotalTokens == 0 {
		return nil
	}
	return &llm.Usage{
		InputTokens:     int(resp.Usage.InputTokens),
		OutputTokens:    int(resp.Usage.OutputTokens),
		CacheReadTokens: int(resp.Usage.InputTokensDetails.CachedTokens),
	}
}

func responseError(e responses.ResponseError) error {
	if e.Message == "" {
		return errors.New("openai: response failed")
	}
	return fmt.Errorf("openai: response failed: %s: %s", e.Code, e.Message)
}
