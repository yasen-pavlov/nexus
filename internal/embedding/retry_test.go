package embedding

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

type mockEmbedder struct {
	calls     int
	failUntil int // fail the first N calls
	err       error
	dim       int
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string, _ string) ([][]float32, error) {
	m.calls++
	if m.calls <= m.failUntil {
		return nil, m.err
	}
	result := make([][]float32, len(texts))
	for i := range result {
		result[i] = make([]float32, m.dim)
	}
	return result, nil
}

func (m *mockEmbedder) Dimension() int { return m.dim }

func newTestRetry(inner Embedder) *RetryEmbedder {
	return &RetryEmbedder{
		inner:      inner,
		maxRetries: 3,
		baseDelay:  time.Millisecond, // fast for tests
		log:        zap.NewNop(),
	}
}

func TestRetry_SuccessFirstAttempt(t *testing.T) {
	mock := &mockEmbedder{dim: 128}
	r := newTestRetry(mock)

	result, err := r.Embed(context.Background(), []string{"hello"}, InputTypeDocument)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result))
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 call, got %d", mock.calls)
	}
}

func TestRetry_SuccessAfterRetry_429(t *testing.T) {
	mock := &mockEmbedder{
		dim:       128,
		failUntil: 2,
		err:       &EmbedError{StatusCode: 429, Provider: "test"},
	}
	r := newTestRetry(mock)

	result, err := r.Embed(context.Background(), []string{"hello"}, InputTypeDocument)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result))
	}
	if mock.calls != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", mock.calls)
	}
}

func TestRetry_SuccessAfterRetry_500(t *testing.T) {
	mock := &mockEmbedder{
		dim:       128,
		failUntil: 1,
		err:       &EmbedError{StatusCode: 500, Provider: "test"},
	}
	r := newTestRetry(mock)

	result, err := r.Embed(context.Background(), []string{"hello"}, InputTypeDocument)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.calls != 2 {
		t.Errorf("expected 2 calls, got %d", mock.calls)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result))
	}
}

func TestRetry_NoRetryOn400(t *testing.T) {
	mock := &mockEmbedder{
		dim:       128,
		failUntil: 10,
		err:       &EmbedError{StatusCode: 400, Provider: "test"},
	}
	r := newTestRetry(mock)

	_, err := r.Embed(context.Background(), []string{"hello"}, InputTypeDocument)
	if err == nil {
		t.Fatal("expected error")
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 call (no retry on 400), got %d", mock.calls)
	}
}

func TestRetry_NoRetryOn401(t *testing.T) {
	mock := &mockEmbedder{
		dim:       128,
		failUntil: 10,
		err:       &EmbedError{StatusCode: 401, Provider: "test"},
	}
	r := newTestRetry(mock)

	_, err := r.Embed(context.Background(), []string{"hello"}, InputTypeDocument)
	if err == nil {
		t.Fatal("expected error")
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 call (no retry on 401), got %d", mock.calls)
	}
}

func TestRetry_NoRetryOnContextCancelled(t *testing.T) {
	mock := &mockEmbedder{
		dim:       128,
		failUntil: 10,
		err:       context.Canceled,
	}
	r := newTestRetry(mock)

	_, err := r.Embed(context.Background(), []string{"hello"}, InputTypeDocument)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 call, got %d", mock.calls)
	}
}

func TestRetry_MaxRetriesExhausted(t *testing.T) {
	mock := &mockEmbedder{
		dim:       128,
		failUntil: 100,
		err:       &EmbedError{StatusCode: 429, Provider: "test"},
	}
	r := newTestRetry(mock)

	_, err := r.Embed(context.Background(), []string{"hello"}, InputTypeDocument)
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	// 1 initial + 3 retries = 4 total
	if mock.calls != 4 {
		t.Errorf("expected 4 calls (1 + 3 retries), got %d", mock.calls)
	}

	var embedErr *EmbedError
	if !errors.As(err, &embedErr) {
		t.Errorf("expected EmbedError, got %T", err)
	}
}

func TestRetry_NetworkErrorIsRetryable(t *testing.T) {
	mock := &mockEmbedder{
		dim:       128,
		failUntil: 1,
		err:       errors.New("connection reset by peer"),
	}
	r := newTestRetry(mock)

	result, err := r.Embed(context.Background(), []string{"hello"}, InputTypeDocument)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.calls != 2 {
		t.Errorf("expected 2 calls (network error retried), got %d", mock.calls)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result))
	}
}

func TestRetry_ContextCancelledDuringBackoff(t *testing.T) {
	mock := &mockEmbedder{
		dim:       128,
		failUntil: 100,
		err:       &EmbedError{StatusCode: 503, Provider: "test"},
	}
	r := &RetryEmbedder{
		inner:      mock,
		maxRetries: 3,
		baseDelay:  time.Second, // long delay
		log:        zap.NewNop(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := r.Embed(ctx, []string{"hello"}, InputTypeDocument)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRetry_Dimension(t *testing.T) {
	mock := &mockEmbedder{dim: 768}
	r := newTestRetry(mock)
	if r.Dimension() != 768 {
		t.Errorf("Dimension() = %d, want 768", r.Dimension())
	}
}

func TestRetry_BackoffIncreases(t *testing.T) {
	r := &RetryEmbedder{baseDelay: 100 * time.Millisecond}
	d0 := r.backoff(0)
	d1 := r.backoff(1)
	d2 := r.backoff(2)

	// With jitter, d1 should generally be > d0 base, d2 > d1 base
	// Base values: 100ms, 200ms, 400ms (plus 0-100ms jitter)
	if d0 > 250*time.Millisecond {
		t.Errorf("backoff(0) = %v, expected <= 250ms", d0)
	}
	if d2 < 400*time.Millisecond {
		t.Errorf("backoff(2) = %v, expected >= 400ms", d2)
	}
	_ = d1
}

// --- EmbedError tests ---

func TestEmbedError_Error(t *testing.T) {
	e := &EmbedError{StatusCode: 429, Provider: "voyage"}
	if e.Error() != "voyage: request failed with status 429" {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestEmbedError_IsRetryable(t *testing.T) {
	tests := []struct {
		status    int
		retryable bool
	}{
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{504, true},
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{200, false},
	}
	for _, tt := range tests {
		e := &EmbedError{StatusCode: tt.status}
		if e.IsRetryable() != tt.retryable {
			t.Errorf("status %d: IsRetryable() = %v, want %v", tt.status, e.IsRetryable(), tt.retryable)
		}
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"429", &EmbedError{StatusCode: 429}, true},
		{"500", &EmbedError{StatusCode: 500}, true},
		{"400", &EmbedError{StatusCode: 400}, false},
		{"context canceled", context.Canceled, false},
		{"deadline exceeded", context.DeadlineExceeded, false},
		{"network error", errors.New("connection reset"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if isRetryable(tt.err) != tt.retryable {
				t.Errorf("isRetryable() = %v, want %v", isRetryable(tt.err), tt.retryable)
			}
		})
	}
}
