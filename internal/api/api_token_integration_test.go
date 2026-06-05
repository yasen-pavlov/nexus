//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// newTokenTestRouter builds a router with no auth wrapper so each request
// controls its own Authorization header — required to exercise the
// JWT-vs-API-token paths independently. The router's apiTokenAuthenticator is
// wired to the real store, so token validation hits the DB.
func newTokenTestRouter(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewLLMManager(st, zap.NewNop()), NewRAGManager(st, zap.NewNop()), nil, sjm, nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())
	return st, router
}

// decodeData unwraps the APIResponse envelope (writeJSON wraps every payload
// in {"data": ...}) into target.
func decodeData(t *testing.T, w *httptest.ResponseRecorder, target any) {
	t.Helper()
	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("re-marshal data: %v", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode data into %T: %v", target, err)
	}
}

// mintToken creates a token via the API using an interactive (JWT) session and
// returns the plaintext secret plus the token's row id.
func mintToken(t *testing.T, router http.Handler, jwt, name string, expiresAt *time.Time) (string, uuid.UUID) {
	t.Helper()
	body := map[string]any{"name": name}
	if expiresAt != nil {
		body["expires_at"] = expiresAt.Format(time.RFC3339)
	}
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("mint token: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp createTokenResponse
	decodeData(t, w, &resp)
	if resp.Token == "" || resp.Meta == nil {
		t.Fatalf("mint token: empty token or meta in response: %+v", resp)
	}
	if !auth.LooksLikeAPIToken(resp.Token) {
		t.Fatalf("mint token: plaintext lacks nexus_pat_ prefix: %q", resp.Token)
	}
	return resp.Token, resp.Meta.ID
}

func statusFor(router http.Handler, method, path, bearer string) int {
	req := httptest.NewRequest(method, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Code
}

// TestAPIToken_MintAndUse covers the happy path: mint with a JWT, then use the
// opaque token to hit a protected endpoint as the owning user.
func TestAPIToken_MintAndUse(t *testing.T) {
	st, router := newTokenTestRouter(t)
	userID, jwt := createTestAdmin(t, st)

	token, _ := mintToken(t, router, jwt, "agent", nil)

	// The token authenticates /auth/me and resolves to the owning user.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("me with token: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var me userResponse
	decodeData(t, w, &me)
	if me.ID != userID {
		t.Errorf("token acted as wrong user: got %s, want %s", me.ID, userID)
	}
}

// TestAPIToken_UnknownRejected ensures a well-formed but unissued token is 401.
func TestAPIToken_UnknownRejected(t *testing.T) {
	_, router := newTokenTestRouter(t)
	bogus, _, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if code := statusFor(router, http.MethodGet, "/api/auth/me", bogus); code != http.StatusUnauthorized {
		t.Errorf("unknown token: expected 401, got %d", code)
	}
}

// TestAPIToken_Expired ensures a token past its expiry is rejected.
func TestAPIToken_Expired(t *testing.T) {
	st, router := newTokenTestRouter(t)
	userID, _ := createTestAdmin(t, st)

	plaintext, hash, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	past := time.Now().Add(-1 * time.Hour)
	if _, err := st.CreateAPIToken(context.Background(), userID, "stale", hash, &past); err != nil {
		t.Fatalf("create expired token: %v", err)
	}
	if code := statusFor(router, http.MethodGet, "/api/auth/me", plaintext); code != http.StatusUnauthorized {
		t.Errorf("expired token: expected 401, got %d", code)
	}
}

// TestAPIToken_RevokeStopsWorking ensures deleting a token invalidates it
// immediately (cache eviction), not just after the TTL.
func TestAPIToken_RevokeStopsWorking(t *testing.T) {
	st, router := newTokenTestRouter(t)
	_, jwt := createTestAdmin(t, st)

	token, tokenID := mintToken(t, router, jwt, "doomed", nil)

	// Works before revocation, priming the validator cache.
	if code := statusFor(router, http.MethodGet, "/api/auth/me", token); code != http.StatusOK {
		t.Fatalf("pre-revoke: expected 200, got %d", code)
	}

	// Revoke via the API (interactive session).
	req := httptest.NewRequest(http.MethodDelete, "/api/tokens/"+tokenID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke: expected 204, got %d; body: %s", w.Code, w.Body.String())
	}

	// Now the token must be rejected despite the cache having held it.
	if code := statusFor(router, http.MethodGet, "/api/auth/me", token); code != http.StatusUnauthorized {
		t.Errorf("post-revoke: expected 401, got %d", code)
	}
}

// TestAPIToken_CrossUserRevoke404 ensures one user can't revoke another's
// token, and the lookup returns 404 (not 403) so existence doesn't leak.
func TestAPIToken_CrossUserRevoke404(t *testing.T) {
	st, router := newTokenTestRouter(t)
	_, aliceJWT := createTestUser(t, st)
	_, bobJWT := createTestUser(t, st)

	aliceToken, aliceTokenID := mintToken(t, router, aliceJWT, "alice-agent", nil)

	// Bob tries to delete Alice's token → 404.
	req := httptest.NewRequest(http.MethodDelete, "/api/tokens/"+aliceTokenID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+bobJWT)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete: expected 404, got %d", w.Code)
	}

	// Alice's token still works.
	if code := statusFor(router, http.MethodGet, "/api/auth/me", aliceToken); code != http.StatusOK {
		t.Errorf("alice token after bob's failed delete: expected 200, got %d", code)
	}
}

// TestAPIToken_AdminTokenReachesAdminRoute ensures a token acts with its
// owner's role — an admin's token reaches admin-only routes, a user's does not.
func TestAPIToken_AdminTokenReachesAdminRoute(t *testing.T) {
	st, router := newTokenTestRouter(t)
	_, adminJWT := createTestAdmin(t, st)
	_, userJWT := createTestUser(t, st)

	adminToken, _ := mintToken(t, router, adminJWT, "admin-agent", nil)
	userToken, _ := mintToken(t, router, userJWT, "user-agent", nil)

	// /api/users is admin-only.
	if code := statusFor(router, http.MethodGet, "/api/users", adminToken); code != http.StatusOK {
		t.Errorf("admin token on /users: expected 200, got %d", code)
	}
	if code := statusFor(router, http.MethodGet, "/api/users", userToken); code != http.StatusForbidden {
		t.Errorf("user token on /users: expected 403, got %d", code)
	}
}

// TestAPIToken_CannotManageTokens ensures an API token can't mint or revoke
// tokens — token management is restricted to interactive (JWT) sessions.
func TestAPIToken_CannotManageTokens(t *testing.T) {
	st, router := newTokenTestRouter(t)
	_, jwt := createTestAdmin(t, st)

	token, tokenID := mintToken(t, router, jwt, "agent", nil)

	// List with the API token → 403.
	if code := statusFor(router, http.MethodGet, "/api/tokens", token); code != http.StatusForbidden {
		t.Errorf("list tokens with api token: expected 403, got %d", code)
	}
	// Delete with the API token → 403.
	if code := statusFor(router, http.MethodDelete, "/api/tokens/"+tokenID.String(), token); code != http.StatusForbidden {
		t.Errorf("delete token with api token: expected 403, got %d", code)
	}
	// Mint with the API token → 403.
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewBufferString(`{"name":"nested"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("mint token with api token: expected 403, got %d", w.Code)
	}
}

// TestAPIToken_ListScopedToOwner ensures list returns only the caller's tokens,
// newest first, and never the secret.
func TestAPIToken_ListScopedToOwner(t *testing.T) {
	st, router := newTokenTestRouter(t)
	_, jwt := createTestAdmin(t, st)
	_, otherJWT := createTestUser(t, st)

	mintToken(t, router, jwt, "one", nil)
	mintToken(t, router, jwt, "two", nil)
	mintToken(t, router, otherJWT, "other", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	// Raw body must not contain a secret prefix — only metadata is serialized.
	if bytes.Contains(w.Body.Bytes(), []byte(auth.APITokenPrefix)) {
		t.Errorf("list response leaked a token secret: %s", w.Body.String())
	}

	var tokens []map[string]any
	decodeData(t, w, &tokens)
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens for owner, got %d", len(tokens))
	}
	for _, tok := range tokens {
		if _, hasSecret := tok["token"]; hasSecret {
			t.Errorf("list item should not carry a token secret: %v", tok)
		}
	}
}

// TestAPIToken_DeleteInvalidID ensures a malformed token id is a 400.
func TestAPIToken_DeleteInvalidID(t *testing.T) {
	st, router := newTokenTestRouter(t)
	_, jwt := createTestAdmin(t, st)

	req := httptest.NewRequest(http.MethodDelete, "/api/tokens/not-a-uuid", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid token id: expected 400, got %d", w.Code)
	}
}

// TestAPIToken_DeleteUnknown404 ensures deleting a non-existent (but
// well-formed) token id returns 404.
func TestAPIToken_DeleteUnknown404(t *testing.T) {
	st, router := newTokenTestRouter(t)
	_, jwt := createTestAdmin(t, st)

	req := httptest.NewRequest(http.MethodDelete, "/api/tokens/"+uuid.New().String(), nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown token id: expected 404, got %d", w.Code)
	}
}

// TestAPIToken_StoreErrors drives the 500 paths by closing the store out from
// under the handlers (JWT parse needs no DB, so requests still reach them).
func TestAPIToken_StoreErrors(t *testing.T) {
	st, router := newTokenTestRouter(t)
	_, jwt := createTestAdmin(t, st)
	st.Close()

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", http.MethodPost, "/api/tokens", `{"name":"x"}`},
		{"list", http.MethodGet, "/api/tokens", ""},
		{"delete", http.MethodDelete, "/api/tokens/" + uuid.New().String(), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}
			req.Header.Set("Authorization", "Bearer "+jwt)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusInternalServerError {
				t.Errorf("%s with closed store: expected 500, got %d", tc.name, w.Code)
			}
		})
	}
}

// TestAPIToken_CreateValidation covers the create-request guard rails.
func TestAPIToken_CreateValidation(t *testing.T) {
	st, router := newTokenTestRouter(t)
	_, jwt := createTestAdmin(t, st)

	cases := []struct {
		name string
		body string
		code int
	}{
		{"empty name", `{"name":""}`, http.StatusBadRequest},
		{"whitespace name", `{"name":"   "}`, http.StatusBadRequest},
		{"name too long", fmt.Sprintf(`{"name":%q}`, strings.Repeat("a", 101)), http.StatusBadRequest},
		{"past expiry", fmt.Sprintf(`{"name":"x","expires_at":%q}`, time.Now().Add(-time.Hour).Format(time.RFC3339)), http.StatusBadRequest},
		{"bad body", `not json`, http.StatusBadRequest},
		{"valid", `{"name":"ok"}`, http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+jwt)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.code {
				t.Errorf("expected %d, got %d; body: %s", tc.code, w.Code, w.Body.String())
			}
		})
	}
}
