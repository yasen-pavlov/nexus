package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/syncruns"
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
//	@Security	BearerAuth
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
//	@Security	BearerAuth
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
	for connID, entry := range h.cm.All() {
		connName := entry.Conn.Name()
		ownerID := ""
		if entry.Config.UserID != nil {
			ownerID = entry.Config.UserID.String()
		}
		job, runCtx, err := h.syncJobs.Start(connID, connName, entry.Conn.Type())
		if err != nil {
			if errors.Is(err, ErrAlreadyRunning) {
				continue
			}
			h.log.Error("auto-reindex: start failed", zap.String("connector", connName), zap.Error(err))
			continue
		}
		go func(cid uuid.UUID, ctx context.Context, n string, c connector.Connector, oid string, shared bool, jobID string) {
			progress := func(total, processed, errors int) {
				h.syncJobs.Update(jobID, total, processed, errors)
			}
			report, err := h.pipeline.RunWithProgress(ctx, cid, c, oid, shared, progress)
			if report != nil {
				h.syncJobs.SetDeleted(jobID, report.DocsDeleted)
			}
			h.syncJobs.Complete(jobID, err)
			if err != nil && !errors.Is(err, context.Canceled) {
				h.log.Error("auto-reindex: sync failed", zap.String("connector", n), zap.Error(err))
			}
		}(connID, runCtx, connName, entry.Conn, ownerID, entry.Config.Shared, job.ID)
	}

	h.log.Info("auto-reindex: triggered sync for all connectors")
}

type rerankSettingsResponse struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	// MinScore is the post-rerank floor — docs below this are dropped.
	// Lives on the Rerank endpoint (rather than on Ranking) because it's
	// only meaningful when a reranker is configured.
	MinScore float64 `json:"min_score"`
}

type rerankSettingsRequest struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	APIKey   string  `json:"api_key"`
	MinScore float64 `json:"min_score"`
}

// GetRerankSettings godoc
//
//	@Summary	Get rerank settings
//	@Description	Returns current reranking provider configuration + the min-score floor. API keys are masked.
//	@Tags		settings
//	@Produce	json
//	@Success	200	{object}	rerankSettingsResponse
//	@Security	BearerAuth
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
		MinScore: h.rankingConfig().RerankerMinScore,
	}

	writeJSON(w, http.StatusOK, resp)
}

