package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/crypto"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/store"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

type createConnectorRequest struct {
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	Config   map[string]any `json:"config"`
	Enabled  bool           `json:"enabled"`
	Schedule string         `json:"schedule"`
}

type updateConnectorRequest struct {
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	Config   map[string]any `json:"config"`
	Enabled  bool           `json:"enabled"`
	Schedule string         `json:"schedule"`
}

type connectorResponse struct {
	model.ConnectorConfig
	Status string `json:"status"`
}

// ListConnectors godoc
//
//	@Summary	List all connectors
//	@Tags		connectors
//	@Produce	json
//	@Success	200	{array}	connectorResponse
//	@Router		/connectors [get]
func (h *handler) ListConnectors(w http.ResponseWriter, r *http.Request) {
	configs, err := h.store.ListConnectorConfigs(r.Context())
	if err != nil {
		h.log.Error("list connectors failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to list connectors")
		return
	}

	active := h.cm.All()
	result := make([]connectorResponse, len(configs))
	for i, cfg := range configs {
		status := "inactive"
		if _, ok := active[cfg.Name]; ok {
			status = "active"
		}
		cfg.Config = crypto.MaskConfig(cfg.Type, cfg.Config)
		result[i] = connectorResponse{ConnectorConfig: cfg, Status: status}
	}

	writeJSON(w, http.StatusOK, result)
}

// GetConnector godoc
//
//	@Summary	Get a connector by ID
//	@Tags		connectors
//	@Produce	json
//	@Param		id	path	string	true	"Connector UUID"
//	@Success	200	{object}	connectorResponse
//	@Failure	404	{object}	APIResponse
//	@Router		/connectors/{id} [get]
func (h *handler) GetConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connector id")
		return
	}

	cfg, err := h.store.GetConnectorConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		h.log.Error("get connector failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to get connector")
		return
	}

	status := "inactive"
	if _, ok := h.cm.Get(cfg.Name); ok {
		status = "active"
	}

	cfg.Config = crypto.MaskConfig(cfg.Type, cfg.Config)
	writeJSON(w, http.StatusOK, connectorResponse{ConnectorConfig: *cfg, Status: status})
}

// CreateConnector godoc
//
//	@Summary	Create a new connector
//	@Tags		connectors
//	@Accept		json
//	@Produce	json
//	@Param		request	body	createConnectorRequest	true	"Connector config"
//	@Success	201	{object}	model.ConnectorConfig
//	@Failure	400	{object}	APIResponse
//	@Failure	409	{object}	APIResponse	"Name already exists"
//	@Router		/connectors [post]
func (h *handler) CreateConnector(w http.ResponseWriter, r *http.Request) {
	var req createConnectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Type == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, "type and name are required")
		return
	}

	if err := validateSchedule(req.Schedule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	cfg := &model.ConnectorConfig{
		Type:     req.Type,
		Name:     req.Name,
		Config:   req.Config,
		Enabled:  req.Enabled,
		Schedule: req.Schedule,
	}
	if cfg.Config == nil {
		cfg.Config = map[string]any{}
	}

	if err := h.cm.Add(r.Context(), cfg); err != nil {
		if errors.Is(err, store.ErrDuplicateName) {
			writeError(w, http.StatusConflict, "connector name already exists")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, cfg)
}

// UpdateConnector godoc
//
//	@Summary	Update a connector
//	@Description	Updates connector config. Masked secret values (****...) are preserved from the existing config.
//	@Tags		connectors
//	@Accept		json
//	@Produce	json
//	@Param		id		path	string					true	"Connector UUID"
//	@Param		request	body	updateConnectorRequest	true	"Updated config"
//	@Success	200	{object}	model.ConnectorConfig
//	@Failure	400	{object}	APIResponse
//	@Failure	404	{object}	APIResponse
//	@Failure	409	{object}	APIResponse	"Name already exists"
//	@Router		/connectors/{id} [put]
func (h *handler) UpdateConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connector id")
		return
	}

	var req updateConnectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Type == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, "type and name are required")
		return
	}

	if err := validateSchedule(req.Schedule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	cfg := &model.ConnectorConfig{
		ID:       id,
		Type:     req.Type,
		Name:     req.Name,
		Config:   req.Config,
		Enabled:  req.Enabled,
		Schedule: req.Schedule,
	}
	if cfg.Config == nil {
		cfg.Config = map[string]any{}
	}

	// Restore masked secrets from existing config so they aren't overwritten
	existing, err := h.store.GetConnectorConfig(r.Context(), id)
	if err == nil {
		cfg.Config = crypto.RestoreMaskedFields(cfg.Type, cfg.Config, existing.Config)
	}

	if err := h.cm.Update(r.Context(), cfg); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		if errors.Is(err, store.ErrDuplicateName) {
			writeError(w, http.StatusConflict, "connector name already exists")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	cfg.Config = crypto.MaskConfig(cfg.Type, cfg.Config)
	writeJSON(w, http.StatusOK, cfg)
}

// DeleteConnector godoc
//
//	@Summary	Delete a connector
//	@Tags		connectors
//	@Param		id	path	string	true	"Connector UUID"
//	@Success	204
//	@Failure	404	{object}	APIResponse
//	@Router		/connectors/{id} [delete]
func (h *handler) DeleteConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connector id")
		return
	}

	if err := h.cm.Remove(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		h.log.Error("delete connector failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete connector")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func validateSchedule(schedule string) error {
	if schedule == "" {
		return nil
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	_, err := parser.Parse(schedule)
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}
	return nil
}
