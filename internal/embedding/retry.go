package embedding

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/muty/nexus/internal/retry"
)

// RetryEmbedder wraps an Embedder with retry logic and exponential backoff.
type RetryEmbedder struct {
	inner      Embedder
	maxRetries int
	baseDelay  time.Duration
	log        *zap.Logger
}

// NewRetryEmbedder wraps an embedder with retry logic.
func NewRetryEmbedder(inner Embedder, log *zap.Logger) *RetryEmbedder {
	return &RetryEmbedder{
		inner:      inner,
		maxRetries: retry.DefaultMaxRetries,
		baseDelay:  retry.DefaultBaseDelay,
		log:        log,
	}
}

func (r *RetryEmbedder) Embed(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	return retry.Do(ctx, r.log, "embedding request", r.maxRetries, r.baseDelay,
		func() ([][]float32, error) { return r.inner.Embed(ctx, texts, inputType) },
		embedErrorRetryable,
	)
}

func (r *RetryEmbedder) Dimension() int {
	return r.inner.Dimension()
}

// embedErrorRetryable is the typed predicate for retry.Do: a typed EmbedError
// uses its own IsRetryable; unrecognized (network) errors retry.
func embedErrorRetryable(err error) bool {
	var embedErr *EmbedError
	if errors.As(err, &embedErr) {
		return embedErr.IsRetryable()
	}
	return true
}
