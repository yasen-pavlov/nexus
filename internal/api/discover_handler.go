package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/muty/nexus/internal/connector"
	"go.uber.org/zap"
)

type discoverRequest struct {
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

// DiscoverConnectorResources godoc
//
//	@Summary	Discover a connector's selectable sub-resources
//	@Description	Builds a connector from the posted type + config (credentials) and enumerates its selectable units (e.g. iCloud calendars) so the create/edit UI can render a picker — before the connector is saved. Returns 400 if the type doesn't support discovery, 502 if the upstream credentials fail.
//	@Tags		connectors
//	@Accept		json
//	@Produce	json
//	@Param		request	body	discoverRequest	true	"Connector type + config"
//	@Success	200	{array}	connector.DiscoveredResource
//	@Failure	400	{object}	APIResponse
//	@Failure	502	{object}	APIResponse	"Upstream discovery failed"
//	@Security	BearerAuth
//	@Router		/connectors/discover [post]
func (h *handler) DiscoverConnectorResources(w http.ResponseWriter, r *http.Request) {
	var req discoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required")
		return
	}

	conn, err := connector.Create(req.Type)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unknown connector type")
		return
	}
	cfgMap := connector.Config(req.Config)
	if cfgMap == nil {
		cfgMap = connector.Config{}
	}
	cfgMap["name"] = req.Type
	if err := conn.Configure(cfgMap); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	discoverer, ok := conn.(connector.ResourceDiscoverer)
	if !ok {
		writeError(w, http.StatusBadRequest, "this connector type does not support discovery")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resources, err := discoverer.DiscoverResources(ctx)
	if err != nil {
		h.log.Warn("connector discovery failed", zap.String("type", req.Type), zap.Error(err))
		// 502 (not 401) so the frontend surfaces the message instead of logging
		// the user out on an upstream-credential failure.
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if resources == nil {
		resources = []connector.DiscoveredResource{}
	}
	writeJSON(w, http.StatusOK, resources)
}
