package api

import (
	"encoding/json"
	"net/http"

	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/llm"
	"go.uber.org/zap"
)

// llmSettingsResponse mirrors the wire shape exposed to the admin UI. API keys
// are masked on response; the client preserves them by sending back the masked
// value, which the handler restores from the DB.
type llmSettingsResponse struct {
	DefaultModel    string   `json:"default_model"`
	AnthropicAPIKey string   `json:"anthropic_api_key"`
	OpenAIAPIKey    string   `json:"openai_api_key"`
	OllamaURL       string   `json:"ollama_url"`
	Allowlist       []string `json:"allowlist"`
	// RewriterModel is the cheap model used for query rewriting and
	// auto-titling. Empty disables both features.
	RewriterModel string `json:"rewriter_model"`
}

type llmSettingsRequest struct {
	DefaultModel    string   `json:"default_model"`
	AnthropicAPIKey string   `json:"anthropic_api_key"`
	OpenAIAPIKey    string   `json:"openai_api_key"`
	OllamaURL       string   `json:"ollama_url"`
	Allowlist       []string `json:"allowlist"`
	RewriterModel   string   `json:"rewriter_model"`
}

// llmDefaultResponse is the shape served by GET /api/llm/default. The
// frontend composer uses the value as the system-wide fallback for the
// per-message model picker (chat-level default still wins; this beats
// the user's localStorage `lastUsed` so admin choices propagate).
type llmDefaultResponse struct {
	DefaultModel string `json:"default_model"`
}

// llmModelResponse is the shape served by GET /api/llm/models. The frontend
// composer uses it to populate the per-message model picker.
type llmModelResponse struct {
	ID                string  `json:"id"`
	Provider          string  `json:"provider"`
	BareID            string  `json:"bare_id"`
	DisplayName       string  `json:"display_name"`
	ContextWindow     int     `json:"context_window"`
	SupportsCitations bool    `json:"supports_citations"`
	SupportsTools     bool    `json:"supports_tools"`
	SupportsVision    bool    `json:"supports_vision"`
	SupportsCaching   bool    `json:"supports_caching"`
	InputCostPerMtok  float64 `json:"input_cost_per_mtok"`
	OutputCostPerMtok float64 `json:"output_cost_per_mtok"`
	TypicalTTFTms     int     `json:"typical_ttft_ms"`
}

// GetLLMSettings godoc
//
//	@Summary	Get LLM settings
//	@Description	Returns the configured LLM providers, default model, and allowlist. API keys are masked.
//	@Tags		settings
//	@Produce	json
//	@Success	200	{object}	llmSettingsResponse
//	@Security	BearerAuth
//	@Router		/settings/llm [get]
func (h *handler) GetLLMSettings(w http.ResponseWriter, _ *http.Request) {
	if h.lm == nil {
		writeError(w, http.StatusInternalServerError, "llm manager not configured")
		return
	}
	snap := h.lm.Snapshot()
	resp := llmSettingsResponse{
		DefaultModel:    snap.DefaultModel,
		AnthropicAPIKey: maskAPIKey(snap.AnthropicAPIKey),
		OpenAIAPIKey:    maskAPIKey(snap.OpenAIAPIKey),
		OllamaURL:       snap.OllamaURL,
		Allowlist:       snap.Allowlist,
		RewriterModel:   snap.RewriterModel,
	}
	writeJSON(w, http.StatusOK, resp)
}

