package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

type createConnectorRequest struct {
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Config  map[string]any `json:"config"`
	Enabled bool           `json:"enabled"`
}

type updateConnectorRequest struct {
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Config  map[string]any `json:"config"`
	Enabled bool           `json:"enabled"`
}

type connectorResponse struct {
	model.ConnectorConfig
	Status string `json:"status"`
}

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
		result[i] = connectorResponse{ConnectorConfig: cfg, Status: status}
	}

	writeJSON(w, http.StatusOK, result)
}

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

	writeJSON(w, http.StatusOK, connectorResponse{ConnectorConfig: *cfg, Status: status})
}

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

	cfg := &model.ConnectorConfig{
		Type:    req.Type,
		Name:    req.Name,
		Config:  req.Config,
		Enabled: req.Enabled,
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

	cfg := &model.ConnectorConfig{
		ID:      id,
		Type:    req.Type,
		Name:    req.Name,
		Config:  req.Config,
		Enabled: req.Enabled,
	}
	if cfg.Config == nil {
		cfg.Config = map[string]any{}
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

	writeJSON(w, http.StatusOK, cfg)
}

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
