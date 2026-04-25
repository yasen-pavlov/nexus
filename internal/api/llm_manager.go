package api

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/muty/nexus/internal/config"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/llm/anthropic"
	"github.com/muty/nexus/internal/llm/ollama"
	"github.com/muty/nexus/internal/llm/openai"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// Settings keys persisted in the settings table.
const (
	llmKeyDefaultModel    = "llm_default_model"
	llmKeyAnthropicAPIKey = "llm_anthropic_api_key"
	llmKeyOpenAIAPIKey    = "llm_openai_api_key"
	llmKeyOllamaURL       = "llm_ollama_url"
	// llm_models_allowlist is a JSON array of provider-prefixed ids.
	llmKeyModelsAllowlist = "llm_models_allowlist"
)

// LLMManager owns the per-provider Generators and the model registry, with
// hot-reload via UpdateFromSettings(). Mirrors EmbeddingManager / RerankManager.
type LLMManager struct {
	mu       sync.RWMutex
	registry llm.Registry
	// Snapshot of the active settings for the admin GET handler. Keys are
	// masked before they leave the struct.
	defaultModel    string
	anthropicAPIKey string
	openaiAPIKey    string
	ollamaURL       string
	allowlist       []string

	store *store.Store
	log   *zap.Logger
}

// NewLLMManager constructs an LLMManager. Initial state has no providers
// configured; call LoadFromDB to populate.
func NewLLMManager(st *store.Store, log *zap.Logger) *LLMManager {
	return &LLMManager{
		store:    st,
		log:      log,
		registry: llm.NewRegistry(llm.RegistryConfig{}),
	}
}

// Get returns the current registry. Always non-nil; may be empty.
func (m *LLMManager) Get() llm.Registry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.registry
}

// DefaultModel returns the configured default model id (provider-prefixed).
func (m *LLMManager) DefaultModel() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultModel
}

// LLMSnapshot is the active settings the admin GET handler reads back. API
// keys are returned in plaintext so the handler can decide on masking — the
// manager itself does not assume the caller wants masking.
type LLMSnapshot struct {
	DefaultModel    string
	AnthropicAPIKey string
	OpenAIAPIKey    string
	OllamaURL       string
	Allowlist       []string
}

// Snapshot returns a copy of the current settings.
func (m *LLMManager) Snapshot() LLMSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	allow := make([]string, len(m.allowlist))
	copy(allow, m.allowlist)
	return LLMSnapshot{
		DefaultModel:    m.defaultModel,
		AnthropicAPIKey: m.anthropicAPIKey,
		OpenAIAPIKey:    m.openaiAPIKey,
		OllamaURL:       m.ollamaURL,
		Allowlist:       allow,
	}
}

// Models returns the visible models per the active allowlist + provider keys.
func (m *LLMManager) Models() []llm.ModelInfo {
	return m.Get().Models()
}

// LoadFromDB reads LLM settings from the DB and constructs the registry.
// Falls back to the env config for any missing keys.
func (m *LLMManager) LoadFromDB(ctx context.Context, appCfg *config.Config) error {
	keys := []string{
		llmKeyDefaultModel,
		llmKeyAnthropicAPIKey,
		llmKeyOpenAIAPIKey,
		llmKeyOllamaURL,
		llmKeyModelsAllowlist,
	}
	settings, err := m.store.GetSettings(ctx, keys)
	if err != nil {
		return err
	}

	defaults := llmDefaults{
		defaultModel:    or(settings[llmKeyDefaultModel], appCfg.LLMDefaultModel),
		anthropicAPIKey: or(settings[llmKeyAnthropicAPIKey], appCfg.LLMAnthropicAPIKey),
		openaiAPIKey:    or(settings[llmKeyOpenAIAPIKey], appCfg.LLMOpenAIAPIKey),
		// Ollama URL chain: dedicated LLM setting → dedicated env →
		// shared Ollama URL (kept in sync with the embedding side).
		ollamaURL: orMany(settings[llmKeyOllamaURL], appCfg.LLMOllamaURL, appCfg.OllamaURL),
		allowlist: parseAllowlist(settings[llmKeyModelsAllowlist]),
	}

	m.swap(defaults)
	return nil
}

