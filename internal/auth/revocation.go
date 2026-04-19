package auth

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TokenVersionLookup fetches the current `token_version` for a user. The api
// package wires this to a store call. The auth package stays free of any
// store/db dependency this way.
type TokenVersionLookup func(ctx context.Context, userID uuid.UUID) (int, error)

// TokenRevocationCache memoises token_version lookups for a short window so
// the middleware doesn't issue a DB roundtrip per request. Invalidate
// explicitly when a user's version is bumped (e.g. password change).
type TokenRevocationCache struct {
	lookup TokenVersionLookup
	ttl    time.Duration

	mu      sync.Mutex
	entries map[uuid.UUID]versionEntry
}

type versionEntry struct {
	version  int
	cachedAt time.Time
	err      error
}

// NewTokenRevocationCache returns a cache with the given lookup function
// and TTL. ttl=0 disables caching (every check hits the lookup); useful in
// tests. Production should use 30s — short enough that revocations
// propagate quickly, long enough to absorb burst traffic.
func NewTokenRevocationCache(lookup TokenVersionLookup, ttl time.Duration) *TokenRevocationCache {
	return &TokenRevocationCache{
		lookup:  lookup,
		ttl:     ttl,
		entries: make(map[uuid.UUID]versionEntry),
	}
}

// CurrentVersion returns the user's current token_version, possibly from
// cache. The error from the underlying lookup is cached too so a missing
// user (deleted) keeps short-circuiting to 401 without hammering the DB.
func (c *TokenRevocationCache) CurrentVersion(ctx context.Context, userID uuid.UUID) (int, error) {
	if c.ttl > 0 {
		c.mu.Lock()
		e, ok := c.entries[userID]
		c.mu.Unlock()
		if ok && time.Since(e.cachedAt) < c.ttl {
			return e.version, e.err
		}
	}

	v, err := c.lookup(ctx, userID)
	if c.ttl > 0 {
		c.mu.Lock()
		c.entries[userID] = versionEntry{version: v, cachedAt: time.Now(), err: err}
		c.mu.Unlock()
	}
	return v, err
}

// Invalidate drops the cached entry for a user so the next request triggers
// a fresh lookup. Call this from the same goroutine that bumps the user's
// token_version row (e.g. ChangePassword) so the new value propagates
// immediately rather than after the TTL.
func (c *TokenRevocationCache) Invalidate(userID uuid.UUID) {
	c.mu.Lock()
	delete(c.entries, userID)
	c.mu.Unlock()
}

// RevocationMiddleware compares the request's token_version claim against
// the user's current version. Mismatches (e.g. token was minted before
// the password was rotated) and missing users (deleted accounts) both
// return 401. Mount AFTER Middleware so claims are already on the context.
//
// When cache is nil the middleware is a no-op — useful for tests that
// don't want the lookup overhead.
func RevocationMiddleware(cache *TokenRevocationCache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cache == nil {
				next.ServeHTTP(w, r)
				return
			}
			claims := UserFromContext(r.Context())
			if claims == nil {
				// No claims = the upstream Middleware should already have
				// rejected, but defend in depth.
				http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
				return
			}
			current, err := cache.CurrentVersion(r.Context(), claims.UserID)
			if err != nil {
				// User vanished (deleted) or DB hiccup; treat as expired so
				// the FE bounces to /login. Logging happens at the lookup
				// site since we don't have the logger here.
				http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
				return
			}
			if claims.TokenVersion != current {
				http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
