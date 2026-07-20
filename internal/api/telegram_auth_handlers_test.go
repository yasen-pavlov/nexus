//go:build integration

package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/store"
)

// seedCompletedTelegramFlow registers a telegram connector (owned by the test
// admin that authWrap injects) and installs a pending auth flow keyed to that
// owner, pre-loaded with the given result. It returns the connector ID string
// and a cleanup func. This lets tests drive TelegramAuthCode's completion path
// — code send, result wait, flow-map cleanup, finishTelegramAuth — without a
// live Telegram server, which is exactly the region the coverage report flagged
// as untested.
func seedCompletedTelegramFlow(t *testing.T, st *store.Store, router http.Handler, name string, flow *authFlow) (string, pendingAuthKey, func()) {
	t.Helper()
	connIDStr := telegramAuthSetup(t, router, name)
	connID := uuid.MustParse(connIDStr)

	cfg, err := st.GetConnectorConfig(context.Background(), connID)
	if err != nil || cfg.UserID == nil {
		t.Fatalf("get connector owner: %v", err)
	}
	key := pendingAuthKey{connectorID: connID, userID: *cfg.UserID}

	pending.mu.Lock()
	pending.flows[key] = flow
	pending.mu.Unlock()

	cleanup := func() {
		pending.mu.Lock()
		delete(pending.flows, key)
		pending.mu.Unlock()
	}
	return connIDStr, key, cleanup
}

func newCodeFlow() *authFlow {
	return &authFlow{
		codeCh:   make(chan string, 1),
		passCh:   make(chan string, 1),
		resultCh: make(chan authResult, 1),
		cancel:   func() {},
	}
}

// TestTelegramAuthCode_Success drives the whole completion path for a
// successful login: the code is delivered to the flow, the ready result is
// read, the flow is evicted from the pending map, and finishTelegramAuth
// reports 200. A zero selfID keeps persistSelfIdentity a no-op.
func TestTelegramAuthCode_Success(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	flow := newCodeFlow()
	flow.resultCh <- authResult{}
	connID, key, cleanup := seedCompletedTelegramFlow(t, st, router, "auth-code-success", flow)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+connID+"/auth/code",
		bytes.NewBufferString(`{"code":"12345"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := <-flow.codeCh; got != "12345" {
		t.Errorf("code delivered = %q, want 12345", got)
	}
	pending.mu.Lock()
	_, stillThere := pending.flows[key]
	pending.mu.Unlock()
	if stillThere {
		t.Error("completed flow should be evicted from the pending map")
	}
}

// TestTelegramAuthCode_SuccessWith2FA additionally exercises the password-send
// branch, which only runs when the request carries a 2FA password.
func TestTelegramAuthCode_SuccessWith2FA(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	flow := newCodeFlow()
	flow.resultCh <- authResult{}
	connID, _, cleanup := seedCompletedTelegramFlow(t, st, router, "auth-code-2fa", flow)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+connID+"/auth/code",
		bytes.NewBufferString(`{"code":"12345","password":"hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := <-flow.passCh; got != "hunter2" {
		t.Errorf("password delivered = %q, want hunter2", got)
	}
}

// TestTelegramAuthCode_PersistsIdentity checks the success path writes the
// resolved self-identity onto the connector config through the handler (not
// just the persistSelfIdentity unit).
func TestTelegramAuthCode_PersistsIdentity(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	flow := newCodeFlow()
	flow.resultCh <- authResult{selfID: 9001, selfName: "Agent"}
	connIDStr, _, cleanup := seedCompletedTelegramFlow(t, st, router, "auth-code-identity", flow)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+connIDStr+"/auth/code",
		bytes.NewBufferString(`{"code":"12345"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got, err := st.GetConnectorConfig(context.Background(), uuid.MustParse(connIDStr))
	if err != nil {
		t.Fatalf("read back connector: %v", err)
	}
	if got.ExternalID != "9001" || got.ExternalName != "Agent" {
		t.Errorf("identity not persisted: external_id=%q external_name=%q", got.ExternalID, got.ExternalName)
	}
}

// TestTelegramAuthCode_AuthFailed seeds a flow whose result carries an error,
// exercising finishTelegramAuth's failure branch (400 auth failed).
func TestTelegramAuthCode_AuthFailed(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	flow := newCodeFlow()
	flow.resultCh <- authResult{err: errors.New("PHONE_CODE_INVALID")}
	connID, _, cleanup := seedCompletedTelegramFlow(t, st, router, "auth-code-failed", flow)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+connID+"/auth/code",
		bytes.NewBufferString(`{"code":"00000"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestTelegramAuthCode_ResultTimeout drives the final result-wait timeout: the
// code send succeeds (buffered codeCh, live ctx) but the flow never produces a
// result, so a client disconnect while waiting on resultCh returns 408. This is
// a distinct branch from the code-send timeout covered elsewhere.
func TestTelegramAuthCode_ResultTimeout(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	flow := newCodeFlow() // codeCh buffered + empty, resultCh never filled
	connID, _, cleanup := seedCompletedTelegramFlow(t, st, router, "auth-code-resulttimeout", flow)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+connID+"/auth/code",
		bytes.NewBufferString(`{"code":"12345"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(w, req)
		close(done)
	}()
	// Let the handler deliver the code and reach the result wait, then simulate
	// a client disconnect.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return after context cancel (result wait blocked forever)")
	}
	if w.Code != http.StatusRequestTimeout {
		t.Errorf("expected 408, got %d: %s", w.Code, w.Body.String())
	}
	// On the intended path the code lands in the buffered channel before the
	// result wait times out. Read it non-blockingly: if cancel raced ahead of
	// the send (an off-nominal path the 408 check above already accounts for)
	// the channel is simply empty — never block the whole test binary on it.
	select {
	case got := <-flow.codeCh:
		if got != "12345" {
			t.Errorf("code delivered = %q, want 12345", got)
		}
	default:
	}
}
