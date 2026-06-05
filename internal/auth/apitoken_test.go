package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateAPIToken(t *testing.T) {
	plaintext, hash, err := GenerateAPIToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(plaintext, APITokenPrefix) {
		t.Errorf("plaintext missing prefix: %q", plaintext)
	}
	if !LooksLikeAPIToken(plaintext) {
		t.Errorf("LooksLikeAPIToken false for a minted token")
	}
	// SHA-256 hex is 64 chars.
	if len(hash) != 64 {
		t.Errorf("expected 64-char hex hash, got %d (%q)", len(hash), hash)
	}
	if hash != HashAPIToken(plaintext) {
		t.Errorf("returned hash doesn't match HashAPIToken(plaintext)")
	}
	if strings.Contains(hash, plaintext) {
		t.Errorf("hash must not embed the plaintext")
	}
}

func TestGenerateAPIToken_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		plaintext, hash, err := GenerateAPIToken()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if seen[plaintext] {
			t.Fatalf("duplicate plaintext minted: %q", plaintext)
		}
		if seen[hash] {
			t.Fatalf("duplicate hash minted: %q", hash)
		}
		seen[plaintext] = true
		seen[hash] = true
	}
}

func TestHashAPIToken_Deterministic(t *testing.T) {
	a, b := "nexus_pat_abc", "nexus_pat_abd"
	first := HashAPIToken(a)
	if first != HashAPIToken(a) {
		t.Error("HashAPIToken not deterministic")
	}
	if first == HashAPIToken(b) {
		t.Error("distinct inputs produced the same hash")
	}
}

func TestRequireInteractiveSession(t *testing.T) {
	nextHit := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextHit = true
		w.WriteHeader(http.StatusOK)
	})
	guard := RequireInteractiveSession(next)

	// API-token request → 403, next not reached.
	nextHit = false
	apiReq := httptest.NewRequest(http.MethodGet, "/x", nil)
	apiReq = apiReq.WithContext(context.WithValue(apiReq.Context(), apiTokenKey, true))
	apiW := httptest.NewRecorder()
	guard.ServeHTTP(apiW, apiReq)
	if apiW.Code != http.StatusForbidden {
		t.Errorf("api-token: expected 403, got %d", apiW.Code)
	}
	if nextHit {
		t.Error("api-token: next handler should not run")
	}

	// Interactive (JWT) request → passes through.
	nextHit = false
	jwtReq := httptest.NewRequest(http.MethodGet, "/x", nil)
	jwtW := httptest.NewRecorder()
	guard.ServeHTTP(jwtW, jwtReq)
	if jwtW.Code != http.StatusOK {
		t.Errorf("jwt: expected 200, got %d", jwtW.Code)
	}
	if !nextHit {
		t.Error("jwt: next handler should run")
	}
}

func TestLooksLikeAPIToken(t *testing.T) {
	cases := map[string]bool{
		"nexus_pat_xyz":    true,
		"nexus_pat_":       true,
		"eyJhbGciOiJIUzI1": false,
		"":                 false,
		"nexus_":           false,
	}
	for in, want := range cases {
		if got := LooksLikeAPIToken(in); got != want {
			t.Errorf("LooksLikeAPIToken(%q) = %v, want %v", in, got, want)
		}
	}
}
