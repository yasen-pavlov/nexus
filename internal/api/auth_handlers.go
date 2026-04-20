package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

const (
	errInvalidRequestBody = "invalid request body"
	errRegistrationFailed = "registration failed"
	errChangePasswordFail = "failed to change password"
)

type registerRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string        `json:"token"`
	User  *userResponse `json:"user"`
}

type userResponse struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type changePasswordRequest struct {
	Password string `json:"password"`
}

// Register godoc
//
//	@Summary	Register first admin user
//	@Description	Only available when no users exist. Creates the first user as admin.
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		request	body	registerRequest	true	"Credentials"
//	@Success	201	{object}	authResponse
//	@Failure	400	{object}	APIResponse
//	@Failure	403	{object}	APIResponse	"Registration disabled"
//	@Router		/auth/register [post]
func (h *handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	if req.Username == "" || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "username required and password must be at least 8 characters")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errRegistrationFailed)
		return
	}

	// Atomic first-admin insert. The store does INSERT ... WHERE NOT EXISTS
	// in one statement so two concurrent registrations can't both win and
	// both become admin (the historical CountUsers + CreateUser race).
	user, err := h.store.CreateFirstAdmin(r.Context(), req.Username, hash)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrFirstAdminExists):
			writeError(w, http.StatusForbidden, "registration is disabled, contact an admin")
			return
		case errors.Is(err, store.ErrDuplicateUsername):
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
		h.log.Error("create first admin failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, errRegistrationFailed)
		return
	}

	token, err := auth.GenerateToken(h.jwtSecret, user.ID, user.Username, user.Role, user.TokenVersion)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errRegistrationFailed)
		return
	}

	writeJSON(w, http.StatusCreated, authResponse{
		Token: token,
		User:  &userResponse{ID: user.ID, Username: user.Username, Role: user.Role, CreatedAt: user.CreatedAt},
	})
}

// Login godoc
//
//	@Summary	Login
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		request	body	loginRequest	true	"Credentials"
//	@Success	200	{object}	authResponse
//	@Failure	400	{object}	APIResponse	"invalid credentials"
//	@Router		/auth/login [post]
func (h *handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	// Per-(username, IP) rate limit. Tripping returns 429 with Retry-After
	// so clients (and brute-force bots) get a clear backoff signal. The
	// limiter call short-circuits before the bcrypt comparison so a tripped
	// bucket doesn't burn ~200ms of CPU per attempt.
	clientIP := r.RemoteAddr
	if h.loginLimiter != nil {
		if ok, retryAfter := h.loginLimiter.Allow(req.Username, clientIP); !ok {
			retrySec := max(int(retryAfter.Seconds()), 1)
			w.Header().Set("Retry-After", strconv.Itoa(retrySec))
			writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
			return
		}
	}

	// Bad credentials return 400, reserving 401 exclusively for expired /
	// invalid session tokens on protected endpoints. This lets the frontend
	// treat every 401 as "log out and redirect" without carve-outs.
	//
	// Constant-time path: even when the username is missing, run bcrypt
	// against a precomputed dummy hash so an attacker can't distinguish
	// "no such user" (would otherwise return in ~1ms) from "wrong password"
	// (~200ms via real bcrypt) and enumerate usernames by latency.
	user, passwordHash, err := h.store.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		_ = auth.CheckPasswordConstantTime("", req.Password)
		if h.loginLimiter != nil {
			h.loginLimiter.RecordFailure(req.Username, clientIP)
		}
		writeError(w, http.StatusBadRequest, "invalid username or password")
		return
	}

	if !auth.CheckPasswordConstantTime(passwordHash, req.Password) {
		if h.loginLimiter != nil {
			h.loginLimiter.RecordFailure(req.Username, clientIP)
		}
		writeError(w, http.StatusBadRequest, "invalid username or password")
		return
	}

	token, err := auth.GenerateToken(h.jwtSecret, user.ID, user.Username, user.Role, user.TokenVersion)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}

	if h.loginLimiter != nil {
		h.loginLimiter.RecordSuccess(req.Username, clientIP)
	}

	writeJSON(w, http.StatusOK, authResponse{
		Token: token,
		User:  &userResponse{ID: user.ID, Username: user.Username, Role: user.Role, CreatedAt: user.CreatedAt},
	})
}

// Me godoc
//
//	@Summary	Get current user
//	@Tags		auth
//	@Produce	json
//	@Success	200	{object}	userResponse
//	@Failure	401	{object}	APIResponse	"Not authenticated"
//	@Failure	500	{object}	APIResponse	"Lookup failed"
//	@Security	BearerAuth
//	@Router		/auth/me [get]
func (h *handler) Me(w http.ResponseWriter, r *http.Request) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	user, err := h.store.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		// Token references a user that's been deleted — treat as expired.
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "user no longer exists")
			return
		}
		h.log.Error("me: lookup user", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, userResponse{
		ID:        user.ID,
		Username:  user.Username,
		Role:      user.Role,
		CreatedAt: user.CreatedAt,
	})
}