// UpdateLLMSettings godoc
//
//	@Summary	Update LLM settings
//	@Description	Updates the configured providers, default model, and allowlist. Masked API keys (****...) are preserved from the existing values. Hot-swaps the registry on success — no restart required.
//	@Tags		settings
//	@Accept		json
//	@Produce	json
//	@Param		request	body	llmSettingsRequest	true	"LLM settings"
//	@Success	200	{object}	llmSettingsResponse
//	@Failure	400	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/settings/llm [put]
func (h *handler) UpdateLLMSettings(w http.ResponseWriter, r *http.Request) {
	if h.lm == nil {
		writeError(w, http.StatusInternalServerError, "llm manager not configured")
		return
	}

	var req llmSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	current := h.lm.Snapshot()

	// Mask round-trip: clients post back the masked key to mean "leave it
	// alone". Empty string means "clear it" — let admins disable a provider.
	anthropicKey := req.AnthropicAPIKey
	if isMasked(anthropicKey) {
		anthropicKey = current.AnthropicAPIKey
	}
	openaiKey := req.OpenAIAPIKey
	if isMasked(openaiKey) {
		openaiKey = current.OpenAIAPIKey
	}

	snap := LLMSnapshot{
		DefaultModel:    req.DefaultModel,
		AnthropicAPIKey: anthropicKey,
		OpenAIAPIKey:    openaiKey,
		OllamaURL:       req.OllamaURL,
		Allowlist:       req.Allowlist,
		RewriterModel:   req.RewriterModel,
	}

	if err := h.lm.UpdateFromSettings(r.Context(), snap); err != nil {
		h.log.Warn("update llm settings failed", zap.Error(err))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := llmSettingsResponse{
		DefaultModel:    snap.DefaultModel,
		AnthropicAPIKey: maskAPIKey(snap.AnthropicAPIKey),
		OpenAIAPIKey:    maskAPIKey(snap.OpenAIAPIKey),
		OllamaURL:       snap.OllamaURL,
		Allowlist:       snap.Allowlist,
		RewriterModel:   snap.RewriterModel,
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetLLMModels godoc
//
//	@Summary	List available LLM models
//	@Description	Returns LLM models filtered by configured providers and (by default) admin allowlist. The per-message picker uses the default response. Pass `?include_disallowed=true` (admin-only) to get the pre-allowlist set — used by the admin allowlist editor so deselected rows stay visible for re-ticking.
//	@Tags		llm
//	@Produce	json
//	@Param		include_disallowed	query	bool	false	"When true, ignore the allowlist filter (admin-only)"
//	@Success	200	{array}	llmModelResponse
//	@Security	BearerAuth
//	@Router		/llm/models [get]
func (h *handler) GetLLMModels(w http.ResponseWriter, r *http.Request) {
	if h.lm == nil {
		writeJSON(w, http.StatusOK, []llmModelResponse{})
		return
	}
	var models []llm.ModelInfo
	if r.URL.Query().Get("include_disallowed") == "true" {
		// Pre-allowlist set is admin-only — non-admins should never see
		// catalog rows the admin has deliberately hidden.
		claims := auth.UserFromContext(r.Context())
		if claims == nil || claims.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin only")
			return
		}
		models = h.lm.AllConfiguredModels()
	} else {
		models = h.lm.Models()
	}
	resp := make([]llmModelResponse, 0, len(models))
	for _, m := range models {
		resp = append(resp, llmModelResponse{
			ID:                m.ID,
			Provider:          m.Provider,
			BareID:            m.BareID,
			DisplayName:       m.DisplayName,
			ContextWindow:     m.ContextWindow,
			SupportsCitations: m.SupportsCitations,
			SupportsTools:     m.SupportsTools,
			SupportsVision:    m.SupportsVision,
			SupportsCaching:   m.SupportsCaching,
			InputCostPerMtok:  m.InputCostPerMtok,
			OutputCostPerMtok: m.OutputCostPerMtok,
			TypicalTTFTms:     m.TypicalTTFTms,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetLLMDefault godoc
//
//	@Summary	Get the system-wide default LLM model
//	@Description	Returns the configured default model id (provider-prefixed). The per-message picker falls back to this value when the user has no chat-level override; lets admin choices propagate to all users without requiring localStorage clears.
//	@Tags		llm
//	@Produce	json
//	@Success	200	{object}	llmDefaultResponse
//	@Security	BearerAuth
//	@Router		/llm/default [get]
func (h *handler) GetLLMDefault(w http.ResponseWriter, _ *http.Request) {
	if h.lm == nil {
		writeJSON(w, http.StatusOK, llmDefaultResponse{})
		return
	}
	writeJSON(w, http.StatusOK, llmDefaultResponse{DefaultModel: h.lm.DefaultModel()})
}