// UpdateRerankSettings godoc
//
//	@Summary	Update rerank settings
//	@Description	Updates the reranking provider + min-score floor. Masked API keys (****...) are preserved. min_score must be in [0,1].
//	@Tags		settings
//	@Accept		json
//	@Produce	json
//	@Param		request	body	rerankSettingsRequest	true	"Rerank settings"
//	@Success	200	{object}	rerankSettingsResponse
//	@Failure	400	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/settings/rerank [put]
func (h *handler) UpdateRerankSettings(w http.ResponseWriter, r *http.Request) {
	var req rerankSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MinScore < 0 || req.MinScore > 1 {
		writeError(w, http.StatusBadRequest, "min_score must be in [0,1]")
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

	// Persist + hot-swap the rerank floor into the active ranking config so
	// the next query sees it immediately. A nil ranking manager means the
	// handler was constructed without one (unlikely in production; guards
	// stray test wirings).
	if h.ranking != nil {
		if err := h.ranking.SetRerankerMinScore(r.Context(), req.MinScore); err != nil {
			h.log.Error("update rerank min score failed", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "failed to save min_score")
			return
		}
	}

	resp := rerankSettingsResponse{
		Provider: req.Provider,
		Model:    req.Model,
		APIKey:   maskAPIKey(req.APIKey),
		MinScore: req.MinScore,
	}

	writeJSON(w, http.StatusOK, resp)
}

// retentionSettingsResponse mirrors the shape the admin UI consumes. Keys are
// plain integers; 0 on either retention value disables that rule — the sweeper
// already honors this convention.
type retentionSettingsResponse struct {
	RetentionDays         int `json:"retention_days"`
	RetentionPerConnector int `json:"retention_per_connector"`
	SweepIntervalMinutes  int `json:"sweep_interval_minutes"`
	MinSweepIntervalMins  int `json:"min_sweep_interval_minutes"`
}

type retentionSettingsRequest struct {
	RetentionDays         int `json:"retention_days"`
	RetentionPerConnector int `json:"retention_per_connector"`
	SweepIntervalMinutes  int `json:"sweep_interval_minutes"`
}

// GetRetentionSettings godoc
//
//	@Summary		Get sync-history retention settings
//	@Description	Returns the currently-persisted retention policy. Admin only. Reports the hard floor on sweep interval so the UI can surface it as a disabled bound instead of letting the user submit an invalid value.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	retentionSettingsResponse
//	@Security		BearerAuth
//	@Router			/settings/retention [get]
func (h *handler) GetRetentionSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.store.GetSettings(r.Context(), []string{
		syncruns.SettingRetentionDays,
		syncruns.SettingRetentionPerConn,
		syncruns.SettingSweepIntervalMins,
	})
	if err != nil {
		h.log.Error("get retention settings failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	resp := retentionSettingsResponse{
		RetentionDays:         parseIntOr(settings[syncruns.SettingRetentionDays], syncruns.DefaultRetentionDays),
		RetentionPerConnector: parseIntOr(settings[syncruns.SettingRetentionPerConn], syncruns.DefaultRetentionPerConn),
		SweepIntervalMinutes:  parseIntOr(settings[syncruns.SettingSweepIntervalMins], syncruns.DefaultSweepIntervalMins),
		MinSweepIntervalMins:  syncruns.MinSweepIntervalMins,
	}
	writeJSON(w, http.StatusOK, resp)
}

// UpdateRetentionSettings godoc
//
//	@Summary		Update sync-history retention settings
//	@Description	Validates and persists the three retention keys the sweeper reads every tick. Retention values must be non-negative integers (0 disables the rule). The sweep interval is clamped to the hard floor reported by GET.
//	@Tags			settings
//	@Accept			json
//	@Produce		json
//	@Param			request	body	retentionSettingsRequest	true	"Retention settings"
//	@Success		200	{object}	retentionSettingsResponse
//	@Failure		400	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/settings/retention [put]
func (h *handler) UpdateRetentionSettings(w http.ResponseWriter, r *http.Request) {
	var req retentionSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RetentionDays < 0 {
		writeError(w, http.StatusBadRequest, "retention_days must be >= 0")
		return
	}
	if req.RetentionPerConnector < 0 {
		writeError(w, http.StatusBadRequest, "retention_per_connector must be >= 0")
		return
	}
	if req.SweepIntervalMinutes < syncruns.MinSweepIntervalMins {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("sweep_interval_minutes must be >= %d", syncruns.MinSweepIntervalMins))
		return
	}

	if err := h.store.SetSettings(r.Context(), map[string]string{
		syncruns.SettingRetentionDays:     strconv.Itoa(req.RetentionDays),
		syncruns.SettingRetentionPerConn:  strconv.Itoa(req.RetentionPerConnector),
		syncruns.SettingSweepIntervalMins: strconv.Itoa(req.SweepIntervalMinutes),
	}); err != nil {
		h.log.Error("update retention settings failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	writeJSON(w, http.StatusOK, retentionSettingsResponse{
		RetentionDays:         req.RetentionDays,
		RetentionPerConnector: req.RetentionPerConnector,
		SweepIntervalMinutes:  req.SweepIntervalMinutes,
		MinSweepIntervalMins:  syncruns.MinSweepIntervalMins,
	})
}

