package embedding

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// EmbedError represents an error from an embedding API with an HTTP status code
// and the relevant snippet of the response body for diagnostics.
type EmbedError struct {
	StatusCode int
	Provider   string
	Body       string // truncated response body (best-effort)
}

func (e *EmbedError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("%s: request failed with status %d: %s", e.Provider, e.StatusCode, e.Body)
	}
	return fmt.Sprintf("%s: request failed with status %d", e.Provider, e.StatusCode)
}

// IsRetryable returns true if the error is transient and the request should be retried.
func (e *EmbedError) IsRetryable() bool {
	switch e.StatusCode {
	case 429: // rate limit
		return true
	case 500, 502, 503, 504: // server errors
		return true
	default:
		return false
	}
}

// errorFromResponse builds an EmbedError, capturing up to 1KB of the response
// body for diagnostics. The provider's API typically returns JSON with a
// "detail" or "error" field that explains why a 4xx happened — surfacing it
// makes debugging far easier than chasing bare status codes.
func errorFromResponse(resp *http.Response, provider string) *EmbedError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return &EmbedError{
		StatusCode: resp.StatusCode,
		Provider:   provider,
		Body:       strings.TrimSpace(string(body)),
	}
}
