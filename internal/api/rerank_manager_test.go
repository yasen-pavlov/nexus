package api

import (
	"context"
	"testing"

	"github.com/muty/nexus/internal/rerank"
	"go.uber.org/zap"
)

type noopReranker struct{}

func (s *noopReranker) Rerank(_ context.Context, _ string, docs []string) ([]rerank.Result, error) {
	out := make([]rerank.Result, len(docs))
	for i := range docs {
		out[i] = rerank.Result{Index: i, Score: 1}
	}
	return out, nil
}

func TestRerankManager_ProviderModel_EmptyByDefault(t *testing.T) {
	m := NewRerankManager(nil, zap.NewNop())
	if got := m.Provider(); got != "" {
		t.Errorf("Provider() before setActive = %q, want empty", got)
	}
	if got := m.Model(); got != "" {
		t.Errorf("Model() before setActive = %q, want empty", got)
	}
}

func TestRerankManager_ProviderModel_AfterSetActive(t *testing.T) {
	m := NewRerankManager(nil, zap.NewNop())
	m.setActive(&noopReranker{}, "cohere", "rerank-v3.5")
	if got := m.Provider(); got != "cohere" {
		t.Errorf("Provider() = %q, want cohere", got)
	}
	if got := m.Model(); got != "rerank-v3.5" {
		t.Errorf("Model() = %q, want rerank-v3.5", got)
	}
}

func TestRerankManager_ProviderModel_NilRerankerReturnsEmpty(t *testing.T) {
	m := NewRerankManager(nil, zap.NewNop())
	m.setActive(&noopReranker{}, "cohere", "rerank-v3.5")
	m.setActive(nil, "", "")
	if got := m.Provider(); got != "" {
		t.Errorf("Provider() after disable = %q, want empty", got)
	}
	if got := m.Model(); got != "" {
		t.Errorf("Model() after disable = %q, want empty", got)
	}
}
