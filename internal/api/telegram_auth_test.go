package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

func TestTelegramCreds(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *model.ConnectorConfig
		wantErr   bool
		wantID    int
		wantHash  string
		wantPhone string
	}{
		{
			name: "not a telegram connector",
			cfg:  &model.ConnectorConfig{Type: "filesystem"},
			// telegramCreds refuses anything but a telegram connector — this is
			// the guard the auth-start handler turns into a 400.
			wantErr: true,
		},
		{
			name:    "missing api_id",
			cfg:     &model.ConnectorConfig{Type: "telegram", Config: map[string]any{"api_hash": "h", "phone": "+1"}},
			wantErr: true,
		},
		{
			name:    "missing api_hash",
			cfg:     &model.ConnectorConfig{Type: "telegram", Config: map[string]any{"api_id": float64(42), "phone": "+1"}},
			wantErr: true,
		},
		{
			name:    "missing phone",
			cfg:     &model.ConnectorConfig{Type: "telegram", Config: map[string]any{"api_id": float64(42), "api_hash": "h"}},
			wantErr: true,
		},
		{
			name:      "valid with float64 api_id",
			cfg:       &model.ConnectorConfig{Type: "telegram", Config: map[string]any{"api_id": float64(42), "api_hash": "h", "phone": "+1"}},
			wantID:    42,
			wantHash:  "h",
			wantPhone: "+1",
		},
		{
			name:      "valid with string api_id",
			cfg:       &model.ConnectorConfig{Type: "telegram", Config: map[string]any{"api_id": "77", "api_hash": "h2", "phone": "+2"}},
			wantID:    77,
			wantHash:  "h2",
			wantPhone: "+2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, hash, phone, err := telegramCreds(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got id=%d hash=%q phone=%q", id, hash, phone)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tt.wantID || hash != tt.wantHash || phone != tt.wantPhone {
				t.Errorf("got (%d,%q,%q), want (%d,%q,%q)", id, hash, phone, tt.wantID, tt.wantHash, tt.wantPhone)
			}
		})
	}
}

func TestSendOrRequestTimeout_Success(t *testing.T) {
	ch := make(chan string, 1)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	if !sendOrRequestTimeout(w, req, ch, "hello") {
		t.Fatal("expected true when the buffered send succeeds")
	}
	if got := <-ch; got != "hello" {
		t.Errorf("channel got %q, want hello", got)
	}
	if w.Code != http.StatusOK {
		t.Errorf("no error should be written on success, got status %d", w.Code)
	}
}

func TestSendOrRequestTimeout_Timeout(t *testing.T) {
	ch := make(chan string) // unbuffered, no receiver → the send can only block
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)

	if sendOrRequestTimeout(w, req, ch, "x") {
		t.Fatal("expected false when the request context is already canceled")
	}
	if w.Code != http.StatusRequestTimeout {
		t.Errorf("status = %d, want 408", w.Code)
	}
}

func TestFinishTelegramAuth_Error(t *testing.T) {
	h := &handler{log: zap.NewNop()}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	h.finishTelegramAuth(w, req, &model.ConnectorConfig{}, uuid.Nil, authResult{err: errors.New("bad code")})

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestInteractiveAuth_Code_ContextCanceled(t *testing.T) {
	// codeCh stays empty so Code() would block forever on a bare receive; a
	// canceled ctx must unblock it (otherwise an abandoned auth flow wedges the
	// goroutine + MTProto client for the process lifetime).
	a := &interactiveAuth{codeCh: make(chan string)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := a.Code(ctx, &tg.AuthSentCode{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Code() returned nil error on canceled ctx, want ctx.Err()")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Code() did not observe context cancellation (still blocked)")
	}
}

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
		// Short non-empty secrets must NOT be echoed verbatim — they get a
		// fixed mask so a tiny key is never partially revealed.
		{"abc", "********"},
		{"key4", "********"},
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

// Verify interactiveAuth implements auth.UserAuthenticator
var _ auth.UserAuthenticator = (*interactiveAuth)(nil)
