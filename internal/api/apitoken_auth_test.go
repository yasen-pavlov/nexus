package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"go.uber.org/zap"
)

// TestAPITokenAuthenticator_CacheHit exercises the cache-hit fast path without
// a store: a primed, fresh entry returns its claims and (because lastTouched is
// recent) skips the background touch, so the nil store is never dereferenced.
func TestAPITokenAuthenticator_CacheHit(t *testing.T) {
	a := newAPITokenAuthenticator(nil, zap.NewNop())
	plaintext := "nexus_pat_cachehit"
	hash := auth.HashAPIToken(plaintext)
	now := time.Now()
	claims := &auth.Claims{UserID: uuid.New(), Username: "u", Role: "user"}
	a.cache[hash] = &apiTokenCacheEntry{
		claims:      claims,
		tokenID:     uuid.New(),
		cachedAt:    now,
		lastTouched: now,
	}

	got, err := a.validate(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got != claims {
		t.Errorf("expected cached claims, got %+v", got)
	}
}

// TestAPITokenAuthenticator_Cached covers the eviction branches of cached().
func TestAPITokenAuthenticator_Cached(t *testing.T) {
	a := newAPITokenAuthenticator(nil, zap.NewNop())
	now := time.Now()

	// Stale (older than TTL) → evicted, returns nil.
	a.cache["stale"] = &apiTokenCacheEntry{cachedAt: now.Add(-2 * apiTokenCacheTTL)}
	if a.cached("stale", now) != nil {
		t.Error("stale entry should not be returned")
	}
	if _, ok := a.cache["stale"]; ok {
		t.Error("stale entry should be evicted")
	}

	// Expired (past expires_at) → evicted, returns nil.
	past := now.Add(-time.Minute)
	a.cache["exp"] = &apiTokenCacheEntry{cachedAt: now, expiresAt: &past}
	if a.cached("exp", now) != nil {
		t.Error("expired entry should not be returned")
	}
	if _, ok := a.cache["exp"]; ok {
		t.Error("expired entry should be evicted")
	}

	// Missing → nil.
	if a.cached("nope", now) != nil {
		t.Error("missing key should return nil")
	}

	// Live → returned.
	a.cache["live"] = &apiTokenCacheEntry{cachedAt: now}
	if a.cached("live", now) == nil {
		t.Error("live entry should be returned")
	}
}

// TestAPITokenAuthenticator_InvalidateByTokenID covers the revoke-eviction scan.
func TestAPITokenAuthenticator_InvalidateByTokenID(t *testing.T) {
	a := newAPITokenAuthenticator(nil, zap.NewNop())
	id := uuid.New()
	a.cache["h"] = &apiTokenCacheEntry{tokenID: id, cachedAt: time.Now()}

	a.invalidateByTokenID(id)
	if _, ok := a.cache["h"]; ok {
		t.Error("entry should be removed after invalidate")
	}
	// Unknown id is a no-op (must not panic).
	a.invalidateByTokenID(uuid.New())
}
