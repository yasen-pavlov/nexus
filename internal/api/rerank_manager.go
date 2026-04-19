package api

import (
	"context"
	"sync"

	"github.com/muty/nexus/internal/config"
	"github.com/muty/nexus/internal/rerank"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// RerankManager provides thread-safe access to the current reranking provider.
type RerankManager struct {
	mu       sync.RWMutex
	reranker rerank.Reranker
	provider string
	model    string
	store    *store.Store
	log      *zap.Logger
}

// NewRerankManager creates a RerankManager.
func NewRerankManager(st *store.Store, log *zap.Logger) *RerankManager {
	return &RerankManager{store: st, log: log}
}

// Get returns the current reranker (may be nil if disabled).
func (m *RerankManager) Get() rerank.Reranker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reranker
}

// Set replaces the current reranker.
func (m *RerankManager) Set(r rerank.Reranker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reranker = r
}

// setActive replaces the reranker together with the provider + model labels
// the admin UI surfaces.
func (m *RerankManager) setActive(r rerank.Reranker, provider, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reranker = r
	m.provider = provider
	m.model = model
}

// Provider returns the label of the active rerank provider ("" when disabled).
func (m *RerankManager) Provider() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.reranker == nil {
		return ""
	}
	return m.provider
}

// Model returns the label of the active rerank model ("" when disabled).
func (m *RerankManager) Model() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.reranker == nil {
		return ""
	}
	return m.model
}

// LoadFromDB loads rerank settings from the database and creates the reranker.
func (m *RerankManager) LoadFromDB(ctx context.Context, appCfg *config.Config) error {
	keys := []string{"rerank_provider", "rerank_model", "rerank_api_key", "embedding_api_key"}
	settings, err := m.store.GetSettings(ctx, keys)
	if err != nil {
		return err
	}

	cfg := &config.Config{
		RerankProvider:  or(settings["rerank_provider"], appCfg.RerankProvider),
		RerankModel:     or(settings["rerank_model"], appCfg.RerankModel),
		RerankAPIKey:    or(settings["rerank_api_key"], appCfg.RerankAPIKey),
		EmbeddingAPIKey: or(settings["embedding_api_key"], appCfg.EmbeddingAPIKey),
	}

	reranker, err := rerank.New(cfg, m.log)
	if err != nil {
		return err
	}

	m.setActive(reranker, cfg.RerankProvider, cfg.RerankModel)
	return nil
}

// UpdateFromSettings creates a new reranker from the given settings and persists them.
func (m *RerankManager) UpdateFromSettings(ctx context.Context, provider, model, apiKey string) error {
	// Load embedding API key as fallback for same-provider reranking
	embeddingKey, _ := m.store.GetSetting(ctx, "embedding_api_key")

	cfg := &config.Config{
		RerankProvider:  provider,
		RerankModel:     model,
		RerankAPIKey:    apiKey,
		EmbeddingAPIKey: embeddingKey,
	}

	reranker, err := rerank.New(cfg, m.log)
	if err != nil {
		return err
	}

	settings := map[string]string{
		"rerank_provider": provider,
		"rerank_model":    model,
		"rerank_api_key":  apiKey,
	}
	if err := m.store.SetSettings(ctx, settings); err != nil {
		return err
	}

	m.setActive(reranker, provider, model)

	if reranker != nil {
		m.log.Info("rerank provider updated",
			zap.String("provider", provider),
			zap.String("model", model),
		)
	} else {
		m.log.Info("reranking disabled")
	}

	return nil
}
