// Package config handles application configuration loaded from environment variables.
package config

import "github.com/kelseyhightower/envconfig"

type Config struct {
	Port          int    `envconfig:"PORT" default:"8080"`
	DatabaseURL   string `envconfig:"DATABASE_URL" required:"true"`
	OpenSearchURL string `envconfig:"OPENSEARCH_URL" default:"http://localhost:9200"`
	LogLevel      string `envconfig:"LOG_LEVEL" default:"info"`

	// OpenSearch authentication (all optional; empty = no auth, plain HTTP —
	// the default bundled deployment relies on docker-network isolation).
	// Set these to connect to a security-plugin-enabled cluster. See the
	// docker-compose.secure.yml override and README for the opt-in flow.
	OpenSearchUsername           string `envconfig:"OPENSEARCH_USERNAME"`
	OpenSearchPassword           string `envconfig:"OPENSEARCH_PASSWORD"`
	OpenSearchCAFile             string `envconfig:"OPENSEARCH_CA_FILE"`              // path to a PEM CA bundle to verify the server cert
	OpenSearchInsecureSkipVerify bool   `envconfig:"OPENSEARCH_INSECURE_SKIP_VERIFY"` // skip TLS verification (demo certs on a private bridge)

	// Content extraction
	TikaURL string `envconfig:"TIKA_URL" default:"http://localhost:9998"`

	// Embedding
	EmbeddingProvider string `envconfig:"EMBEDDING_PROVIDER"` // ollama, openai, voyage, cohere (empty = disabled)
	EmbeddingModel    string `envconfig:"EMBEDDING_MODEL"`
	EmbeddingAPIKey   string `envconfig:"EMBEDDING_API_KEY"`
	OllamaURL         string `envconfig:"OLLAMA_URL" default:"http://localhost:11434"`

	// Authentication
	JWTSecret string `envconfig:"JWT_SECRET"` // secret for signing JWT tokens; auto-generated if empty

	// CORS
	CORSOrigins []string `envconfig:"CORS_ORIGINS" default:"http://localhost:5173"` // comma-separated list of allowed origins

	// Reranking
	RerankProvider string `envconfig:"RERANK_PROVIDER"` // voyage, cohere (empty = disabled)
	RerankModel    string `envconfig:"RERANK_MODEL"`
	RerankAPIKey   string `envconfig:"RERANK_API_KEY"`

	// LLM (answer generation for RAG). Provider keys are independent so a
	// single deployment can mix-and-match (e.g. Anthropic for synthesis,
	// Ollama for the cheap rewriter). Empty default model = first-boot
	// auto-detect picks the cheapest available across the configured
	// providers (resolved by the LLM manager, not envconfig).
	LLMDefaultModel    string `envconfig:"LLM_DEFAULT_MODEL"` // provider-prefixed, e.g. "anthropic:claude-sonnet-4-6"
	LLMAnthropicAPIKey string `envconfig:"LLM_ANTHROPIC_API_KEY"`
	LLMOpenAIAPIKey    string `envconfig:"LLM_OPENAI_API_KEY"`
	// LLMOllamaURL falls back to OllamaURL when empty so single-Ollama
	// deployments don't have to set the URL twice.
	LLMOllamaURL string `envconfig:"LLM_OLLAMA_URL"`

	// Encryption
	EncryptionKey string `envconfig:"ENCRYPTION_KEY"` // 64-char hex string (32 bytes) for AES-256-GCM

	// Filesystem connector
	FSRootPath string `envconfig:"FS_ROOT_PATH"`
	FSPatterns string `envconfig:"FS_PATTERNS" default:"*.txt,*.md"`

	// Binary content cache — stores attachments/media for connectors where
	// re-fetch is slow (IMAP) or unreliable (Telegram media expiry).
	// Mount this as a Docker volume in production to persist across restarts.
	BinaryStorePath string `envconfig:"BINARY_STORE_PATH" default:"data/binaries"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("nexus", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
