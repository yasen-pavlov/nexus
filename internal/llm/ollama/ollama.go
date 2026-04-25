// Package ollama implements the llm.Generator for local Ollama models via
// the /api/chat endpoint. The protocol is NDJSON streaming — one JSON object
// per line, each with optional message.content / message.tool_calls / done.
//
// Ollama's streaming + tool-call story is shaky in practice (some models
// emit truncated tool-call JSON in stream mode). When tools are present and
// the configured model is in knownToolStreamingBroken, we fall back to a
// non-streaming request and emit one terminal EventText.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/muty/nexus/internal/llm"
	"go.uber.org/zap"
)

// knownToolStreamingBroken is the deny-list of models where streaming + tool
// use is unreliable. Conservative default; admins can extend later.
var knownToolStreamingBroken = map[string]bool{
	"qwen3:14b": true,
	"qwen3:7b":  true,
}

// Client is an llm.Generator backed by a local/remote Ollama instance.
type Client struct {
	baseURL string
	client  *http.Client
	log     *zap.Logger
}

// New constructs a client. baseURL is required (e.g. http://localhost:11434).
func New(baseURL string, log *zap.Logger) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 0}, // streaming uses ctx
		log:     log,
	}
}

// chatRequest mirrors Ollama's POST /api/chat body. Only the fields we need.
type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMsg      `json:"messages"`
	Stream   bool           `json:"stream"`
	Tools    []chatToolDef  `json:"tools,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
}

type chatMsg struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Images    []string       `json:"images,omitempty"`     // base64 strings
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"` // assistant turn
}

type chatToolCall struct {
	Function chatToolFunc `json:"function"`
}

type chatToolFunc struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type chatToolDef struct {
	Type     string        `json:"type"`
	Function chatToolDefFn `json:"function"`
}

type chatToolDefFn struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// chatResponseChunk is one NDJSON frame from POST /api/chat (with stream=true)
// or the single response when stream=false.
type chatResponseChunk struct {
	Model      string  `json:"model"`
	Message    chatMsg `json:"message"`
	Done       bool    `json:"done"`
	DoneReason string  `json:"done_reason,omitempty"`
	// Token-accounting fields (final chunk only). Ollama reports
	// prompt_eval_count and eval_count.
	PromptEvalCount int `json:"prompt_eval_count,omitempty"`
	EvalCount       int `json:"eval_count,omitempty"`
}

// Generate kicks off a chat request and returns a stream of llm.Events.
func (c *Client) Generate(ctx context.Context, req llm.GenerateRequest) (<-chan llm.Event, error) {
	if req.Model == "" {
		return nil, errors.New("ollama: model required")
	}

	useStream := len(req.Tools) == 0 || !knownToolStreamingBroken[req.Model]

	body, err := buildBody(req, useStream)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq) //nolint:bodyclose // closed in goroutine when streaming
	if err != nil {
		return nil, fmt.Errorf("ollama: request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close() //nolint:errcheck // best-effort
		return nil, llm.ErrorFromResponse(resp, "ollama")
	}

	out := make(chan llm.Event, 16)
	if useStream {
		go c.runStream(ctx, resp, out)
	} else {
		go c.runOneShot(ctx, resp, out)
	}
	return out, nil
}

// runStream reads NDJSON frames and forwards events.
func (c *Client) runStream(ctx context.Context, resp *http.Response, out chan<- llm.Event) {
	defer close(out)
	defer resp.Body.Close() //nolint:errcheck // best-effort

	stopReason := llm.StopEnd
	var usage *llm.Usage

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024) // big lines for tool args

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var chunk chatResponseChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			out <- llm.Event{Kind: llm.EventError, Err: fmt.Errorf("ollama: parse chunk: %w", err)}
			return
		}

		if chunk.Message.Content != "" {
			if !sendOrCancel(ctx, out, llm.Event{Kind: llm.EventText, TextDelta: chunk.Message.Content}) {
				return
			}
		}

		// Ollama emits tool calls as a complete object on the final chunk
		// (or one per chunk on some models). Treat each as a fully-formed
		// call, fanning Final=true so the orchestrator can run it.
		for _, tc := range chunk.Message.ToolCalls {
			argsBytes, _ := json.Marshal(tc.Function.Arguments)
			if !sendOrCancel(ctx, out, llm.Event{
				Kind: llm.EventToolCall,
				ToolCall: &llm.ToolCallDelta{
					Name:     tc.Function.Name,
					ArgsJSON: string(argsBytes),
					Final:    true,
				},
			}) {
				return
			}
			stopReason = llm.StopToolUse
		}

		if chunk.Done {
			if chunk.DoneReason != "" {
				stopReason = mapDoneReason(chunk.DoneReason)
			}
			if chunk.EvalCount > 0 || chunk.PromptEvalCount > 0 {
				usage = &llm.Usage{
					InputTokens:  chunk.PromptEvalCount,
					OutputTokens: chunk.EvalCount,
				}
			}
			break
		}
	}

	if err := scanner.Err(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			out <- llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
			return
		}
		out <- llm.Event{Kind: llm.EventError, Err: fmt.Errorf("ollama: read: %w", err)}
		return
	}

	out <- llm.Event{Kind: llm.EventDone, StopReason: stopReason, Usage: usage}
}

