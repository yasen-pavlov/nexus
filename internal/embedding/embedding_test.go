package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/muty/nexus/internal/config"
	"go.uber.org/zap"
)

func TestNew_Disabled(t *testing.T) {
	e, err := New(&config.Config{}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if e != nil {
		t.Error("expected nil embedder when provider is empty")
	}
}

func TestNew_UnknownProvider(t *testing.T) {
	_, err := New(&config.Config{EmbeddingProvider: "unknown"}, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestNew_MissingAPIKey(t *testing.T) {
	for _, provider := range []string{"openai", "voyage", "cohere"} {
		t.Run(provider, func(t *testing.T) {
			_, err := New(&config.Config{EmbeddingProvider: provider}, zap.NewNop())
			if err == nil {
				t.Fatalf("expected error for missing API key")
			}
		})
	}
}

func TestNew_AllProviders(t *testing.T) {
	tests := []struct {
		provider string
		dim      int
	}{
		{"ollama", 768},
		{"openai", 1536},
		{"voyage", 1024},
		{"cohere", 1024},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			cfg := &config.Config{
				EmbeddingProvider: tt.provider,
				EmbeddingAPIKey:   "test-key",
				OllamaURL:         "http://localhost:11434",
			}
			e, err := New(cfg, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			if e.Dimension() != tt.dim {
				t.Errorf("expected dimension %d, got %d", tt.dim, e.Dimension())
			}
		})
	}
}

// --- Ollama tests ---

func TestOllama_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(ollamaEmbedResponse{ //nolint:errcheck // test
			Embeddings: [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
		})
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "test", zap.NewNop())
	embeddings, err := o.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("embed failed: %v", err)
	}
	if len(embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embeddings))
	}
}

func TestOllama_Embed_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "test", zap.NewNop())
	_, err := o.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOllama_Embed_ConnectionError(t *testing.T) {
	o := NewOllama("http://localhost:59999", "test", zap.NewNop())
	_, err := o.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- OpenAI tests ---

func TestOpenAI_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(openAIEmbedResponse{ //nolint:errcheck // test
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{{Embedding: []float32{0.1, 0.2}}, {Embedding: []float32{0.3, 0.4}}},
		})
	}))
	defer srv.Close()

	o := NewOpenAI("test-key", "test", zap.NewNop())
	o.baseURL = srv.URL

	embeddings, err := o.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("embed failed: %v", err)
	}
	if len(embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embeddings))
	}
}

func TestOpenAI_Embed_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	o := NewOpenAI("key", "test", zap.NewNop())
	o.baseURL = srv.URL
	_, err := o.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Voyage tests ---

func TestVoyage_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(voyageEmbedResponse{ //nolint:errcheck // test
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{{Embedding: []float32{0.1, 0.2}}},
		})
	}))
	defer srv.Close()

	v := NewVoyage("key", "test", zap.NewNop())
	v.baseURL = srv.URL

	embeddings, err := v.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(embeddings) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(embeddings))
	}
}

func TestVoyage_Embed_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	v := NewVoyage("key", "test", zap.NewNop())
	v.baseURL = srv.URL
	_, err := v.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Cohere tests ---

func TestCohere_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(cohereEmbedResponse{ //nolint:errcheck // test
			Embeddings: struct {
				Float [][]float32 `json:"float"`
			}{Float: [][]float32{{0.1, 0.2}}},
		})
	}))
	defer srv.Close()

	c := NewCohere("key", "test", zap.NewNop())
	c.baseURL = srv.URL

	embeddings, err := c.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(embeddings) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(embeddings))
	}
}

func TestCohere_Embed_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewCohere("key", "test", zap.NewNop())
	c.baseURL = srv.URL
	_, err := c.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Ollama EnsureModel ---

func TestOllama_EnsureModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "success"}) //nolint:errcheck // test
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "test", zap.NewNop())
	if err := o.EnsureModel(context.Background()); err != nil {
		t.Fatalf("ensure model failed: %v", err)
	}
}

func TestOllama_EnsureModel_Error(t *testing.T) {
	o := NewOllama("http://localhost:59999", "test", zap.NewNop())
	if err := o.EnsureModel(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// --- Dimension tests ---

func TestOllama_Dimension(t *testing.T) {
	tests := map[string]int{"nomic-embed-text": 768, "mxbai-embed-large": 1024, "all-minilm": 384, "unknown": 768}
	for model, dim := range tests {
		if NewOllama("", model, zap.NewNop()).Dimension() != dim {
			t.Errorf("model %s: expected %d", model, dim)
		}
	}
}

func TestOpenAI_Dimension(t *testing.T) {
	tests := map[string]int{"text-embedding-3-small": 1536, "text-embedding-3-large": 3072, "text-embedding-ada-002": 1536, "unknown": 1536}
	for model, dim := range tests {
		if NewOpenAI("k", model, zap.NewNop()).Dimension() != dim {
			t.Errorf("model %s: expected %d", model, dim)
		}
	}
}

func TestVoyage_Dimension(t *testing.T) {
	tests := map[string]int{"voyage-3-large": 1024, "voyage-3": 1024, "voyage-3-lite": 512, "unknown": 1024}
	for model, dim := range tests {
		if NewVoyage("k", model, zap.NewNop()).Dimension() != dim {
			t.Errorf("model %s: expected %d", model, dim)
		}
	}
}

func TestCohere_Dimension(t *testing.T) {
	tests := map[string]int{"embed-v4.0": 1024, "embed-english-v3.0": 1024, "embed-english-light-v3.0": 384, "embed-multilingual-v3.0": 1024, "unknown": 1024}
	for model, dim := range tests {
		if NewCohere("k", model, zap.NewNop()).Dimension() != dim {
			t.Errorf("model %s: expected %d", model, dim)
		}
	}
}
