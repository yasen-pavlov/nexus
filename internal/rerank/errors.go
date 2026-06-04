package rerank

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// RerankError represents an error from a reranking API with an HTTP status code.
type RerankError struct {
	StatusCode int
	Provider   string
	Body       string // truncated response body (best-effort)
}

func (e *RerankError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("%s: rerank request failed with status %d: %s", e.Provider, e.StatusCode, e.Body)
	}
	return fmt.Sprintf("%s: rerank request failed with status %d", e.Provider, e.StatusCode)
}

// errorFromResponse builds a RerankError capturing the (truncated) response
// body so 4xx failures carry the provider's diagnostic detail, matching the
// embedding package's EmbedError.
func errorFromResponse(resp *http.Response, provider string) *RerankError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return &RerankError{
		StatusCode: resp.StatusCode,
		Provider:   provider,
		Body:       strings.TrimSpace(string(body)),
	}
}

// IsRetryable returns true if the error is transient and the request should be retried.
func (e *RerankError) IsRetryable() bool {
	switch e.StatusCode {
	case 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}
