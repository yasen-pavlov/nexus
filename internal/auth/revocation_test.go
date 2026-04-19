package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTokenRevocationCache_HitMissAndInvalidate(t *testing.T) {
	calls := 0
	uid := uuid.New()
	store := map[uuid.UUID]int{uid: 1}

	c := NewTokenRevocationCache(func(_ context.Context, id uuid.UUID) (int, error) {
		calls++
		v, ok := store[id]
		if !ok {
			return 0, errors.New("not found")
		}
		return v, nil
	}, time.Minute)

	// First call hits the lookup.
	v, err := c.CurrentVersion(context.Background(), uid)
	if err != nil || v != 1 {
		t.Fatalf("first lookup: v=%d err=%v", v, err)
	}
	if calls != 1 {
		t.Errorf("expected 1 lookup, got %d", calls)
	}

	// Second call hits the cache.
	v, _ = c.CurrentVersion(context.Background(), uid)
	if v != 1 || calls != 1 {
		t.Errorf("expected cached, got v=%d calls=%d", v, calls)
	}

	// After bumping the source-of-truth + invalidating, we re-fetch.
	store[uid] = 2
	c.Invalidate(uid)
	v, _ = c.CurrentVersion(context.Background(), uid)
	if v != 2 || calls != 2 {
		t.Errorf("expected fresh fetch after invalidate, got v=%d calls=%d", v, calls)
	}
}

func TestTokenRevocationCache_TTLZeroBypassesCache(t *testing.T) {
	calls := 0
	uid := uuid.New()
	c := NewTokenRevocationCache(func(_ context.Context, _ uuid.UUID) (int, error) {
		calls++
		return 1, nil
	}, 0)

	for range 3 {
		_, _ = c.CurrentVersion(context.Background(), uid)
	}
	if calls != 3 {
		t.Errorf("expected 3 lookups with ttl=0, got %d", calls)
	}
}

func TestRevocationMiddleware_AcceptsMatchingVersion(t *testing.T) {
	uid := uuid.New()
	c := NewTokenRevocationCache(func(_ context.Context, _ uuid.UUID) (int, error) {
		return 7, nil
	}, time.Minute)

	called := false
	mw := RevocationMiddleware(c)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(ContextWithClaims(r.Context(), &Claims{UserID: uid, TokenVersion: 7}))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusOK || !called {
		t.Errorf("expected handler called with 200; got code=%d called=%v", w.Code, called)
	}
}

func TestRevocationMiddleware_RejectsStaleVersion(t *testing.T) {
	uid := uuid.New()
	c := NewTokenRevocationCache(func(_ context.Context, _ uuid.UUID) (int, error) {
		return 7, nil
	}, time.Minute)

	called := false
	mw := RevocationMiddleware(c)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(ContextWithClaims(r.Context(), &Claims{UserID: uid, TokenVersion: 6}))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized || called {
		t.Errorf("expected 401 with handler skipped; got code=%d called=%v", w.Code, called)
	}
}

func TestRevocationMiddleware_LookupErrorIsUnauthorized(t *testing.T) {
	uid := uuid.New()
	c := NewTokenRevocationCache(func(_ context.Context, _ uuid.UUID) (int, error) {
		return 0, errors.New("user gone")
	}, time.Minute)

	mw := RevocationMiddleware(c)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(ContextWithClaims(r.Context(), &Claims{UserID: uid, TokenVersion: 1}))
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 on lookup error, got %d", w.Code)
	}
}

func TestRevocationMiddleware_NilCacheIsNoop(t *testing.T) {
	called := false
	mw := RevocationMiddleware(nil)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if !called {
		t.Error("nil cache should not block requests")
	}
}

func TestRevocationMiddleware_NoClaimsRejected(t *testing.T) {
	c := NewTokenRevocationCache(func(_ context.Context, _ uuid.UUID) (int, error) {
		return 0, nil
	}, time.Minute)
	mw := RevocationMiddleware(c)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/", nil) // no claims attached
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when no claims, got %d", w.Code)
	}
}
