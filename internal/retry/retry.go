// Package retry provides shared exponential-backoff retry logic for the
// provider adapters (embedding, rerank, llm), which previously each carried a
// byte-for-byte copy of the backoff math and retry loop.
package retry

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"go.uber.org/zap"
)

const (
	// DefaultMaxRetries is the number of retries (so MaxRetries+1 attempts).
	DefaultMaxRetries = 3
	// DefaultBaseDelay is the first-retry delay; subsequent retries double it.
	DefaultBaseDelay = time.Second
)

// Backoff returns the delay for the given attempt: baseDelay*2^attempt plus up
// to baseDelay of jitter.
func Backoff(attempt int, baseDelay time.Duration) time.Duration {
	delay := baseDelay << attempt
	jitter := time.Duration(rand.Int64N(int64(baseDelay)))
	return delay + jitter
}

// Retryable reports whether err is transient. Context cancellation / deadline
// are never retried; otherwise the caller's typed predicate decides (typically
// a provider's typed-error IsRetryable). The predicate should default to true
// for unrecognized (e.g. network) errors.
func Retryable(err error, typed func(error) bool) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return typed(err)
}

// Do runs op with retry + exponential backoff. typed decides whether a given
// error is a transient provider error (the context-cancel guard is applied
// here). label prefixes the warn log. maxRetries/baseDelay are passed in (not
// hardcoded) so callers stay configurable and tests can use small delays. On
// exhaustion it returns the last error; on a non-retryable error it returns
// immediately.
func Do[T any](ctx context.Context, log *zap.Logger, label string, maxRetries int, baseDelay time.Duration, op func() (T, error), typed func(error) bool) (T, error) {
	var zero T
	var last error
	for attempt := range maxRetries + 1 {
		result, err := op()
		if err == nil {
			return result, nil
		}
		last = err

		if !Retryable(err, typed) {
			return zero, err
		}
		if attempt >= maxRetries {
			break
		}

		delay := Backoff(attempt, baseDelay)
		if log != nil {
			log.Warn(label+" failed, retrying",
				zap.Int("attempt", attempt+1),
				zap.Int("max_retries", maxRetries),
				zap.Duration("backoff", delay),
				zap.Error(err),
			)
		}

		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
	}
	return zero, last
}
