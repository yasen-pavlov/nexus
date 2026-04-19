package api

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

type stubEmbedder struct{ dim int }

func (s *stubEmbedder) Embed(_ context.Context, texts []string, _ string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, s.dim)
	}
	return out, nil
}

func (s *stubEmbedder) Dimension() int { return s.dim }

func TestOr(t *testing.T) {
	if or("a", "b") != "a" {
		t.Error("expected 'a'")
	}
	if or("", "b") != "b" {
		t.Error("expected 'b'")
	}
	if or("", "") != "" {
		t.Error("expected empty")
	}
}

func TestEmbeddingManager_ProviderModel_EmptyByDefault(t *testing.T) {
	m := NewEmbeddingManager(nil, zap.NewNop())
	if got := m.Provider(); got != "" {
		t.Errorf("Provider() before setActive = %q, want empty", got)
	}
	if got := m.Model(); got != "" {
		t.Errorf("Model() before setActive = %q, want empty", got)
	}
}

func TestEmbeddingManager_ProviderModel_AfterSetActive(t *testing.T) {
	m := NewEmbeddingManager(nil, zap.NewNop())
	m.setActive(&stubEmbedder{dim: 1024}, "voyage", "voyage-3-large")
	if got := m.Provider(); got != "voyage" {
		t.Errorf("Provider() = %q, want voyage", got)
	}
	if got := m.Model(); got != "voyage-3-large" {
		t.Errorf("Model() = %q, want voyage-3-large", got)
	}
	if got := m.Dimension(); got != 1024 {
		t.Errorf("Dimension() = %d, want 1024", got)
	}
}

// Disabling (setActive with nil) must clear the exposed labels even though
// the previous provider/model strings linger on the struct — admins will
// see "disabled" in the UI, not stale values.
func TestEmbeddingManager_ProviderModel_NilEmbedderReturnsEmpty(t *testing.T) {
	m := NewEmbeddingManager(nil, zap.NewNop())
	m.setActive(&stubEmbedder{dim: 1024}, "voyage", "voyage-3-large")
	m.setActive(nil, "", "")
	if got := m.Provider(); got != "" {
		t.Errorf("Provider() after disable = %q, want empty", got)
	}
	if got := m.Model(); got != "" {
		t.Errorf("Model() after disable = %q, want empty", got)
	}
}
