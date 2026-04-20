//go:build integration

// Phase: post-Phase-5 auth hardening regression tests. Each test below
// pins one of the audit findings the user signed off on:
//
//   - constant-time login (timing parity for known/unknown user)
//   - login rate limiter (429 + Retry-After after the bucket trips)
//   - first-admin race (CreateFirstAdmin atomicity → ErrFirstAdminExists)
//   - JWT revocation via token_version (admin password reset kicks the
//     other user out without waiting for natural expiry)
//   - self-rotation re-issues a token (200 + new JWT, not 204)
//   - shared connector mutation locked to admin

package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// newHardeningRouter wires the post-Phase-5 router with a fresh
// revocation cache + login limiter so the assertions below operate
// on production-shaped middleware.
func newHardeningRouter(t *testing.T, limiter *auth.LoginRateLimiter) (http.Handler, *store.Store) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	revocation := auth.NewTokenRevocationCache(
		func(ctx context.Context, id uuid.UUID) (int, error) {
			u, err := st.GetUserByID(ctx, id)
			if err != nil {
				return 0, err
			}
			return u.TokenVersion, nil
		},
		time.Second, // short TTL so tests don't have to wait
	)
	router := NewRouter(
		st, sc, p, cm, em,
		NewRerankManager(st, zap.NewNop()),
		NewSyncJobManager(st, zap.NewNop()),
		nil, nil, nil,
		testJWTSecret,
		revocation,
		limiter,
		nil,
		zap.NewNop(),
	)
	return router, st
}

// --- constant-time login ----------------------------------------------------

// TestLogin_ConstantTime_RoughParity asserts the dummy bcrypt path keeps
// missing-user latency in the same order of magnitude as wrong-password
// latency. Wall-clock comparisons are noisy, so we only require that
// missing-user is at least 50ms (real bcrypt at cost=12 should land
// around ~150-300ms; the absent dummy check would return in <5ms).
func TestLogin_ConstantTime_RoughParity(t *testing.T) {
	router, _ := newHardeningRouter(t, nil)

	doJSON(t, router, http.MethodPost, "/api/auth/register",
		`{"username":"alice","password":"password123"}`, "")

	start := time.Now()
	w := doJSON(t, router, http.MethodPost, "/api/auth/login",
		`{"username":"ghost","password":"some-password"}`, "")
	missingDuration := time.Since(start)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing-user login: expected 400, got %d", w.Code)
	}
	if missingDuration < 50*time.Millisecond {
		t.Errorf("missing-user login returned in %s — bcrypt dummy didn't run", missingDuration)
	}
}

// --- login rate limiter -----------------------------------------------------

