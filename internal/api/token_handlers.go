package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// maxAPITokenNameLen bounds the user-supplied token label so the listing UI
// and logs stay tidy.
const maxAPITokenNameLen = 100

type createTokenRequest struct {
	Name string `json:"name"`
	// ExpiresAt is optional; nil/omitted means the token never expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// createTokenResponse is returned exactly once, on creation. Token holds the
// plaintext secret — it is never retrievable again.
type createTokenResponse struct {
	Token string          `json:"token"`
	Meta  *model.APIToken `json:"meta"`
}

// CreateToken godoc
//
//	@Summary	Create an API token for the current user
//	@Description	Mints a long-lived personal access token. The plaintext token is returned ONCE and never again. expires_at is optional (omit for a non-expiring token).
//	@Tags		tokens
//	@Accept		json
//	@Produce	json
//	@Param		request	body	createTokenRequest	true	"Token details"
//	@Success	201	{object}	createTokenResponse
//	@Failure	400	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/tokens [post]
func (h *handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, errNotAuthenticated)
		return
	}

	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "token name is required")
		return
	}
	if len(name) > maxAPITokenNameLen {
		writeError(w, http.StatusBadRequest, "token name too long")
		return
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		writeError(w, http.StatusBadRequest, "expires_at must be in the future")
		return
	}

	plaintext, hash, err := auth.GenerateAPIToken()
	if err != nil {
		h.log.Error("generate api token failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	tok, err := h.store.CreateAPIToken(r.Context(), claims.UserID, name, hash, req.ExpiresAt)
	if err != nil {
		h.log.Error("create api token failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	writeJSON(w, http.StatusCreated, createTokenResponse{Token: plaintext, Meta: tok})
}

// ListTokens godoc
//
//	@Summary	List the current user's API tokens
//	@Description	Returns token metadata only — never the secret.
//	@Tags		tokens
//	@Produce	json
//	@Success	200	{array}	model.APIToken
//	@Security	BearerAuth
//	@Router		/tokens [get]
func (h *handler) ListTokens(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, errNotAuthenticated)
		return
	}

	tokens, err := h.store.ListAPITokensByUser(r.Context(), claims.UserID)
	if err != nil {
		h.log.Error("list api tokens failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

// DeleteToken godoc
//
//	@Summary	Revoke an API token
//	@Description	Deletes one of the caller's tokens. Non-owners get 404 so token existence doesn't leak across users.
//	@Tags		tokens
//	@Param		id	path	string	true	"Token UUID"
//	@Success	204
//	@Failure	400	{object}	APIResponse
//	@Failure	404	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/tokens/{id} [delete]
func (h *handler) DeleteToken(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, errNotAuthenticated)
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid token id")
		return
	}

	if err := h.store.DeleteAPIToken(r.Context(), id, claims.UserID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "token not found")
			return
		}
		h.log.Error("delete api token failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete token")
		return
	}

	// Drop it from the validator cache so it stops working immediately rather
	// than lingering for the cache TTL.
	if h.apiTokenAuth != nil {
		h.apiTokenAuth.invalidateByTokenID(id)
	}

	w.WriteHeader(http.StatusNoContent)
}
