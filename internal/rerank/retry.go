package rerank

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"go.uber.org/zap"
)

const (
	defaultMaxRetries = 3
	defaultBaseDelay  = time.Second
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
		maxRetries: defaultMaxRetries,
		baseDelay:  defaultBaseDelay,
		log:        log,
	}
}

func (r *RetryReranker) Rerank(ctx context.Context, query string, documents []string) ([]Result, error) {
	var lastErr error

	for attempt := range r.maxRetries + 1 {
		result, err := r.inner.Rerank(ctx, query, documents)
		if err == nil {
			return result, nil
		}

		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}

		if attempt >= r.maxRetries {
			break
		}

		delay := r.backoff(attempt)
		r.log.Warn("rerank request failed, retrying",
			zap.Int("attempt", attempt+1),
			zap.Int("max_retries", r.maxRetries),
			zap.Duration("backoff", delay),
			zap.Error(err),
		)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return nil, lastErr
}

func (r *RetryReranker) backoff(attempt int) time.Duration {
	delay := r.baseDelay << attempt
	jitter := time.Duration(rand.Int64N(int64(r.baseDelay)))
	return delay + jitter
}

func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var rerankErr *RerankError
	if errors.As(err, &rerankErr) {
		return rerankErr.IsRetryable()
	}
	return true
}
