// Package config handles application configuration loaded from environment variables.
package config

import "github.com/kelseyhightower/envconfig"

type Config struct {
	Port          int    `envconfig:"PORT" default:"8080"`
	DatabaseURL   string `envconfig:"DATABASE_URL" required:"true"`
	OpenSearchURL string `envconfig:"OPENSEARCH_URL" default:"http://localhost:9200"`
	LogLevel      string `envconfig:"LOG_LEVEL" default:"info"`

	// Content extraction
	TikaURL string `envconfig:"TIKA_URL" default:"http://localhost:9998"`

	// Embedding
	EmbeddingProvider string `envconfig:"EMBEDDING_PROVIDER"` // ollama, openai, voyage, cohere (empty = disabled)
	EmbeddingModel    string `envconfig:"EMBEDDING_MODEL"`
	EmbeddingAPIKey   string `envconfig:"EMBEDDING_API_KEY"`
	OllamaURL         string `envconfig:"OLLAMA_URL" default:"http://localhost:11434"`

	// Reranking
	RerankProvider string `envconfig:"RERANK_PROVIDER"` // voyage, cohere (empty = disabled)
	RerankModel    string `envconfig:"RERANK_MODEL"`
	RerankAPIKey   string `envconfig:"RERANK_API_KEY"`

	// Encryption
	EncryptionKey string `envconfig:"ENCRYPTION_KEY"` // 64-char hex string (32 bytes) for AES-256-GCM

	// Filesystem connector
	FSRootPath string `envconfig:"FS_ROOT_PATH"`
	FSPatterns string `envconfig:"FS_PATTERNS" default:"*.txt,*.md"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("nexus", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
