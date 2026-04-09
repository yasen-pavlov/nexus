package embedding

import "fmt"

// EmbedError represents an error from an embedding API with an HTTP status code.
type EmbedError struct {
	StatusCode int
	Provider   string
}

func (e *EmbedError) Error() string {
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
