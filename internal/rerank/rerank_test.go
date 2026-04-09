package rerank

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/muty/nexus/internal/config"
	"go.uber.org/zap"
)

type mockReranker struct {
	calls     int
	failUntil int
	err       error
	results   []Result
}

func (m *mockReranker) Rerank(_ context.Context, _ string, docs []string) ([]Result, error) {
	m.calls++
	if m.calls <= m.failUntil {
		return nil, m.err
	}
	if m.results != nil {
		return m.results, nil
	}
	results := make([]Result, len(docs))
	for i := range docs {
		results[i] = Result{Index: i, Score: 1.0 - float64(i)*0.1}
	}
	return results, nil
}

func TestNew_Disabled(t *testing.T) {
	r, err := New(&config.Config{}, zap.NewNop())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Error("expected nil reranker when provider is empty")
	}
}

func TestNew_Voyage(t *testing.T) {
	r, err := New(&config.Config{
		RerankProvider: "voyage",
		RerankAPIKey:   "test-key",
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil reranker")
	}
}

func TestNew_Cohere(t *testing.T) {
	r, err := New(&config.Config{
		RerankProvider: "cohere",
		RerankAPIKey:   "test-key",
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil reranker")
	}
}

func TestNew_MissingAPIKey(t *testing.T) {
	_, err := New(&config.Config{
		RerankProvider: "voyage",
	}, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestNew_FallbackToEmbeddingKey(t *testing.T) {
	r, err := New(&config.Config{
		RerankProvider:  "voyage",
		EmbeddingAPIKey: "fallback-key",
	}, zap.NewNop())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil reranker with fallback key")
	}
}

func TestNew_UnknownProvider(t *testing.T) {
	_, err := New(&config.Config{
		RerankProvider: "unknown",
	}, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

// --- Retry tests ---

func newTestRetry(inner Reranker) *RetryReranker {
	return &RetryReranker{
		inner:      inner,
		maxRetries: 3,
		baseDelay:  time.Millisecond,
		log:        zap.NewNop(),
	}
}

func TestRetry_SuccessFirstAttempt(t *testing.T) {
	mock := &mockReranker{}
	r := newTestRetry(mock)

	results, err := r.Rerank(context.Background(), "query", []string{"doc1", "doc2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 call, got %d", mock.calls)
	}
}

func TestRetry_SuccessAfterRetry_429(t *testing.T) {
	mock := &mockReranker{
		failUntil: 2,
		err:       &RerankError{StatusCode: 429, Provider: "test"},
	}
	r := newTestRetry(mock)

	results, err := r.Rerank(context.Background(), "query", []string{"doc1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if mock.calls != 3 {
		t.Errorf("expected 3 calls, got %d", mock.calls)
	}
}

func TestRetry_NoRetryOn400(t *testing.T) {
	mock := &mockReranker{
		failUntil: 10,
		err:       &RerankError{StatusCode: 400, Provider: "test"},
	}
	r := newTestRetry(mock)

	_, err := r.Rerank(context.Background(), "query", []string{"doc1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 call (no retry on 400), got %d", mock.calls)
	}
}

func TestRetry_MaxRetriesExhausted(t *testing.T) {
	mock := &mockReranker{
		failUntil: 100,
		err:       &RerankError{StatusCode: 503, Provider: "test"},
	}
	r := newTestRetry(mock)

	_, err := r.Rerank(context.Background(), "query", []string{"doc1"})
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if mock.calls != 4 {
		t.Errorf("expected 4 calls (1 + 3 retries), got %d", mock.calls)
	}
}

func TestRetry_NoRetryOnContextCancelled(t *testing.T) {
	mock := &mockReranker{
		failUntil: 10,
		err:       context.Canceled,
	}
	r := newTestRetry(mock)

	_, err := r.Rerank(context.Background(), "query", []string{"doc1"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 call, got %d", mock.calls)
	}
}

// --- Provider tests with mock HTTP server ---

func TestVoyage_Rerank(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 1, "relevance_score": 0.95},
				{"index": 0, "relevance_score": 0.30},
			},
		})
	}))
	defer srv.Close()

	v := &Voyage{apiKey: "test", model: "rerank-2", baseURL: srv.URL, client: srv.Client()}
	results, err := v.Rerank(context.Background(), "query", []string{"doc1", "doc2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Score != 0.95 {
		t.Errorf("first result score = %f, want 0.95", results[0].Score)
	}
}

func TestCohere_Rerank(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 0, "relevance_score": 0.88},
			},
		})
	}))
	defer srv.Close()

	c := &Cohere{apiKey: "test", model: "rerank-v3.5", baseURL: srv.URL, client: srv.Client()}
	results, err := c.Rerank(context.Background(), "query", []string{"doc1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Score != 0.88 {
		t.Errorf("score = %f, want 0.88", results[0].Score)
	}
}

func TestVoyage_Rerank_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	v := &Voyage{apiKey: "test", model: "rerank-2", baseURL: srv.URL, client: srv.Client()}
	_, err := v.Rerank(context.Background(), "query", []string{"doc1"})
	if err == nil {
		t.Fatal("expected error")
	}
	var rerankErr *RerankError
	if !errors.As(err, &rerankErr) || rerankErr.StatusCode != 429 {
		t.Errorf("expected RerankError with 429, got %v", err)
	}
}

// --- Error tests ---

func TestRerankError(t *testing.T) {
	e := &RerankError{StatusCode: 429, Provider: "voyage"}
	if e.Error() != "voyage: rerank request failed with status 429" {
		t.Errorf("Error() = %q", e.Error())
	}
	if !e.IsRetryable() {
		t.Error("429 should be retryable")
	}

	e400 := &RerankError{StatusCode: 400, Provider: "test"}
	if e400.IsRetryable() {
		t.Error("400 should not be retryable")
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"429", &RerankError{StatusCode: 429}, true},
		{"500", &RerankError{StatusCode: 500}, true},
		{"400", &RerankError{StatusCode: 400}, false},
		{"context canceled", context.Canceled, false},
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
