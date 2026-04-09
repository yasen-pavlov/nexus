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
	tgconn "github.com/muty/nexus/internal/connector/telegram"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// pendingAuth tracks in-flight Telegram auth flows.
type pendingAuth struct {
	mu    sync.Mutex
	flows map[string]*authFlow
}

type authFlow struct {
	client   *telegram.Client
	codeCh   chan string
	passCh   chan string
	resultCh chan error
	cancel   context.CancelFunc
}

var pending = &pendingAuth{flows: make(map[string]*authFlow)}

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
	resultCh := make(chan error, 1)

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

	pending.mu.Lock()
	// Cancel any existing flow for this connector
	if old, ok := pending.flows[id.String()]; ok {
		old.cancel()
	}
	pending.flows[id.String()] = flow
	pending.mu.Unlock()

	// Run auth in background
	go func() {
		defer cancel()
		err := client.Run(ctx, func(ctx context.Context) error {
			codeAuth := auth.NewFlow(
				&interactiveAuth{
					phone:  phone,
					codeCh: codeCh,
					passCh: passCh,
				},
				auth.SendCodeOptions{},
			)
			return codeAuth.Run(ctx, client.Auth())
		})
		resultCh <- err
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
//	@Router		/connectors/{id}/auth/code [post]
func (h *handler) TelegramAuthCode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req telegramAuthCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	pending.mu.Lock()
	flow, ok := pending.flows[id]
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
	case err := <-flow.resultCh:
		pending.mu.Lock()
		delete(pending.flows, id)
		pending.mu.Unlock()

		if err != nil {
			h.log.Error("telegram auth failed", zap.Error(err))
			writeError(w, http.StatusBadRequest, "auth failed: "+err.Error())
			return
		}

		h.log.Info("telegram auth successful", zap.String("connector", id))
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
