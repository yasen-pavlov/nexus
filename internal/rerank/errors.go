package rerank

import "fmt"

// RerankError represents an error from a reranking API with an HTTP status code.
type RerankError struct {
	StatusCode int
	Provider   string
}

func (e *RerankError) Error() string {
	return fmt.Sprintf("%s: rerank request failed with status %d", e.Provider, e.StatusCode)
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
