package api

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

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
