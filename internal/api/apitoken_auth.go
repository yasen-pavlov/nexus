package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

const (
	// apiTokenCacheTTL bounds how long a validated token is served from memory
	// before re-checking the DB. Short enough that a revoked (deleted) token
	// stops working quickly, long enough to absorb an agent's request bursts.
	// Mirrors the JWT revocation cache window.
	apiTokenCacheTTL = 30 * time.Second
	// apiTokenTouchInterval throttles last_used_at writes — it's coarse
	// telemetry ("last used 3h ago"), not correctness, so we don't issue a
	// write per request.
	apiTokenTouchInterval = 5 * time.Minute
)

// apiTokenAuthenticator validates personal access tokens against the store,
// with a small TTL cache so a busy agent doesn't trigger a DB roundtrip on
// every request. It exposes validate as an auth.APITokenValidator.
type apiTokenAuthenticator struct {
	store *store.Store
	log   *zap.Logger

	mu    sync.Mutex
	cache map[string]*apiTokenCacheEntry // keyed by token hash
}

type apiTokenCacheEntry struct {
	claims      *auth.Claims
	tokenID     uuid.UUID
	expiresAt   *time.Time
	cachedAt    time.Time
	lastTouched time.Time
}

func newAPITokenAuthenticator(st *store.Store, log *zap.Logger) *apiTokenAuthenticator {
	return &apiTokenAuthenticator{
		store: st,
		log:   log,
		cache: make(map[string]*apiTokenCacheEntry),
	}
}

// errAPITokenInvalid is the single opaque error returned for any unusable
// token (unknown, expired, owner deleted). The middleware maps it to a generic
// 401 so callers can't distinguish the cases.
var errAPITokenInvalid = errors.New("api token invalid")

// validate resolves a token plaintext to its owner's Claims. It satisfies
// auth.APITokenValidator.
func (a *apiTokenAuthenticator) validate(ctx context.Context, plaintext string) (*auth.Claims, error) {
	hash := auth.HashAPIToken(plaintext)
	now := time.Now()

	if entry := a.cached(hash, now); entry != nil {
		a.maybeTouch(ctx, hash, entry, now)
		return entry.claims, nil
	}

	idn, err := a.store.GetAPITokenByHash(ctx, hash)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			a.log.Error("api token lookup failed", zap.Error(err))
		}
		return nil, errAPITokenInvalid
	}
	if idn.ExpiresAt != nil && now.After(*idn.ExpiresAt) {
		return nil, errAPITokenInvalid
	}

	claims := &auth.Claims{
		UserID:   idn.UserID,
		Username: idn.Username,
		Role:     idn.Role,
	}
	entry := &apiTokenCacheEntry{
		claims:    claims,
		tokenID:   idn.TokenID,
		expiresAt: idn.ExpiresAt,
		cachedAt:  now,
	}
	a.mu.Lock()
	a.cache[hash] = entry
	a.mu.Unlock()

	a.maybeTouch(ctx, hash, entry, now)
	return claims, nil
}

// cached returns a live, unexpired cache entry for the hash, or nil. Expired
// or stale entries are evicted.
func (a *apiTokenAuthenticator) cached(hash string, now time.Time) *apiTokenCacheEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.cache[hash]
	if !ok {
		return nil
	}
	if now.Sub(entry.cachedAt) >= apiTokenCacheTTL {
		delete(a.cache, hash)
		return nil
	}
	if entry.expiresAt != nil && now.After(*entry.expiresAt) {
		delete(a.cache, hash)
		return nil
	}
	return entry
}

// maybeTouch updates last_used_at at most once per apiTokenTouchInterval, in
// the background so it never blocks (or is cancelled by) the request.
func (a *apiTokenAuthenticator) maybeTouch(_ context.Context, hash string, entry *apiTokenCacheEntry, now time.Time) {
	a.mu.Lock()
	if !entry.lastTouched.IsZero() && now.Sub(entry.lastTouched) < apiTokenTouchInterval {
		a.mu.Unlock()
		return
	}
	entry.lastTouched = now
	// Re-point the map entry in case it was replaced concurrently.
	a.cache[hash] = entry
	tokenID := entry.tokenID
	a.mu.Unlock()

	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.store.TouchAPITokenLastUsed(bg, tokenID); err != nil {
			a.log.Warn("api token touch last_used failed", zap.Error(err))
		}
	}()
}

// invalidateByTokenID drops a token from the cache by its row id. Called when
// a token is revoked so it stops working immediately rather than lingering for
// the cache TTL. The cache only holds actively-used tokens, so the linear scan
// is cheap.
func (a *apiTokenAuthenticator) invalidateByTokenID(tokenID uuid.UUID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for hash, entry := range a.cache {
		if entry.tokenID == tokenID {
			delete(a.cache, hash)
			return
		}
	}
}
