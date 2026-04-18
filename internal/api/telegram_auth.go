package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	nauth "github.com/muty/nexus/internal/auth"
	tgconn "github.com/muty/nexus/internal/connector/telegram"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// pendingAuth tracks in-flight Telegram auth flows.
type pendingAuth struct {
	mu    sync.Mutex
	flows map[pendingAuthKey]*authFlow
}

// pendingAuthKey scopes a flow to a specific (connector, user) pair so concurrent
// flows from different users on the same connector don't interfere.
type pendingAuthKey struct {
	connectorID uuid.UUID
	userID      uuid.UUID
}

type authFlow struct {
	client   *telegram.Client
	codeCh   chan string
	passCh   chan string
	resultCh chan authResult
	cancel   context.CancelFunc
}

// authResult carries the outcome of a background Telegram auth flow.
// When err is nil, the self-user fields identify who the Nexus user
// authenticated as so the caller can persist them onto the connector
// config (see ExternalID/ExternalName on model.ConnectorConfig).
type authResult struct {
	err      error
	selfID   int64
	selfName string
}

// resolveSelfIdentity asks Telegram who the just-authenticated user is
// so the connector config can persist a display name and external ID.
// Never called before the auth flow has succeeded.
func resolveSelfIdentity(ctx context.Context, client *telegram.Client) (int64, string) {
	self, err := client.Self(ctx)
	if err != nil || self == nil {
		return 0, ""
	}
	name := tgconn.DisplayName(self)
	return self.ID, name
}

// persistSelfIdentity writes external_id + external_name onto the
// connector config and routes the write through the ConnectorManager
// so in-memory state updates too. Extracted so it can be unit-tested
// without driving the full auth goroutine.
func persistSelfIdentity(ctx context.Context, cm *ConnectorManager, cfg *model.ConnectorConfig, selfID int64, selfName string) error {
	if selfID == 0 {
		return nil
	}
	cfg.ExternalID = strconv.FormatInt(selfID, 10)
	if selfName != "" {
		cfg.ExternalName = selfName
	}
	return cm.Update(ctx, cfg)
}

var pending = &pendingAuth{flows: make(map[pendingAuthKey]*authFlow)}

type telegramAuthCodeRequest struct {
	Code     string `json:"code"`
	Password string `json:"password,omitempty"`
}

// TelegramAuthStart godoc
//
//	@Summary	Start Telegram authentication
//	@Description	Initiates the Telegram MTProto auth flow. Sends a code to the user's Telegram app.
//	@Tags		connectors
//	@Produce	json
//	@Param		id	path	string	true	"Connector UUID"
//	@Success	200	{object}	map[string]string
//	@Failure	400	{object}	APIResponse
//	@Failure	404	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/connectors/{id}/auth/start [post]
func (h *handler) TelegramAuthStart(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusInternalServerError, "failed to get connector")
		return
	}

	claims := nauth.UserFromContext(r.Context())
	if !canModifyConnector(claims, cfg) {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}

	if cfg.Type != "telegram" {
		writeError(w, http.StatusBadRequest, "connector is not a telegram connector")
		return
	}

	var apiID int
	switch v := cfg.Config["api_id"].(type) {
	case float64:
		apiID = int(v)
	case string:
		apiID, _ = strconv.Atoi(v)
	}
	apiHash, _ := cfg.Config["api_hash"].(string)
	phone, _ := cfg.Config["phone"].(string)

	if apiID == 0 || apiHash == "" || phone == "" {
		writeError(w, http.StatusBadRequest, "connector missing api_id, api_hash, or phone")
		return
	}

	sessionKey := fmt.Sprintf("telegram_session_%s", id.String())
	sessionStore := tgconn.NewDBSessionStorage(sessionKey, h.store.GetSetting, h.store.SetSetting)

	codeCh := make(chan string, 1)
	passCh := make(chan string, 1)
	resultCh := make(chan authResult, 1)

	ctx, cancel := context.WithCancel(context.Background())

	client := telegram.NewClient(int(apiID), apiHash, telegram.Options{
		SessionStorage: sessionStore,
	})

	flow := &authFlow{
		client:   client,
		codeCh:   codeCh,
		passCh:   passCh,
		resultCh: resultCh,
		cancel:   cancel,
	}

	flowKey := pendingAuthKey{connectorID: id, userID: claims.UserID}
	pending.mu.Lock()
	// Cancel any existing flow for the same (connector, user) pair
	if old, ok := pending.flows[flowKey]; ok {
		old.cancel()
	}
	pending.flows[flowKey] = flow
	pending.mu.Unlock()

	// Run auth in background
	go func() {
		defer cancel()
		var result authResult
		result.err = client.Run(ctx, func(ctx context.Context) error {
			codeAuth := auth.NewFlow(
				&interactiveAuth{
					phone:  phone,
					codeCh: codeCh,
					passCh: passCh,
				},
				auth.SendCodeOptions{},
			)
			if err := codeAuth.Run(ctx, client.Auth()); err != nil {
				return err
			}
			// Resolve the authenticated user's identity before the
			// client.Run callback returns — client.Self is only
			// callable while the MTProto client is alive.
			result.selfID, result.selfName = resolveSelfIdentity(ctx, client)
			return nil
		})
		resultCh <- result
	}()

	h.log.Info("telegram auth started", zap.String("connector", id.String()))
	writeJSON(w, http.StatusOK, map[string]string{"status": "code_sent", "message": "Check your Telegram app for the login code"})
}

