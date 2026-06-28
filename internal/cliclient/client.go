// Package cliclient is a thin HTTP client over the Nexus REST API. It is shared
// by the nexus-cli commands and (later) the MCP server: it centralizes bearer
// auth, the {"data": ...} success / {"error": ...} failure envelope, and typed
// calls onto the existing endpoints. It deliberately holds no token-persistence
// logic — a caller resolves a token (env, file, keychain) and hands it in — so
// the same client serves both the interactive CLI and the env-injected MCP
// subcommand.
package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultTimeout bounds a single API call. Generous enough for a cold search on
// a homelab box, short enough that a wedged server fails fast.
const defaultTimeout = 60 * time.Second

// Client talks to a Nexus server as a single authenticated user.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New returns a Client for baseURL authenticating with token. token may be empty
// for the unauthenticated login call. A trailing slash on baseURL is trimmed so
// path joining stays predictable.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: defaultTimeout},
	}
}

// APIError is returned for any non-2xx response. It carries the HTTP status and
// the server's {"error": ...} message (or a fallback) so commands can render a
// clean one-line failure.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("server returned %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	}
	return fmt.Sprintf("%s (status %d)", e.Message, e.StatusCode)
}

// envelope mirrors the server's APIResponse: success carries `data`, failure
// carries `error`. Middleware 401/403 responses are a bare {"error": ...} too,
// so the same shape decodes both.
type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

// do issues a request and unwraps the response envelope. body, if non-nil, is
// JSON-encoded into the request; out, if non-nil, receives the decoded `data`
// payload.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	return decodeEnvelope(resp.StatusCode, raw, out)
}

// decodeEnvelope maps an HTTP status + raw body onto either an *APIError (non-2xx)
// or the decoded `data` payload. Split out from do so it is unit-testable without
// a live server.
func decodeEnvelope(status int, raw []byte, out any) error {
	var env envelope
	// A 204 (or any empty body) has nothing to decode.
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil {
			// Non-JSON body (shouldn't happen against Nexus, but be defensive).
			if status < 200 || status >= 300 {
				return &APIError{StatusCode: status, Message: strings.TrimSpace(string(raw))}
			}
			return fmt.Errorf("decode response: %w", err)
		}
	}

	if status < 200 || status >= 300 {
		return &APIError{StatusCode: status, Message: env.Error}
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("decode response data: %w", err)
		}
	}
	return nil
}
