package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/muty/nexus/internal/connector"
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

// GetEmbeddingSettings godoc
//
//	@Summary	Get embedding settings
//	@Description	Returns current embedding provider configuration. API keys are masked.
//	@Tags		settings
//	@Produce	json
//	@Success	200	{object}	embeddingSettingsResponse
//	@Router		/settings/embedding [get]
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

// UpdateEmbeddingSettings godoc
//
//	@Summary	Update embedding settings
//	@Description	Updates the embedding provider. If provider or model changes, automatically triggers a full re-index. Masked API keys (****...) are preserved.
//	@Tags		settings
//	@Accept		json
//	@Produce	json
//	@Param		request	body	embeddingSettingsRequest	true	"Embedding settings"
//	@Success	200	{object}	embeddingSettingsResponse
//	@Failure	400	{object}	APIResponse
//	@Router		/settings/embedding [put]
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

	// Check if provider or model changed — triggers re-index
	oldProvider, _ := h.store.GetSetting(r.Context(), "embedding_provider")
	oldModel, _ := h.store.GetSetting(r.Context(), "embedding_model")
	oldDim := h.em.Dimension()

	if err := h.em.UpdateFromSettings(r.Context(), req.Provider, req.Model, req.APIKey, req.OllamaURL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// If provider or model changed, trigger re-index
	if req.Provider != oldProvider || req.Model != oldModel {
		newDim := h.em.Dimension()
		h.triggerAutoReindex(r.Context(), oldDim, newDim)
	}

	resp := embeddingSettingsResponse{
		Provider:  req.Provider,
		Model:     req.Model,
		APIKey:    maskAPIKey(req.APIKey),
		OllamaURL: req.OllamaURL,
	}

	writeJSON(w, http.StatusOK, resp)
}

// triggerAutoReindex recreates the index if dimensions changed, clears cursors, and syncs all.
func (h *handler) triggerAutoReindex(ctx context.Context, oldDim, newDim int) {
	if oldDim != newDim {
		if err := h.search.RecreateIndex(ctx, newDim); err != nil {
			h.log.Error("auto-reindex: recreate index failed", zap.Error(err))
			return
		}
		h.log.Info("auto-reindex: index recreated", zap.Int("old_dim", oldDim), zap.Int("new_dim", newDim))
	}

	if err := h.store.DeleteAllSyncCursors(ctx); err != nil {
		h.log.Error("auto-reindex: delete cursors failed", zap.Error(err))
		return
	}

	// Trigger async sync for all connectors
	for name, conn := range h.cm.All() {
		job := h.syncJobs.Start(name, conn.Type())
		go func(n string, c connector.Connector, jobID string) {
			bgCtx := context.Background()
			progress := func(total, processed, errors int) {
				h.syncJobs.Update(jobID, total, processed, errors)
			}
			_, err := h.pipeline.RunWithProgress(bgCtx, c, progress)
			h.syncJobs.Complete(jobID, err)
			if err != nil {
				h.log.Error("auto-reindex: sync failed", zap.String("connector", n), zap.Error(err))
			}
		}(name, conn, job.ID)
	}

	h.log.Info("auto-reindex: triggered sync for all connectors")
}

type rerankSettingsResponse struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
}

type rerankSettingsRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
}

// GetRerankSettings godoc
//
//	@Summary	Get rerank settings
//	@Description	Returns current reranking provider configuration. API keys are masked.
//	@Tags		settings
//	@Produce	json
//	@Success	200	{object}	rerankSettingsResponse
//	@Router		/settings/rerank [get]
func (h *handler) GetRerankSettings(w http.ResponseWriter, r *http.Request) {
	keys := []string{"rerank_provider", "rerank_model", "rerank_api_key"}
	settings, err := h.store.GetSettings(r.Context(), keys)
	if err != nil {
		h.log.Error("get rerank settings failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to get settings")
		return
	}

	resp := rerankSettingsResponse{
		Provider: settings["rerank_provider"],
		Model:    settings["rerank_model"],
		APIKey:   maskAPIKey(settings["rerank_api_key"]),
	}

	writeJSON(w, http.StatusOK, resp)
}

// UpdateRerankSettings godoc
//
//	@Summary	Update rerank settings
//	@Description	Updates the reranking provider. Masked API keys (****...) are preserved.
//	@Tags		settings
//	@Accept		json
//	@Produce	json
//	@Param		request	body	rerankSettingsRequest	true	"Rerank settings"
//	@Success	200	{object}	rerankSettingsResponse
//	@Failure	400	{object}	APIResponse
//	@Router		/settings/rerank [put]
func (h *handler) UpdateRerankSettings(w http.ResponseWriter, r *http.Request) {
	var req rerankSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.APIKey != "" && isMasked(req.APIKey) {
		existing, err := h.store.GetSetting(r.Context(), "rerank_api_key")
		if err != nil {
			h.log.Error("get rerank api key failed", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "failed to get settings")
			return
		}
		req.APIKey = existing
	}

	if err := h.rm.UpdateFromSettings(r.Context(), req.Provider, req.Model, req.APIKey); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := rerankSettingsResponse{
		Provider: req.Provider,
		Model:    req.Model,
		APIKey:   maskAPIKey(req.APIKey),
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
