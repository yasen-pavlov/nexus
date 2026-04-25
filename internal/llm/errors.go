package llm

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// LLMError represents an error from an LLM API with an HTTP status code and
// the relevant snippet of the response body for diagnostics. Mirrors the
// shape used by internal/embedding/errors.go.
type LLMError struct { //nolint:revive // package name "llm" is already lowercase, this is the canonical typed error
	StatusCode int
	Provider   string
	Body       string // truncated response body (best-effort)
}

func (e *LLMError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("%s: request failed with status %d: %s", e.Provider, e.StatusCode, e.Body)
	}
	return fmt.Sprintf("%s: request failed with status %d", e.Provider, e.StatusCode)
}

// IsRetryable returns true for transient statuses worth retrying.
func (e *LLMError) IsRetryable() bool {
	switch e.StatusCode {
	case 429: // rate limit
		return true
	case 500, 502, 503, 504: // server errors
		return true
	default:
		return false
	}
}

// errorFromResponse builds an LLMError, capturing up to 1KB of the response
// body for diagnostics. Surfacing the provider's "detail" / "error" field
// makes 4xx debugging far easier than chasing bare status codes.
func errorFromResponse(resp *http.Response, provider string) *LLMError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return &LLMError{
		StatusCode: resp.StatusCode,
		Provider:   provider,
		Body:       strings.TrimSpace(string(body)),
	}
}

// ErrorFromResponse is the exported variant adapters in subpackages can call.
func ErrorFromResponse(resp *http.Response, provider string) *LLMError {
	return errorFromResponse(resp, provider)
}