// UpdateFromSettings validates new settings, persists them, and hot-swaps
// the registry. Empty providers are unconfigured; the registry's Get for an
// unconfigured model returns a clear error.
func (m *LLMManager) UpdateFromSettings(ctx context.Context, snap LLMSnapshot) error {
	defaults := llmDefaults{
		defaultModel:    snap.DefaultModel,
		anthropicAPIKey: snap.AnthropicAPIKey,
		openaiAPIKey:    snap.OpenAIAPIKey,
		ollamaURL:       snap.OllamaURL,
		allowlist:       snap.Allowlist,
	}

	// Validate the default model resolves under the new config — fast
	// failure beats a confusing "provider not configured" later.
	registry := buildRegistry(defaults, m.log)
	if defaults.defaultModel != "" {
		if _, _, err := registry.Get(defaults.defaultModel); err != nil {
			return err
		}
	}

	allowlistJSON, err := json.Marshal(defaults.allowlist)
	if err != nil {
		return err
	}
	persisted := map[string]string{
		llmKeyDefaultModel:    defaults.defaultModel,
		llmKeyAnthropicAPIKey: defaults.anthropicAPIKey,
		llmKeyOpenAIAPIKey:    defaults.openaiAPIKey,
		llmKeyOllamaURL:       defaults.ollamaURL,
		llmKeyModelsAllowlist: string(allowlistJSON),
	}
	if err := m.store.SetSettings(ctx, persisted); err != nil {
		return err
	}

	m.swap(defaults)
	m.log.Info("llm settings updated",
		zap.String("default_model", defaults.defaultModel),
		zap.Bool("anthropic", defaults.anthropicAPIKey != ""),
		zap.Bool("openai", defaults.openaiAPIKey != ""),
		zap.Bool("ollama", defaults.ollamaURL != ""),
		zap.Int("allowlist_size", len(defaults.allowlist)),
	)
	return nil
}

// llmDefaults is the inputs to a registry build.
type llmDefaults struct {
	defaultModel    string
	anthropicAPIKey string
	openaiAPIKey    string
	ollamaURL       string
	allowlist       []string
}

// swap atomically replaces the registry + snapshot.
func (m *LLMManager) swap(d llmDefaults) {
	registry := buildRegistry(d, m.log)
	m.mu.Lock()
	m.registry = registry
	m.defaultModel = d.defaultModel
	m.anthropicAPIKey = d.anthropicAPIKey
	m.openaiAPIKey = d.openaiAPIKey
	m.ollamaURL = d.ollamaURL
	m.allowlist = append([]string(nil), d.allowlist...)
	m.mu.Unlock()
}

// buildRegistry assembles per-provider Generators (each wrapped by retry)
// and hands them to llm.NewRegistry.
func buildRegistry(d llmDefaults, log *zap.Logger) llm.Registry {
	gens := make(map[string]llm.Generator)
	if d.anthropicAPIKey != "" {
		c := anthropic.New(d.anthropicAPIKey, "", log)
		gens[llm.ProviderAnthropic] = llm.NewRetryGenerator(c, log)
	}
	if d.openaiAPIKey != "" {
		c := openai.New(d.openaiAPIKey, "", log)
		gens[llm.ProviderOpenAI] = llm.NewRetryGenerator(c, log)
	}
	if d.ollamaURL != "" {
		c := ollama.New(d.ollamaURL, log)
		gens[llm.ProviderOllama] = llm.NewRetryGenerator(c, log)
	}
	return llm.NewRegistry(llm.RegistryConfig{
		Generators: gens,
		Allowlist:  d.allowlist,
	})
}

// parseAllowlist tolerates both an empty value and a malformed value
// (treats either as "no allowlist"). Returns nil to mean "expose every
// catalog model whose provider is configured".
func parseAllowlist(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// orMany returns the first non-empty arg.
func orMany(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
