// Package rerank provides a unified interface for document reranking via multiple providers.
package rerank

import (
	"context"
	"fmt"

	"github.com/muty/nexus/internal/config"
	"go.uber.org/zap"
)

// Reranker scores query-document pairs for relevance using a cross-encoder model.
type Reranker interface {
	// Rerank scores each document against the query and returns results sorted by relevance.
	Rerank(ctx context.Context, query string, documents []string) ([]Result, error)
}

// Result is a single reranking result.
type Result struct {
	Index int     `json:"index"`
	Score float64 `json:"relevance_score"`
}

// New creates a Reranker based on the configured provider.
// Returns nil if no provider is configured (reranking disabled).
func New(cfg *config.Config, log *zap.Logger) (Reranker, error) {
	var inner Reranker

	switch cfg.RerankProvider {
	case "":
		return nil, nil
	case "voyage":
		model := cfg.RerankModel
		if model == "" {
			model = "rerank-2"
		}
		apiKey := cfg.RerankAPIKey
		if apiKey == "" {
			apiKey = cfg.EmbeddingAPIKey // fall back to embedding key for same provider
		}
		if apiKey == "" {
			return nil, fmt.Errorf("rerank: API key required for voyage provider")
		}
		inner = NewVoyage(apiKey, model)
	case "cohere":
		model := cfg.RerankModel
		if model == "" {
			model = "rerank-v3.5"
		}
		apiKey := cfg.RerankAPIKey
		if apiKey == "" {
			apiKey = cfg.EmbeddingAPIKey
		}
		if apiKey == "" {
			return nil, fmt.Errorf("rerank: API key required for cohere provider")
		}
		inner = NewCohere(apiKey, model)
	default:
		return nil, fmt.Errorf("rerank: unknown provider %q (supported: voyage, cohere)", cfg.RerankProvider)
	}

	return NewRetryReranker(inner, log), nil
}
