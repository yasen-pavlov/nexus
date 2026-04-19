package auth

import (
	"sync"
	"time"
)

// LoginRateLimiter tracks failed /auth/login attempts per
// (username, IP) bucket. Successful logins reset the bucket. Designed for
// the homelab scale (a handful of users, single binary) — in-memory map
// with a mutex is plenty; nothing here scales horizontally.
//
// Rate-limit policy: max N attempts inside a rolling W-second window.
// When exceeded, the bucket stays "tripped" for L seconds; further
// attempts during lockout are rejected without checking credentials and
// without consuming additional bcrypt cycles.
//
// The two key dimensions (username + IP) are intentional. Throttling
// only by username makes it trivial to lock legitimate users out by
// flooding their account from a botnet; throttling only by IP misses
// distributed credential-stuffing. The combined bucket key
// "user|ip" rate-limits both vectors at once: a single attacker IP
// guessing one account, and a single botnet IP trying many accounts,
// both hit the same limit.
type LoginRateLimiter struct {
	maxAttempts int
	window      time.Duration
	lockout     time.Duration

	mu      sync.Mutex
	buckets map[string]*loginBucket
}

type loginBucket struct {
	attempts    []time.Time // failure timestamps inside `window`
	lockedUntil time.Time
}

// LoginRateLimiterConfig holds the tuning knobs. Defaults
// (5 attempts / 5 min, 5 min lockout) come from the OWASP cheat sheet.
type LoginRateLimiterConfig struct {
	MaxAttempts int
	Window      time.Duration
	Lockout     time.Duration
}

// DefaultLoginRateLimiterConfig returns sensible defaults for a homelab:
// 5 attempts in any rolling 5-minute window, 5-minute lockout after.
func DefaultLoginRateLimiterConfig() LoginRateLimiterConfig {
	return LoginRateLimiterConfig{
		MaxAttempts: 5,
		Window:      5 * time.Minute,
		Lockout:     5 * time.Minute,
	}
}

// NewLoginRateLimiter constructs a limiter with the given config. cfg
// values <= 0 fall back to DefaultLoginRateLimiterConfig() per-field.
func NewLoginRateLimiter(cfg LoginRateLimiterConfig) *LoginRateLimiter {
	def := DefaultLoginRateLimiterConfig()
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = def.MaxAttempts
	}
	if cfg.Window <= 0 {
		cfg.Window = def.Window
	}
	if cfg.Lockout <= 0 {
		cfg.Lockout = def.Lockout
	}
	return &LoginRateLimiter{
		maxAttempts: cfg.MaxAttempts,
		window:      cfg.Window,
		lockout:     cfg.Lockout,
		buckets:     make(map[string]*loginBucket),
	}
}

// Allow returns whether the given (username, ip) should be allowed to
// attempt a login right now. retryAfter is the duration until the next
// attempt is permitted; zero when allowed.
func (l *LoginRateLimiter) Allow(username, ip string) (allowed bool, retryAfter time.Duration) {
	if l == nil {
		return true, 0
	}
	key := bucketKey(username, ip)
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		return true, 0
	}
	if now.Before(b.lockedUntil) {
		return false, b.lockedUntil.Sub(now)
	}
	// Drop attempts outside the window before counting.
	cutoff := now.Add(-l.window)
	live := b.attempts[:0]
	for _, t := range b.attempts {
		if t.After(cutoff) {
			live = append(live, t)
		}
	}
	b.attempts = live
	if len(live) >= l.maxAttempts {
		b.lockedUntil = now.Add(l.lockout)
		return false, l.lockout
	}
	return true, 0
}

// RecordFailure registers one failed attempt against the bucket and
// trips the lockout if MaxAttempts is reached.
func (l *LoginRateLimiter) RecordFailure(username, ip string) {
	if l == nil {
		return
	}
	key := bucketKey(username, ip)
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &loginBucket{}
		l.buckets[key] = b
	}
	cutoff := now.Add(-l.window)
	live := b.attempts[:0]
	for _, t := range b.attempts {
		if t.After(cutoff) {
			live = append(live, t)
		}
	}
	live = append(live, now)
	b.attempts = live
	if len(live) >= l.maxAttempts {
		b.lockedUntil = now.Add(l.lockout)
	}
}

// RecordSuccess clears the bucket on a successful login so the
// legitimate user isn't punished for typing their password wrong twice.
func (l *LoginRateLimiter) RecordSuccess(username, ip string) {
	if l == nil {
		return
	}
	key := bucketKey(username, ip)
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// bucketKey deliberately joins username and IP with a separator that
// can't appear in either field as parsed by chi/net (": " is unused in
// IPs and "|" is illegal in DNS labels — usernames are validated to
// `[a-zA-Z0-9._-]+` so neither character collides).
func bucketKey(username, ip string) string {
	return username + "|" + ip
}
