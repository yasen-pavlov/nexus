package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/muty/nexus/internal/auth"
	tgconn "github.com/muty/nexus/internal/connector/telegram"
	"go.uber.org/zap"
)

// identityItem is one row in the /api/me/identities response — a
// per-connector projection of "who the Nexus user IS on this external
// system". Consumed by the frontend to flag self messages in the
// conversation view and, later, to tag email addresses as "mine" in
// the mailbox view.
type identityItem struct {
	ConnectorID  string `json:"connector_id"`
	SourceType   string `json:"source_type"`
	SourceName   string `json:"source_name"`
	ExternalID   string `json:"external_id"`
	ExternalName string `json:"external_name"`
	HasAvatar    bool   `json:"has_avatar"`
}

// identitiesResponse is the top-level shape of /api/me/identities.
// Wrapping the array keeps the endpoint forward-compatible with
// future aggregate fields (e.g. aliases, default_identity).
type identitiesResponse struct {
	Identities []identityItem `json:"identities"`
}

// GetMyIdentities godoc
//
//	@Summary		List self-identities across connected sources
//	@Description	Returns the requesting user's external identities on each of their *owned* connectors (shared connectors are skipped — they don't represent "me"). Only connectors that have completed auth and populated `external_id` emit an entry. Chat-like UI uses this to distinguish own messages from others.
//	@Tags			identity
//	@Produce		json
//	@Success		200	{object}	identitiesResponse
//	@Failure		401	{object}	APIResponse
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/me/identities [get]
func (h *handler) GetMyIdentities(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	configs, err := h.store.ListUserConnectorConfigs(r.Context(), claims.UserID)
	if err != nil {
		h.log.Error("list connector configs for identities", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to list identities")
		return
	}

	out := identitiesResponse{Identities: []identityItem{}}
	for _, cfg := range configs {
		// Shared connectors don't represent a "self" — skip even if the
		// shared connector happens to carry an external identity.
		if cfg.Shared || cfg.UserID == nil || *cfg.UserID != claims.UserID {
			continue
		}
		if cfg.ExternalID == "" {
			continue
		}

		out.Identities = append(out.Identities, identityItem{
			ConnectorID:  cfg.ID.String(),
			SourceType:   cfg.Type,
			SourceName:   cfg.Name,
			ExternalID:   cfg.ExternalID,
			ExternalName: cfg.ExternalName,
			HasAvatar:    h.connectorHasAvatar(r.Context(), cfg.Type, cfg.Name, cfg.ExternalID),
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// connectorHasAvatar reports whether a binary-cache entry exists for the
// connector's self-avatar. Returns false on any lookup error — the UI
// treats "unknown" as "missing" and falls back to initials. Only
// implemented for sources where the connector writes avatars; today
// that's Telegram.
func (h *handler) connectorHasAvatar(ctx context.Context, sourceType, sourceName, externalID string) bool {
	if h.binaryStore == nil {
		return false
	}
	key, ok := avatarCacheKey(sourceType, externalID)
	if !ok {
		return false
	}
	exists, err := h.binaryStore.Exists(ctx, sourceType, sourceName, key)
	if err != nil {
		return false
	}
	return exists
}

// avatarCacheKey translates a per-source external identifier into the
// binary-store source_id the connector writes under. Returns ok=false
// for sources that don't participate in the avatar cache.
func avatarCacheKey(sourceType, externalID string) (string, bool) {
	switch sourceType {
	case "telegram":
		id, err := strconv.ParseInt(externalID, 10, 64)
		if err != nil {
			return "", false
		}
		return tgconn.AvatarSourceID(id), true
	default:
		return "", false
	}
}
