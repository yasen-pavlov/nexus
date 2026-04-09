package embedding

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
		maxRetries: defaultMaxRetries,
		baseDelay:  defaultBaseDelay,
		log:        log,
	}
}

func (r *RetryEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	var lastErr error

	for attempt := range r.maxRetries + 1 {
		result, err := r.inner.Embed(ctx, texts)
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
		r.log.Warn("embedding request failed, retrying",
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

func (r *RetryEmbedder) Dimension() int {
	return r.inner.Dimension()
}

// backoff returns the delay for the given attempt using exponential backoff with jitter.
func (r *RetryEmbedder) backoff(attempt int) time.Duration {
	delay := r.baseDelay << attempt // baseDelay * 2^attempt
	jitter := time.Duration(rand.Int64N(int64(r.baseDelay)))
	return delay + jitter
}

// isRetryable returns true if the error is transient and should be retried.
func isRetryable(err error) bool {
	// Context cancellation is never retryable
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check for typed EmbedError with status code
	var embedErr *EmbedError
	if errors.As(err, &embedErr) {
		return embedErr.IsRetryable()
	}

	// Network errors (timeouts, connection resets) are retryable
	return true
}