// TelegramAuthCode godoc
//
//	@Summary	Submit Telegram auth code
//	@Description	Completes Telegram authentication by submitting the code received in the Telegram app.
//	@Tags		connectors
//	@Accept		json
//	@Produce	json
//	@Param		id		path	string	true	"Connector UUID"
//	@Param		request	body	object	true	"Auth code"
//	@Success	200	{object}	map[string]string
//	@Failure	400	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/connectors/{id}/auth/code [post]
func (h *handler) TelegramAuthCode(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid connector id")
		return
	}

	// Verify the caller can modify this connector before progressing the flow.
	// Otherwise an unauthorized user could submit codes against another user's
	// pending auth flow.
	cfg, err := h.store.GetConnectorConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get connector")
		return
	}
	claims := nauth.UserFromContext(r.Context())
	if !canModifyConnector(claims, cfg) {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}

	var req telegramAuthCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	flowKey := pendingAuthKey{connectorID: id, userID: claims.UserID}
	pending.mu.Lock()
	flow, ok := pending.flows[flowKey]
	pending.mu.Unlock()

	if !ok {
		writeError(w, http.StatusBadRequest, "no pending auth flow, call start first")
		return
	}

	// Send the code
	flow.codeCh <- req.Code

	// If 2FA password is provided, send it too
	if req.Password != "" {
		flow.passCh <- req.Password
	}

	// Wait for result (with timeout from request context)
	select {
	case res := <-flow.resultCh:
		pending.mu.Lock()
		delete(pending.flows, flowKey)
		pending.mu.Unlock()

		if res.err != nil {
			h.log.Error("telegram auth failed", zap.Error(res.err))
			writeError(w, http.StatusBadRequest, "auth failed: "+res.err.Error())
			return
		}

		// Persist the self-identity onto the connector config so the
		// /api/me/identities endpoint can surface it to the frontend.
		// Failure is non-fatal — auth still succeeded, identity can be
		// backfilled by a subsequent sync. Log and move on.
		if err := persistSelfIdentity(r.Context(), h.cm, cfg, res.selfID, res.selfName); err != nil {
			h.log.Warn("persist telegram self-identity",
				zap.String("connector", id.String()),
				zap.Error(err))
		}

		h.log.Info("telegram auth successful", zap.String("connector", id.String()))
		writeJSON(w, http.StatusOK, map[string]string{"status": "authenticated"})

	case <-r.Context().Done():
		writeError(w, http.StatusRequestTimeout, "auth timed out, try again")
	}
}

// interactiveAuth implements auth.UserAuthenticator for the code flow.
type interactiveAuth struct {
	phone  string
	codeCh chan string
	passCh chan string
}

func (a *interactiveAuth) Phone(_ context.Context) (string, error) {
	return a.phone, nil
}

func (a *interactiveAuth) Code(_ context.Context, _ *tg.AuthSentCode) (string, error) {
	code := <-a.codeCh
	return code, nil
}

func (a *interactiveAuth) Password(_ context.Context) (string, error) {
	select {
	case pass := <-a.passCh:
		return pass, nil
	default:
		return "", fmt.Errorf("2FA password required but not provided")
	}
}

func (a *interactiveAuth) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign up not supported")
}

func (a *interactiveAuth) AcceptTermsOfService(_ context.Context, _ tg.HelpTermsOfService) error {
	return nil
}
