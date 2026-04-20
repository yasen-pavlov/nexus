package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/crypto"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/store"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

const (
	errConnectorNotFound  = "connector not found"
	errInvalidConnectorID = "invalid connector id"
	errFailedGetConnector = "failed to get connector"
)

type createConnectorRequest struct {
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	Config   map[string]any `json:"config"`
	Enabled  bool           `json:"enabled"`
	Schedule string         `json:"schedule"`
	Shared   bool           `json:"shared"`
}

type updateConnectorRequest struct {
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	Config   map[string]any `json:"config"`
	Enabled  bool           `json:"enabled"`
	Schedule string         `json:"schedule"`
	Shared   bool           `json:"shared"`
}

type connectorResponse struct {
	model.ConnectorConfig
	Status string `json:"status"`
}

// canReadConnector returns true if the user is allowed to read this connector.
// Admins can read everything. Users can read their own connectors plus shared.
func canReadConnector(claims *auth.Claims, cfg *model.ConnectorConfig) bool {
	if claims == nil {
		return false
	}
	if claims.Role == "admin" {
		return true
	}
	if cfg.Shared {
		return true
	}
	return cfg.UserID != nil && *cfg.UserID == claims.UserID
}

// canModifyConnector returns true if the user is allowed to mutate this
// connector. Admins can modify everything. Regular users can mutate their
// own private connectors only — shared connectors are admin-only even
// for the owner, so an owner can't unilaterally flip `shared=false`,
// rotate credentials, or delete a connector that other users are
// reading from. Owners who need to revoke sharing must ask an admin.
func canModifyConnector(claims *auth.Claims, cfg *model.ConnectorConfig) bool {
	if claims == nil {
		return false
	}
	if claims.Role == "admin" {
		return true
	}
	if cfg.Shared {
		return false
	}
	return cfg.UserID != nil && *cfg.UserID == claims.UserID
}

// writeMutationDenied returns the right status when canModifyConnector
// said no. If the caller can't even READ the resource, return 404 to
// avoid leaking existence; if they CAN read it (e.g. it's shared, they're
// the owner of a shared connector now restricted to admins, or they're
// any user looking at a shared connector), return 403 with a descriptive
// reason — they already know it's there, hiding the policy serves no one.
func writeMutationDenied(w http.ResponseWriter, claims *auth.Claims, cfg *model.ConnectorConfig) {
	if canReadConnector(claims, cfg) {
		writeError(w, http.StatusForbidden, "shared connectors can only be modified by an admin")
		return
	}
	writeError(w, http.StatusNotFound, errConnectorNotFound)
}

// canReadDocument returns true if the user is allowed to read a document with
// the given ownership metadata. Mirrors canReadConnector but operates on the
// raw owner_id/shared fields stored on a chunk (since the download endpoint
// doesn't have a ConnectorConfig in hand).
func canReadDocument(claims *auth.Claims, ownerID string, shared bool) bool {
	if claims == nil {
		return false
	}
	if claims.Role == "admin" {
		return true
	}
	if shared {
		return true
	}
	return ownerID != "" && ownerID == claims.UserID.String()
}

// ListConnectors godoc
//
//	@Summary	List all connectors
//	@Tags		connectors
//	@Produce	json
//	@Success	200	{array}	connectorResponse
//	@Security	BearerAuth
//	@Router		/connectors [get]
func (h *handler) ListConnectors(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	configs, err := h.store.ListUserConnectorConfigs(r.Context(), userID)
	if err != nil {
		h.log.Error("list connectors failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to list connectors")
		return
	}

	active := h.cm.All()
	result := make([]connectorResponse, len(configs))
	for i, cfg := range configs {
		status := "inactive"
		if _, ok := active[cfg.ID]; ok {
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
//	@Security	BearerAuth
//	@Router		/connectors/{id} [get]
func (h *handler) GetConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidConnectorID)
		return
	}

	cfg, err := h.store.GetConnectorConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, errConnectorNotFound)
			return
		}
		h.log.Error("get connector failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, errFailedGetConnector)
		return
	}

	if !canReadConnector(auth.UserFromContext(r.Context()), cfg) {
		writeError(w, http.StatusNotFound, errConnectorNotFound)
		return
	}

	status := "inactive"
	if _, _, ok := h.cm.GetByID(id); ok {
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
//	@Security	BearerAuth
//	@Router		/connectors [post]
func (h *handler) CreateConnector(w http.ResponseWriter, r *http.Request) {
	var req createConnectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
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

	userID := auth.UserIDFromContext(r.Context())
	cfg := &model.ConnectorConfig{
		Type:     req.Type,
		Name:     req.Name,
		Config:   req.Config,
		Enabled:  req.Enabled,
		Schedule: req.Schedule,
		Shared:   req.Shared,
		UserID:   &userID,
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
//	@Security	BearerAuth
//	@Router		/connectors/{id} [put]
func (h *handler) UpdateConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidConnectorID)
		return
	}

	var req updateConnectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
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

	// Restore masked secrets from existing config so they aren't overwritten
	existing, err := h.store.GetConnectorConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, errConnectorNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, errFailedGetConnector)
		return
	}

	if !canModifyConnector(auth.UserFromContext(r.Context()), existing) {
		writeMutationDenied(w, auth.UserFromContext(r.Context()), existing)
		return
	}

	cfg := &model.ConnectorConfig{
		ID:       id,
		Type:     req.Type,
		Name:     req.Name,
		Config:   crypto.RestoreMaskedFields(req.Type, req.Config, existing.Config),
		Enabled:  req.Enabled,
		Schedule: req.Schedule,
		Shared:   req.Shared,
		UserID:   existing.UserID,
	}
	if cfg.Config == nil {
		cfg.Config = map[string]any{}
	}

	if err := h.cm.Update(r.Context(), cfg); err != nil {
		writeConnectorUpdateError(w, err)
		return
	}

	// If ownership flipped, propagate the change to all chunks already indexed
	// in OpenSearch — otherwise old chunks keep their stale shared/owner_id and
	// the visibility change has no effect until the next full re-sync.
	if existing.Shared != cfg.Shared {
		h.propagateOwnershipChange(r.Context(), cfg)
	}

	cfg.Config = crypto.MaskConfig(cfg.Type, cfg.Config)
	writeJSON(w, http.StatusOK, cfg)
}

