package cliclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type postMessageRequest struct {
	Content string `json:"content"`
	Model   string `json:"model"`
}

// SSEHandler receives one parsed SSE frame: the event name and its raw JSON
// data. Returning an error aborts the stream.
type SSEHandler func(event string, data []byte) error

// StreamMessage posts content to a chat and streams the answer as SSE frames to
// handler. It bypasses the JSON-envelope path: a pre-stream failure (bad
// request, no model configured, chat gone) arrives as a normal {"error"} body
// and is returned as *APIError, while a 200 text/event-stream response is parsed
// frame by frame. model may be empty (the server falls back to its default).
//
// The call has NO client-side timeout — a long generation is bounded only by
// ctx, so Ctrl-C (a cancelled ctx) is how a caller stops it.
func (c *Client) StreamMessage(ctx context.Context, chatID, content, model string, handler SSEHandler) error {
	body, err := json.Marshal(postMessageRequest{Content: content, Model: model})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chats/"+chatID+"/messages", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	// A generation can outlive the default per-call timeout, so use a dedicated
	// no-timeout client (ctx governs the lifetime) that still inherits any
	// transport configured on the base client.
	resp, err := (&http.Client{Transport: c.http.Transport}).Do(req)
	if err != nil {
		return fmt.Errorf("POST /api/chats/%s/messages: %w", chatID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Pre-stream failures are normal JSON envelopes, not SSE.
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		raw, _ := io.ReadAll(resp.Body)
		if envErr := decodeEnvelope(resp.StatusCode, raw, nil); envErr != nil {
			return envErr
		}
		return fmt.Errorf("unexpected non-stream response: %d", resp.StatusCode)
	}

	return parseSSE(resp.Body, handler)
}

// sseAssembler accumulates the `event:`/`data:` lines of one SSE frame.
type sseAssembler struct {
	event string
	data  []byte
}

// feed processes one field line, returning true when a blank line ends the
// frame (signalling the caller to dispatch it). event/data lines return false;
// comments (":..."), id:, retry: are ignored.
func (a *sseAssembler) feed(field []byte) (frameEnd bool) {
	switch {
	case len(field) == 0:
		return true
	case bytes.HasPrefix(field, []byte("event:")):
		a.event = string(bytes.TrimSpace(field[len("event:"):]))
	case bytes.HasPrefix(field, []byte("data:")):
		chunk := bytes.TrimPrefix(field[len("data:"):], []byte(" "))
		if len(a.data) > 0 {
			a.data = append(a.data, '\n')
		}
		a.data = append(a.data, chunk...)
	}
	return false
}

// take returns the assembled frame and resets for the next one.
func (a *sseAssembler) take() (event string, data []byte) {
	event, data = a.event, a.data
	a.event, a.data = "", nil
	return event, data
}

// parseSSE reads `event: <name>\ndata: <json>\n\n` frames from r and dispatches
// each to handler. It uses bufio.Reader (not Scanner) so large evidence/tool
// frames that exceed Scanner's 64 KB token cap don't truncate the stream.
func parseSSE(r io.Reader, handler SSEHandler) error {
	br := bufio.NewReader(r)
	var a sseAssembler
	flush := func() error {
		ev, d := a.take()
		if ev == "" {
			return nil
		}
		return handler(ev, d)
	}
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 && a.feed(bytes.TrimRight(line, "\r\n")) {
			if err := flush(); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return flush() // flush a trailing frame with no blank line
			}
			return fmt.Errorf("read stream: %w", readErr)
		}
	}
}