// runOneShot reads a single non-streaming response. Used as the fallback
// path when tools + a known-broken model combine.
func (c *Client) runOneShot(_ context.Context, resp *http.Response, out chan<- llm.Event) {
	defer close(out)
	defer resp.Body.Close() //nolint:errcheck // best-effort

	var chunk chatResponseChunk
	if err := json.NewDecoder(resp.Body).Decode(&chunk); err != nil {
		out <- llm.Event{Kind: llm.EventError, Err: fmt.Errorf("ollama: decode: %w", err)}
		return
	}

	if chunk.Message.Content != "" {
		out <- llm.Event{Kind: llm.EventText, TextDelta: chunk.Message.Content}
	}

	stopReason := llm.StopEnd
	for _, tc := range chunk.Message.ToolCalls {
		argsBytes, _ := json.Marshal(tc.Function.Arguments)
		out <- llm.Event{
			Kind: llm.EventToolCall,
			ToolCall: &llm.ToolCallDelta{
				Name:     tc.Function.Name,
				ArgsJSON: string(argsBytes),
				Final:    true,
			},
		}
		stopReason = llm.StopToolUse
	}
	if chunk.DoneReason != "" {
		stopReason = mapDoneReason(chunk.DoneReason)
	}

	var usage *llm.Usage
	if chunk.EvalCount > 0 || chunk.PromptEvalCount > 0 {
		usage = &llm.Usage{
			InputTokens:  chunk.PromptEvalCount,
			OutputTokens: chunk.EvalCount,
		}
	}
	out <- llm.Event{Kind: llm.EventDone, StopReason: stopReason, Usage: usage}
}

// buildBody serializes the request to Ollama's wire format.
func buildBody(req llm.GenerateRequest, stream bool) ([]byte, error) {
	body := chatRequest{
		Model:  req.Model,
		Stream: stream,
	}

	if req.System != "" || len(req.Documents) > 0 {
		systemBody := req.System
		if len(req.Documents) > 0 {
			systemBody += "\n\n" + buildDocumentsBlock(req.Documents)
		}
		body.Messages = append(body.Messages, chatMsg{Role: "system", Content: systemBody})
	}

	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleSystem:
			// Already collapsed.
		case llm.RoleUser:
			body.Messages = append(body.Messages, chatMsg{Role: "user", Content: m.Content})
		case llm.RoleAssistant:
			am := chatMsg{Role: "assistant", Content: m.Content}
			for _, tc := range m.ToolCalls {
				args := map[string]any{}
				if tc.ArgsJSON != "" {
					_ = json.Unmarshal([]byte(tc.ArgsJSON), &args)
				}
				am.ToolCalls = append(am.ToolCalls, chatToolCall{Function: chatToolFunc{Name: tc.Name, Arguments: args}})
			}
			body.Messages = append(body.Messages, am)
		case llm.RoleTool:
			body.Messages = append(body.Messages, chatMsg{Role: "tool", Content: m.Content})
		}
	}

	if len(body.Messages) == 0 {
		return nil, errors.New("ollama: at least one message required")
	}

	if req.Temperature != nil {
		body.Options = map[string]any{"temperature": float64(*req.Temperature)}
	}
	if req.MaxTokens > 0 {
		if body.Options == nil {
			body.Options = map[string]any{}
		}
		body.Options["num_predict"] = req.MaxTokens
	}

	for _, t := range req.Tools {
		body.Tools = append(body.Tools, chatToolDef{
			Type: "function",
			Function: chatToolDefFn{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Schema,
			},
		})
	}

	return json.Marshal(body)
}

// buildDocumentsBlock renders retrieved docs the same way as the OpenAI
// adapter — Ollama models read XML-tagged blocks fine and the orchestrator's
// [N] parser depends on the document index.
func buildDocumentsBlock(docs []llm.Document) string {
	var b strings.Builder
	b.WriteString("Retrieved documents (cite as [N] where N is the document number):\n")
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

func mapDoneReason(r string) llm.StopReason {
	switch r {
	case "stop":
		return llm.StopEnd
	case "length":
		return llm.StopMaxTokens
	case "tool_calls":
		return llm.StopToolUse
	default:
		return llm.StopEnd
	}
}

func sendOrCancel(ctx context.Context, out chan<- llm.Event, ev llm.Event) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		out <- llm.Event{Kind: llm.EventDone, StopReason: llm.StopCancelled}
		return false
	}
}

// EncodeImage exposes the image-to-base64 helper for the Phase 6 multi-modal
// orchestrator. Ollama vision models accept base64 images on the message's
// "images" field; the orchestrator constructs the message itself.
func EncodeImage(img llm.Image) string {
	return base64.StdEncoding.EncodeToString(img.Data)
}