// writeConnectorUpdateError translates ConnectorManager.Update errors into the
// right HTTP status / message pair.
func writeConnectorUpdateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, errConnectorNotFound)
	case errors.Is(err, store.ErrDuplicateName):
		writeError(w, http.StatusConflict, "connector name already exists")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// propagateOwnershipChange mirrors a (shared / user_id) flip into OpenSearch
// so chunks already indexed pick up the new visibility without a re-sync.
// Best-effort — a failure is logged and the update still returns 200.
func (h *handler) propagateOwnershipChange(ctx context.Context, cfg *model.ConnectorConfig) {
	ownerID := ""
	if cfg.UserID != nil {
		ownerID = cfg.UserID.String()
	}
	if err := h.search.UpdateOwnershipBySource(ctx, cfg.Type, cfg.Name, ownerID, cfg.Shared); err != nil {
		h.log.Warn("failed to propagate ownership change to search index",
			zap.String("connector", cfg.Name),
			zap.Error(err),
		)
	}
}

// DeleteConnector godoc
//
//	@Summary	Delete a connector
//	@Tags		connectors
//	@Param		id	path	string	true	"Connector UUID"
//	@Success	204
//	@Failure	404	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/connectors/{id} [delete]
func (h *handler) DeleteConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidConnectorID)
		return
	}

	existing, err := h.store.GetConnectorConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, errConnectorNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, errFailedGetConnector)
		return
	}

	if !canModifyConnector(auth.UserFromContext(r.Context()), existing) {
		writeMutationDenied(w, auth.UserFromContext(r.Context()), existing)
		return
	}

	if err := h.cm.Remove(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, errConnectorNotFound)
			return
		}
		h.log.Error("delete connector failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete connector")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetConnectorAvatar godoc
//
//	@Summary		Fetch a cached profile avatar from a connector
//	@Description	Streams the bytes of a profile photo the connector cached to the binary store (e.g. a Telegram sender's avatar, keyed by their external user ID). Auth-scoped to the connector's visibility; returns 404 (not 403) for connectors the caller can't read, to avoid leaking existence. Emits a private, caches-fine response so the browser reuses the blob across conversation views.
//	@Tags			connectors
//	@Produce		image/*
//	@Param			id			path	string	true	"Connector UUID"
//	@Param			external_id	path	string	true	"External (source-assigned) identifier whose avatar to fetch"
//	@Success		200
//	@Failure		400	{object}	APIResponse
//	@Failure		404	{object}	APIResponse
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/connectors/{id}/avatars/{external_id} [get]
func (h *handler) GetConnectorAvatar(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidConnectorID)
		return
	}
	externalID := chi.URLParam(r, "external_id")
	if externalID == "" {
		writeError(w, http.StatusBadRequest, "external_id is required")
		return
	}

	cfg, err := h.store.GetConnectorConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, errConnectorNotFound)
			return
		}
		h.log.Error("get connector for avatar", zap.Error(err))
		writeError(w, http.StatusInternalServerError, errFailedGetConnector)
		return
	}

	if !canReadConnector(auth.UserFromContext(r.Context()), cfg) {
		writeError(w, http.StatusNotFound, errConnectorNotFound)
		return
	}

	key, ok := avatarCacheKey(cfg.Type, externalID)
	if !ok {
		writeError(w, http.StatusNotFound, "avatars not supported for this connector type")
		return
	}

	if h.binaryStore == nil {
		writeError(w, http.StatusNotFound, "avatar not cached")
		return
	}

	rc, err := h.binaryStore.Get(r.Context(), cfg.Type, cfg.Name, key)
	if err != nil {
		writeError(w, http.StatusNotFound, "avatar not cached")
		return
	}
	defer func() { _ = rc.Close() }()

	// Telegram avatars come back as JPEGs from the MTProto peer-photo
	// endpoint. Hard-coding the content type avoids buffering the first
	// N bytes just to run http.DetectContentType — cheap and correct for
	// the only source that uses this endpoint today.
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if _, err := io.Copy(w, rc); err != nil {
		h.log.Warn("stream avatar failed", zap.Error(err))
	}
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
