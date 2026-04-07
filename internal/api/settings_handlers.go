package api

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

type embeddingSettingsResponse struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key"`
	OllamaURL string `json:"ollama_url"`
}

type embeddingSettingsRequest struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key"`
	OllamaURL string `json:"ollama_url"`
}

func (h *handler) GetEmbeddingSettings(w http.ResponseWriter, r *http.Request) {
	keys := []string{"embedding_provider", "embedding_model", "embedding_api_key", "ollama_url"}
	settings, err := h.store.GetSettings(r.Context(), keys)
	if err != nil {
		h.log.Error("get embedding settings failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to get settings")
		return
	}

	resp := embeddingSettingsResponse{
		Provider:  settings["embedding_provider"],
		Model:     settings["embedding_model"],
		APIKey:    maskAPIKey(settings["embedding_api_key"]),
		OllamaURL: settings["ollama_url"],
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) UpdateEmbeddingSettings(w http.ResponseWriter, r *http.Request) {
	var req embeddingSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// If API key is masked (unchanged), load the existing key from DB
	if req.APIKey != "" && isMasked(req.APIKey) {
		existing, err := h.store.GetSetting(r.Context(), "embedding_api_key")
		if err != nil {
			h.log.Error("get api key failed", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "failed to get settings")
			return
		}
		req.APIKey = existing
	}

	if err := h.em.UpdateFromSettings(r.Context(), req.Provider, req.Model, req.APIKey, req.OllamaURL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := embeddingSettingsResponse{
		Provider:  req.Provider,
		Model:     req.Model,
		APIKey:    maskAPIKey(req.APIKey),
		OllamaURL: req.OllamaURL,
	}

	writeJSON(w, http.StatusOK, resp)
}

func maskAPIKey(key string) string {
	if len(key) <= 4 {
		return key
	}
	return "****" + key[len(key)-4:]
}

func isMasked(key string) bool {
	return len(key) > 4 && key[:4] == "****"
}
