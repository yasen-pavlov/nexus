package llm

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

// RetryGenerator wraps a Generator with retry logic for transient errors.
//
// Streaming-aware: retries only happen *before* the first successful Event is
// produced. Once the upstream stream has emitted any payload to the consumer,
// reconnecting would replay text or duplicate citations, so we surface the
// failure as an EventError and close the channel. This matches the
// internal/embedding RetryEmbedder behavior but applied per-stream.
type RetryGenerator struct {
	inner      Generator
	maxRetries int
	baseDelay  time.Duration
	log        *zap.Logger
}

// NewRetryGenerator wraps a Generator with retry + backoff.
func NewRetryGenerator(inner Generator, log *zap.Logger) *RetryGenerator {
	return &RetryGenerator{
		inner:      inner,
		maxRetries: defaultMaxRetries,
		baseDelay:  defaultBaseDelay,
		log:        log,
	}
}

// Generate proxies to the inner generator with retry on the *initial* call
// failure. Once the inner Generate returns a channel, that channel is
// returned to the caller as-is — mid-stream errors are not retried.
func (r *RetryGenerator) Generate(ctx context.Context, req GenerateRequest) (<-chan Event, error) {
	var lastErr error

	for attempt := range r.maxRetries + 1 {
		ch, err := r.inner.Generate(ctx, req)
		if err == nil {
			return ch, nil
		}

		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}

		if attempt >= r.maxRetries {
			break
		}

		delay := r.backoff(attempt)
		r.log.Warn("llm generate failed, retrying",
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

// backoff returns the delay for the given attempt: baseDelay*2^attempt + jitter.
func (r *RetryGenerator) backoff(attempt int) time.Duration {
	delay := r.baseDelay << attempt
	jitter := time.Duration(rand.Int64N(int64(r.baseDelay)))
	return delay + jitter
}

// isRetryable returns true for transient errors. Context cancellation /
// deadline exceeded are never retried; typed LLMError uses its own
// IsRetryable; unrecognized errors (network) are retried.
func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var llmErr *LLMError
	if errors.As(err, &llmErr) {
		return llmErr.IsRetryable()
	}

	return true
}