func TestLogin_RateLimit_TripsAfterMaxAttempts(t *testing.T) {
	limiter := auth.NewLoginRateLimiter(auth.LoginRateLimiterConfig{
		MaxAttempts: 3,
		Window:      time.Minute,
		Lockout:     time.Minute,
	})
	router, _ := newHardeningRouter(t, limiter)

	doJSON(t, router, http.MethodPost, "/api/auth/register",
		`{"username":"alice","password":"password123"}`, "")

	for i := range 3 {
		w := doJSON(t, router, http.MethodPost, "/api/auth/login",
			`{"username":"alice","password":"WRONG"}`, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: expected 400, got %d", i+1, w.Code)
		}
	}

	// 4th attempt: blocked with 429 + Retry-After header.
	w := doJSON(t, router, http.MethodPost, "/api/auth/login",
		`{"username":"alice","password":"WRONG"}`, "")
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("4th attempt: expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}
}

func TestLogin_RateLimit_SuccessClearsBucket(t *testing.T) {
	limiter := auth.NewLoginRateLimiter(auth.LoginRateLimiterConfig{
		MaxAttempts: 3,
		Window:      time.Minute,
		Lockout:     time.Minute,
	})
	router, _ := newHardeningRouter(t, limiter)

	doJSON(t, router, http.MethodPost, "/api/auth/register",
		`{"username":"alice","password":"password123"}`, "")

	// 2 wrong, 1 right → bucket cleared.
	doJSON(t, router, http.MethodPost, "/api/auth/login",
		`{"username":"alice","password":"WRONG"}`, "")
	doJSON(t, router, http.MethodPost, "/api/auth/login",
		`{"username":"alice","password":"WRONG"}`, "")
	w := doJSON(t, router, http.MethodPost, "/api/auth/login",
		`{"username":"alice","password":"password123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("login after partial failures should succeed, got %d", w.Code)
	}

	// Now we should be allowed at least 2 more failures before lockout.
	for i := range 2 {
		w := doJSON(t, router, http.MethodPost, "/api/auth/login",
			`{"username":"alice","password":"WRONG"}`, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("post-success attempt %d: expected 400, got %d", i+1, w.Code)
		}
	}
}

// --- first-admin race -------------------------------------------------------

// TestRegister_ConcurrentFirstAdmin_OnlyOneWins fires N concurrent
// register requests on a fresh DB; exactly one should get 201, the rest
// 403 ("registration disabled"). The store's INSERT ... WHERE NOT EXISTS
// closes the historical TOCTOU window between CountUsers and CreateUser.
func TestRegister_ConcurrentFirstAdmin_OnlyOneWins(t *testing.T) {
	router, _ := newHardeningRouter(t, nil)

	const concurrent = 8
	codes := make(chan int, concurrent)
	var wg sync.WaitGroup
	for i := range concurrent {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := `{"username":"bootstrap` + itoa(i) + `","password":"password123"}`
			w := doJSON(t, router, http.MethodPost, "/api/auth/register", body, "")
			codes <- w.Code
		}(i)
	}
	wg.Wait()
	close(codes)

	created, forbidden, other := 0, 0, 0
	for c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusForbidden:
			forbidden++
		default:
			other++
		}
	}
	if created != 1 {
		t.Errorf("expected exactly 1 successful register, got %d (forbidden=%d other=%d)",
			created, forbidden, other)
	}
	if forbidden+created != concurrent {
		t.Errorf("unexpected status codes: created=%d forbidden=%d other=%d",
			created, forbidden, other)
	}
}

func itoa(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b[i:])
}

// --- JWT revocation ---------------------------------------------------------

// TestJWTRevocation_AdminResetKicksOtherUser asserts that when an admin
// changes another user's password, that user's existing token stops
// authenticating without waiting for the natural 24h expiry.
func TestJWTRevocation_AdminResetKicksOtherUser(t *testing.T) {
	router, _ := newHardeningRouter(t, nil)
	admin, user := setupAdminAndUser(t, router)

	// User's token works.
	w := doJSON(t, router, http.MethodGet, "/api/auth/me", "", user.token)
	if w.Code != http.StatusOK {
		t.Fatalf("baseline /me: expected 200, got %d", w.Code)
	}

	// Admin resets user's password. Bumps token_version atomically.
	w = doJSON(t, router, http.MethodPut,
		"/api/users/"+user.id.String()+"/password",
		`{"password":"newpassword456"}`, admin.token)
	if w.Code != http.StatusNoContent {
		t.Fatalf("admin reset: expected 204, got %d", w.Code)
	}

	// User's old token is now revoked. Cache TTL on this router is 1s, so
	// give it a moment for the next call to definitely re-fetch.
	time.Sleep(1100 * time.Millisecond)
	w = doJSON(t, router, http.MethodGet, "/api/auth/me", "", user.token)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token: expected 401, got %d", w.Code)
	}
}

// TestJWTRevocation_SelfRotateReissues asserts the self-rotation path
// returns a fresh token so the FE keeps the user signed in even though
// the old token is now invalid.
func TestJWTRevocation_SelfRotateReissues(t *testing.T) {
	router, _ := newHardeningRouter(t, nil)
	_, user := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPut,
		"/api/users/"+user.id.String()+"/password",
		`{"password":"newpassword456"}`, user.token)
	if w.Code != http.StatusOK {
		t.Fatalf("self rotate: expected 200, got %d", w.Code)
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	newToken, _ := data["token"].(string)
	if newToken == "" || newToken == user.token {
		t.Fatalf("expected new distinct token, got %q (was %q)", newToken, user.token)
	}

	// New token should authenticate immediately.
	w = doJSON(t, router, http.MethodGet, "/api/auth/me", "", newToken)
	if w.Code != http.StatusOK {
		t.Errorf("new token /me: expected 200, got %d", w.Code)
	}

	// Old token is dead.
	time.Sleep(1100 * time.Millisecond)
	w = doJSON(t, router, http.MethodGet, "/api/auth/me", "", user.token)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("old token: expected 401 after self rotate, got %d", w.Code)
	}
}

// --- shared connector mutation policy --------------------------------------

func TestSharedConnector_OwnerCannotMutate(t *testing.T) {
	router, _ := newHardeningRouter(t, nil)
	admin, user := setupAdminAndUser(t, router)
	_ = admin

	// User creates a private filesystem connector.
	createBody := `{
		"type":"filesystem","name":"alice-fs",
		"config":{"root_path":"/tmp"},
		"enabled":true,"schedule":"","shared":false
	}`
	w := doJSON(t, router, http.MethodPost, "/api/connectors/", createBody, user.token)
	if w.Code != http.StatusCreated {
		t.Fatalf("create connector: %d %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	cid, _ := data["id"].(string)

	// User flips it to shared via admin (the only path that's still
	// allowed for the shared bit).
	updateShared := `{
		"type":"filesystem","name":"alice-fs",
		"config":{"root_path":"/tmp"},
		"enabled":true,"schedule":"","shared":true
	}`
	w = doJSON(t, router, http.MethodPut, "/api/connectors/"+cid, updateShared, admin.token)
	if w.Code != http.StatusOK {
		t.Fatalf("admin flip-to-shared: %d %s", w.Code, w.Body.String())
	}

	// Owner now tries to flip it back to private — must be forbidden.
	updateUnshared := `{
		"type":"filesystem","name":"alice-fs",
		"config":{"root_path":"/tmp"},
		"enabled":true,"schedule":"","shared":false
	}`
	w = doJSON(t, router, http.MethodPut, "/api/connectors/"+cid, updateUnshared, user.token)
	if w.Code != http.StatusForbidden {
		t.Errorf("owner mutate-shared: expected 403, got %d (%s)", w.Code, strings.TrimSpace(w.Body.String()))
	}

	// Owner also can't delete the shared connector.
	w = doJSON(t, router, http.MethodDelete, "/api/connectors/"+cid, "", user.token)
	if w.Code != http.StatusForbidden {
		t.Errorf("owner delete-shared: expected 403, got %d", w.Code)
	}

	// Admin still can.
	w = doJSON(t, router, http.MethodDelete, "/api/connectors/"+cid, "", admin.token)
	if w.Code != http.StatusNoContent {
		t.Errorf("admin delete-shared: expected 204, got %d", w.Code)
	}
}

// --- store-level: CreateFirstAdmin atomicity -------------------------------

func TestCreateFirstAdmin_SecondCallReturnsErrFirstAdminExists(t *testing.T) {
	_, st := newHardeningRouter(t, nil)

	hash, _ := auth.HashPassword("password123")
	if _, err := st.CreateFirstAdmin(context.Background(), "first", hash); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := st.CreateFirstAdmin(context.Background(), "second", hash)
	if err == nil {
		t.Fatal("second call should fail")
	}
	if err != store.ErrFirstAdminExists {
		t.Errorf("expected ErrFirstAdminExists, got %v", err)
	}
}