// CreateUser godoc
//
//	@Summary	Create a new user (admin only)
//	@Tags		users
//	@Accept		json
//	@Produce	json
//	@Param		request	body	createUserRequest	true	"User details"
//	@Success	201	{object}	userResponse
//	@Failure	400	{object}	APIResponse
//	@Failure	409	{object}	APIResponse	"Username taken"
//	@Security	BearerAuth
//	@Router		/users [post]
func (h *handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	if req.Username == "" || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "username required and password must be at least 8 characters")
		return
	}

	role := req.Role
	if role == "" {
		role = "user"
	}
	if role != "admin" && role != "user" {
		writeError(w, http.StatusBadRequest, "role must be 'admin' or 'user'")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	user, err := h.store.CreateUser(r.Context(), req.Username, hash, role)
	if err != nil {
		if errors.Is(err, store.ErrDuplicateUsername) {
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
		h.log.Error("create user failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	writeJSON(w, http.StatusCreated, userResponse{ID: user.ID, Username: user.Username, Role: user.Role, CreatedAt: user.CreatedAt})
}

// ListUsers godoc
//
//	@Summary	List all users (admin only)
//	@Tags		users
//	@Produce	json
//	@Success	200	{array}	userResponse
//	@Security	BearerAuth
//	@Router		/users [get]
func (h *handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		h.log.Error("list users failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	result := make([]userResponse, len(users))
	for i, u := range users {
		result[i] = userResponse{ID: u.ID, Username: u.Username, Role: u.Role, CreatedAt: u.CreatedAt}
	}
	writeJSON(w, http.StatusOK, result)
}

// DeleteUser godoc
//
//	@Summary	Delete a user (admin only)
//	@Tags		users
//	@Param		id	path	string	true	"User UUID"
//	@Success	204
//	@Failure	400	{object}	APIResponse
//	@Failure	404	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/users/{id} [delete]
func (h *handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	// Prevent self-deletion
	claims := auth.UserFromContext(r.Context())
	if claims != nil && claims.UserID == id {
		writeError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}

	if err := h.store.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		h.log.Error("delete user failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ChangePassword godoc
//
//	@Summary	Change user password (admin or self)
//	@Tags		users
//	@Accept		json
//	@Param		id		path	string					true	"User UUID"
//	@Param		request	body	changePasswordRequest	true	"New password"
//	@Success	204
//	@Failure	400	{object}	APIResponse
//	@Failure	403	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/users/{id}/password [put]
func (h *handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	// Only admin or the user themselves can change password
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if claims.Role != "admin" && claims.UserID != id {
		writeError(w, http.StatusForbidden, "can only change your own password")
		return
	}

	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errChangePasswordFail)
		return
	}

	if err := h.store.UpdateUserPassword(r.Context(), id, hash); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		h.log.Error("change password failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, errChangePasswordFail)
		return
	}

	// UpdateUserPassword bumps token_version atomically with the password
	// write, invalidating every JWT minted before this request. Drop the
	// cached version so the change takes effect immediately rather than
	// after the cache TTL.
	if h.revocation != nil {
		h.revocation.Invalidate(id)
	}

	// Self-rotation: mint a fresh token so the caller stays signed in. If
	// we returned 204 like before, the FE's next request would 401 and
	// bounce to /login — surprising for a "rotate freely" UX. For admin-
	// changes-someone-else, we deliberately return 204 with no new token,
	// since the goal IS to revoke that user's existing sessions.
	if claims.UserID == id {
		h.issueRotatedToken(w, r, id)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// issueRotatedToken mints a fresh JWT for a user who just rotated their own
// password, so the caller stays signed in without bouncing to /login.
func (h *handler) issueRotatedToken(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	user, err := h.store.GetUserByID(r.Context(), id)
	if err != nil {
		h.log.Error("change password: re-fetch user", zap.Error(err))
		writeError(w, http.StatusInternalServerError, errChangePasswordFail)
		return
	}
	token, err := auth.GenerateToken(h.jwtSecret, user.ID, user.Username, user.Role, user.TokenVersion)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errChangePasswordFail)
		return
	}
	writeJSON(w, http.StatusOK, authResponse{
		Token: token,
		User: &userResponse{
			ID:        user.ID,
			Username:  user.Username,
			Role:      user.Role,
			CreatedAt: user.CreatedAt,
		},
	})
}
