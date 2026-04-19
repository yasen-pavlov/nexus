package api

import (
	"context"
	"sync"

	"github.com/muty/nexus/internal/config"
	"github.com/muty/nexus/internal/embedding"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// EmbeddingManager provides thread-safe access to the current embedding provider.
// It supports hot-reloading when settings change via the API.
type EmbeddingManager struct {
	mu       sync.RWMutex
	embedder embedding.Embedder
	provider string
	model    string
	store    *store.Store
	log      *zap.Logger
}

// NewEmbeddingManager creates an EmbeddingManager.
func NewEmbeddingManager(st *store.Store, log *zap.Logger) *EmbeddingManager {
	return &EmbeddingManager{store: st, log: log}
}

// Get returns the current embedder (may be nil if disabled).
func (m *EmbeddingManager) Get() embedding.Embedder {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.embedder
}

// Set replaces the current embedder.
func (m *EmbeddingManager) Set(e embedding.Embedder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.embedder = e
}

// setActive replaces the embedder together with the provider + model labels
// the admin UI wants to surface. Callers that swap the embedder via Set lose
// the labels; this keeps them in sync.
func (m *EmbeddingManager) setActive(e embedding.Embedder, provider, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.embedder = e
	m.provider = provider
	m.model = model
}

// Dimension returns the current embedding dimension (0 if disabled).
func (m *EmbeddingManager) Dimension() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.embedder == nil {
		return 0
	}
	return m.embedder.Dimension()
}

// Provider returns the label of the active embedding provider ("" when
// disabled). Populated when the manager loads from DB or UpdateFromSettings
// runs, so the admin stats surface can display it without re-reading the
// settings table.
func (m *EmbeddingManager) Provider() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.embedder == nil {
		return ""
	}
	return m.provider
}

// Model returns the label of the active embedding model ("" when disabled).
func (m *EmbeddingManager) Model() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.embedder == nil {
		return ""
	}
	return m.model
}

// LoadFromDB loads embedding settings from the database and creates the embedder.
// Falls back to the provided config if no DB settings exist.
func (m *EmbeddingManager) LoadFromDB(ctx context.Context, appCfg *config.Config) error {
	keys := []string{"embedding_provider", "embedding_model", "embedding_api_key", "ollama_url"}
	settings, err := m.store.GetSettings(ctx, keys)
	if err != nil {
		return err
	}

	// Build config from DB settings, falling back to env vars
	cfg := &config.Config{
		EmbeddingProvider: or(settings["embedding_provider"], appCfg.EmbeddingProvider),
		EmbeddingModel:    or(settings["embedding_model"], appCfg.EmbeddingModel),
		EmbeddingAPIKey:   or(settings["embedding_api_key"], appCfg.EmbeddingAPIKey),
		OllamaURL:         or(settings["ollama_url"], appCfg.OllamaURL),
	}

	embedder, err := embedding.New(cfg, m.log)
	if err != nil {
		return err
	}

	m.setActive(embedder, cfg.EmbeddingProvider, cfg.EmbeddingModel)
	return nil
}

// UpdateFromSettings creates a new embedder from the given settings and persists them.
func (m *EmbeddingManager) UpdateFromSettings(ctx context.Context, provider, model, apiKey, ollamaURL string) error {
	cfg := &config.Config{
		EmbeddingProvider: provider,
		EmbeddingModel:    model,
		EmbeddingAPIKey:   apiKey,
		OllamaURL:         ollamaURL,
	}

	// Validate by creating the embedder
	embedder, err := embedding.New(cfg, m.log)
	if err != nil {
		return err
	}

	// Persist to DB
	settings := map[string]string{
		"embedding_provider": provider,
		"embedding_model":    model,
		"embedding_api_key":  apiKey,
		"ollama_url":         ollamaURL,
	}
	if err := m.store.SetSettings(ctx, settings); err != nil {
		return err
	}

	// Hot-swap the embedder together with the labels the admin UI surfaces.
	m.setActive(embedder, provider, model)

	if embedder != nil {
		m.log.Info("embedding provider updated",
			zap.String("provider", provider),
			zap.String("model", model),
		)
	} else {
		m.log.Info("embedding disabled")
	}

	return nil
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
