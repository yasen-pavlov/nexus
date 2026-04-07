package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

func TestInteractiveAuth_Phone(t *testing.T) {
	a := &interactiveAuth{phone: "+1234567890"}
	phone, err := a.Phone(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if phone != "+1234567890" {
		t.Errorf("expected +1234567890, got %q", phone)
	}
}

func TestInteractiveAuth_Code(t *testing.T) {
	codeCh := make(chan string, 1)
	a := &interactiveAuth{codeCh: codeCh}

	codeCh <- "12345"
	code, err := a.Code(context.Background(), &tg.AuthSentCode{})
	if err != nil {
		t.Fatal(err)
	}
	if code != "12345" {
		t.Errorf("expected 12345, got %q", code)
	}
}

func TestInteractiveAuth_Password(t *testing.T) {
	passCh := make(chan string, 1)
	a := &interactiveAuth{passCh: passCh}

	passCh <- "secret"
	pass, err := a.Password(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pass != "secret" {
		t.Errorf("expected 'secret', got %q", pass)
	}
}

func TestInteractiveAuth_Password_NotProvided(t *testing.T) {
	passCh := make(chan string) // unbuffered, empty
	a := &interactiveAuth{passCh: passCh}

	_, err := a.Password(context.Background())
	if err == nil {
		t.Fatal("expected error when password not provided")
	}
}

func TestInteractiveAuth_SignUp(t *testing.T) {
	a := &interactiveAuth{}
	_, err := a.SignUp(context.Background())
	if err == nil {
		t.Fatal("expected error — sign up not supported")
	}
}

func TestInteractiveAuth_AcceptTermsOfService(t *testing.T) {
	a := &interactiveAuth{}
	err := a.AcceptTermsOfService(context.Background(), tg.HelpTermsOfService{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"", ""},
		{"abc", "abc"},
		{"sk-1234567890", "****7890"},
	}
	for _, tt := range tests {
		if got := maskAPIKey(tt.input); got != tt.want {
			t.Errorf("maskAPIKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsMasked(t *testing.T) {
	if !isMasked("****1234") {
		t.Error("expected masked")
	}
	if isMasked("sk-1234") {
		t.Error("expected not masked")
	}
}

func TestTelegramAuthCode_WithPendingFlow(t *testing.T) {
	// Inject a mock pending auth flow
	codeCh := make(chan string, 1)
	passCh := make(chan string, 1)
	resultCh := make(chan error, 1)

	testID := "test-connector-id"
	pending.mu.Lock()
	pending.flows[testID] = &authFlow{
		codeCh:   codeCh,
		passCh:   passCh,
		resultCh: resultCh,
	}
	pending.mu.Unlock()

	// Simulate successful auth in background
	go func() {
		code := <-codeCh
		if code != "12345" {
			resultCh <- fmt.Errorf("wrong code")
			return
		}
		resultCh <- nil // success
	}()

	h := &handler{log: zap.NewNop()}

	body := `{"code":"12345"}`
	r := chi.NewRouter()
	r.Post("/api/connectors/{id}/auth/code", h.TelegramAuthCode)

	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+testID+"/auth/code", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestTelegramAuthCode_WithPassword(t *testing.T) {
	codeCh := make(chan string, 1)
	passCh := make(chan string, 1)
	resultCh := make(chan error, 1)

	testID := "test-2fa-id"
	pending.mu.Lock()
	pending.flows[testID] = &authFlow{
		codeCh:   codeCh,
		passCh:   passCh,
		resultCh: resultCh,
	}
	pending.mu.Unlock()

	go func() {
		<-codeCh
		<-passCh
		resultCh <- nil
	}()

	h := &handler{log: zap.NewNop()}

	body := `{"code":"12345","password":"secret"}`
	r := chi.NewRouter()
	r.Post("/api/connectors/{id}/auth/code", h.TelegramAuthCode)

	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+testID+"/auth/code", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestTelegramAuthCode_AuthFails(t *testing.T) {
	codeCh := make(chan string, 1)
	passCh := make(chan string, 1)
	resultCh := make(chan error, 1)

	testID := "test-fail-id"
	pending.mu.Lock()
	pending.flows[testID] = &authFlow{
		codeCh:   codeCh,
		passCh:   passCh,
		resultCh: resultCh,
	}
	pending.mu.Unlock()

	go func() {
		<-codeCh
		resultCh <- fmt.Errorf("invalid code")
	}()

	h := &handler{log: zap.NewNop()}

	body := `{"code":"wrong"}`
	r := chi.NewRouter()
	r.Post("/api/connectors/{id}/auth/code", h.TelegramAuthCode)

	req := httptest.NewRequest(http.MethodPost, "/api/connectors/"+testID+"/auth/code", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// Verify interactiveAuth implements auth.UserAuthenticator
var _ auth.UserAuthenticator = (*interactiveAuth)(nil)
