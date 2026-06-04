package rerank

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/muty/nexus/internal/retry"
)

// RetryReranker wraps a Reranker with retry logic and exponential backoff.
type RetryReranker struct {
	inner      Reranker
	maxRetries int
	baseDelay  time.Duration
	log        *zap.Logger
}

// NewRetryReranker wraps a reranker with retry logic.
func NewRetryReranker(inner Reranker, log *zap.Logger) *RetryReranker {
	return &RetryReranker{
		inner:      inner,
		maxRetries: retry.DefaultMaxRetries,
		baseDelay:  retry.DefaultBaseDelay,
		log:        log,
	}
}

func (r *RetryReranker) Rerank(ctx context.Context, query string, documents []string) ([]Result, error) {
	return retry.Do(ctx, r.log, "rerank request", r.maxRetries, r.baseDelay,
		func() ([]Result, error) { return r.inner.Rerank(ctx, query, documents) },
		rerankErrorRetryable,
	)
}

// rerankErrorRetryable is the typed predicate for retry.Do: a typed RerankError
// uses its own IsRetryable; unrecognized (network) errors retry.
func rerankErrorRetryable(err error) bool {
	var rerankErr *RerankError
	if errors.As(err, &rerankErr) {
		return rerankErr.IsRetryable()
	}
	return true
}
