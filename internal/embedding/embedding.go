// Package embedding provides a unified interface for text embedding via multiple providers.
package embedding

import (
	"context"
	"fmt"

	"github.com/muty/nexus/internal/config"
	"go.uber.org/zap"
)

// Embedder generates vector embeddings from text.
type Embedder interface {
	// Embed generates embeddings for one or more texts.
	Embed(ctx context.Context, texts []string) ([][]float32, error)

	// Dimension returns the embedding vector dimension.
	Dimension() int
}

// New creates an Embedder based on the configured provider.
// Returns nil if no provider is configured (embeddings disabled).
func New(cfg *config.Config, log *zap.Logger) (Embedder, error) {
	var inner Embedder

	switch cfg.EmbeddingProvider {
	case "":
		return nil, nil // embeddings disabled
	case "ollama":
		model := cfg.EmbeddingModel
		if model == "" {
			model = "nomic-embed-text"
		}
		inner = NewOllama(cfg.OllamaURL, model, log)
	case "openai":
		model := cfg.EmbeddingModel
		if model == "" {
			model = "text-embedding-3-small"
		}
		if cfg.EmbeddingAPIKey == "" {
			return nil, fmt.Errorf("embedding: NEXUS_EMBEDDING_API_KEY required for openai provider")
		}
		inner = NewOpenAI(cfg.EmbeddingAPIKey, model, log)
	case "voyage":
		model := cfg.EmbeddingModel
		if model == "" {
			model = "voyage-3-large"
		}
		if cfg.EmbeddingAPIKey == "" {
			return nil, fmt.Errorf("embedding: NEXUS_EMBEDDING_API_KEY required for voyage provider")
		}
		inner = NewVoyage(cfg.EmbeddingAPIKey, model, log)
	case "cohere":
		model := cfg.EmbeddingModel
		if model == "" {
			model = "embed-v4.0"
		}
		if cfg.EmbeddingAPIKey == "" {
			return nil, fmt.Errorf("embedding: NEXUS_EMBEDDING_API_KEY required for cohere provider")
		}
		inner = NewCohere(cfg.EmbeddingAPIKey, model, log)
	default:
		return nil, fmt.Errorf("embedding: unknown provider %q (supported: ollama, openai, voyage, cohere)", cfg.EmbeddingProvider)
	}

	return NewRetryEmbedder(inner, log), nil
}
