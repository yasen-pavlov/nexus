package api

import (
	"encoding/json"
	"net/http"

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
}

type llmSettingsRequest struct {
	DefaultModel    string   `json:"default_model"`
	AnthropicAPIKey string   `json:"anthropic_api_key"`
	OpenAIAPIKey    string   `json:"openai_api_key"`
	OllamaURL       string   `json:"ollama_url"`
	Allowlist       []string `json:"allowlist"`
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
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetLLMModels godoc
//
//	@Summary	List available LLM models
//	@Description	Returns the visible LLM models filtered by configured providers and admin allowlist. Non-admin users see the same list. Used by the per-message model picker in the Ask UI.
//	@Tags		llm
//	@Produce	json
//	@Success	200	{array}	llmModelResponse
//	@Security	BearerAuth
//	@Router		/llm/models [get]
func (h *handler) GetLLMModels(w http.ResponseWriter, _ *http.Request) {
	if h.lm == nil {
		writeJSON(w, http.StatusOK, []llmModelResponse{})
		return
	}
	models := h.lm.Models()
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
