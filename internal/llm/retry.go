package llm

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/muty/nexus/internal/retry"
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
		maxRetries: retry.DefaultMaxRetries,
		baseDelay:  retry.DefaultBaseDelay,
		log:        log,
	}
}

// Generate proxies to the inner generator with retry on the *initial* call
// failure. Once the inner Generate returns a channel, that channel is
// returned to the caller as-is — mid-stream errors are not retried.
func (r *RetryGenerator) Generate(ctx context.Context, req GenerateRequest) (<-chan Event, error) {
	return retry.Do(ctx, r.log, "llm generate", r.maxRetries, r.baseDelay,
		func() (<-chan Event, error) { return r.inner.Generate(ctx, req) },
		llmErrorRetryable,
	)
}

// llmErrorRetryable is the typed predicate for retry.Do: a typed LLMError uses
// its own IsRetryable; unrecognized (network) errors retry.
func llmErrorRetryable(err error) bool {
	var llmErr *LLMError
	if errors.As(err, &llmErr) {
		return llmErr.IsRetryable()
	}
	return true
}