// RunRetentionSweep godoc
//
//	@Summary		Run retention cleanup immediately
//	@Description	Triggers a one-shot sweep of the sync_runs history using the currently-persisted retention rules. Admin only.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	map[string]bool
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/settings/retention/sweep [post]
func (h *handler) RunRetentionSweep(w http.ResponseWriter, r *http.Request) {
	if h.sweeper == nil {
		writeError(w, http.StatusServiceUnavailable, "retention sweeper not configured")
		return
	}
	if err := h.sweeper.SweepOnce(r.Context()); err != nil {
		h.log.Error("retention sweep failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "sweep failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Ranking settings — backed by api.RankingManager which loads from the
// settings table at boot and hot-swaps the in-memory RankingConfig on each
// PUT so the next query sees the new values. The `reranker_min_score`
// knob lives on the Rerank settings endpoint instead (it's rerank-adjacent
// behavior) and is not exposed on this endpoint's shape.

// rankingKnownSourceTypes limits the keys accepted in the per-source maps
// so typos don't silently survive persistence and then go unread at search
// time because no doc with that source_type exists.
var rankingKnownSourceTypes = map[string]bool{
	"imap": true, "telegram": true, "paperless": true, "filesystem": true,
}

type rankingSettings struct {
	SourceHalfLifeDays map[string]float64 `json:"source_half_life_days"`
	SourceRecencyFloor map[string]float64 `json:"source_recency_floor"`
	SourceTrustWeight  map[string]float64 `json:"source_trust_weight"`
	MetadataBonus      bool               `json:"metadata_bonus_enabled"`
	SourceTrustEnabled bool               `json:"source_trust_enabled"`
	KnownSourceTypes   []string           `json:"known_source_types,omitempty"`
}

func rankingSettingsFrom(cfg search.RankingConfig) rankingSettings {
	return rankingSettings{
		SourceHalfLifeDays: cfg.SourceHalfLifeDays,
		SourceRecencyFloor: cfg.SourceRecencyFloor,
		SourceTrustWeight:  cfg.SourceTrustWeight,
		MetadataBonus:      cfg.MetadataBonusEnabled,
		SourceTrustEnabled: cfg.SourceTrustEnabled,
		KnownSourceTypes:   []string{"imap", "telegram", "paperless", "filesystem"},
	}
}

// GetRankingSettings godoc
//
//	@Summary		Get search ranking tunables
//	@Description	Returns the active ranking config — per-source half-life + floor + trust weight, plus the global bonus/trust toggles. Admin only.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	rankingSettings
//	@Security		BearerAuth
//	@Router			/settings/ranking [get]
func (h *handler) GetRankingSettings(w http.ResponseWriter, r *http.Request) {
	_ = r
	writeJSON(w, http.StatusOK, rankingSettingsFrom(h.ranking.Get()))
}

// UpdateRankingSettings godoc
//
//	@Summary		Update search ranking tunables
//	@Description	Persists ranking knobs and hot-swaps them into the in-memory config so the next query sees the new values. Unknown source_type keys in any of the per-source maps are rejected with 400. Admin only.
//	@Tags			settings
//	@Accept			json
//	@Produce		json
//	@Param			request	body	rankingSettings	true	"Ranking settings"
//	@Success		200	{object}	rankingSettings
//	@Failure		400	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/settings/ranking [put]
func (h *handler) UpdateRankingSettings(w http.ResponseWriter, r *http.Request) {
	var req rankingSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Reject unknown source_type keys so a typo can't silently persist.
	for k := range req.SourceHalfLifeDays {
		if !rankingKnownSourceTypes[k] {
			writeError(w, http.StatusBadRequest, "unknown source_type in source_half_life_days: "+k)
			return
		}
	}
	for k := range req.SourceRecencyFloor {
		if !rankingKnownSourceTypes[k] {
			writeError(w, http.StatusBadRequest, "unknown source_type in source_recency_floor: "+k)
			return
		}
	}
	for k := range req.SourceTrustWeight {
		if !rankingKnownSourceTypes[k] {
			writeError(w, http.StatusBadRequest, "unknown source_type in source_trust_weight: "+k)
			return
		}
	}
	for k, v := range req.SourceHalfLifeDays {
		if v <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("source_half_life_days[%s] must be > 0", k))
			return
		}
	}
	for k, v := range req.SourceRecencyFloor {
		if v < 0 || v > 1 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("source_recency_floor[%s] must be in [0,1]", k))
			return
		}
	}
	for k, v := range req.SourceTrustWeight {
		if v < 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("source_trust_weight[%s] must be >= 0", k))
			return
		}
	}

	// Merge the submitted partial map onto the current config so partial
	// PUTs preserve the keys the client didn't send. Rerank min score is
	// owned by the Rerank settings endpoint now and deliberately preserved.
	current := h.ranking.Get()
	next := search.DefaultRankingConfig()
	for k, v := range current.SourceHalfLifeDays {
		next.SourceHalfLifeDays[k] = v
	}
	for k, v := range req.SourceHalfLifeDays {
		next.SourceHalfLifeDays[k] = v
	}
	for k, v := range current.SourceRecencyFloor {
		next.SourceRecencyFloor[k] = v
	}
	for k, v := range req.SourceRecencyFloor {
		next.SourceRecencyFloor[k] = v
	}
	for k, v := range current.SourceTrustWeight {
		next.SourceTrustWeight[k] = v
	}
	for k, v := range req.SourceTrustWeight {
		next.SourceTrustWeight[k] = v
	}
	next.MetadataBonusEnabled = req.MetadataBonus
	next.SourceTrustEnabled = req.SourceTrustEnabled
	next.RerankerMinScore = current.RerankerMinScore

	if err := h.ranking.Replace(r.Context(), next); err != nil {
		h.log.Error("update ranking settings failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	writeJSON(w, http.StatusOK, rankingSettingsFrom(h.ranking.Get()))
}

func parseIntOr(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
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
