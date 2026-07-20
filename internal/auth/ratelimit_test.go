package auth

import (
	"testing"
	"time"
)

func TestLoginRateLimiter_AllowsUntilThreshold(t *testing.T) {
	l := NewLoginRateLimiter(LoginRateLimiterConfig{
		MaxAttempts: 3,
		Window:      time.Minute,
		Lockout:     time.Minute,
	})

	for i := range 3 {
		ok, _ := l.Allow("alice", "1.2.3.4")
		if !ok {
			t.Fatalf("attempt %d: expected allowed, got blocked", i+1)
		}
		l.RecordFailure("alice", "1.2.3.4")
	}

	// 4th attempt should be blocked.
	ok, retry := l.Allow("alice", "1.2.3.4")
	if ok {
		t.Error("expected blocked on 4th attempt")
	}
	if retry <= 0 {
		t.Errorf("expected positive Retry-After, got %s", retry)
	}
}

func TestLoginRateLimiter_PerBucketIsolation(t *testing.T) {
	l := NewLoginRateLimiter(LoginRateLimiterConfig{
		MaxAttempts: 2,
		Window:      time.Minute,
		Lockout:     time.Minute,
	})

	// Trip alice from one IP.
	l.RecordFailure("alice", "1.2.3.4")
	l.RecordFailure("alice", "1.2.3.4")

	// alice from same IP is locked.
	if ok, _ := l.Allow("alice", "1.2.3.4"); ok {
		t.Error("alice from 1.2.3.4 should be locked")
	}
	// alice from a different IP is independent.
	if ok, _ := l.Allow("alice", "5.6.7.8"); !ok {
		t.Error("alice from 5.6.7.8 should be unaffected")
	}
	// bob from the same IP is independent.
	if ok, _ := l.Allow("bob", "1.2.3.4"); !ok {
		t.Error("bob from 1.2.3.4 should be unaffected")
	}
}

func TestLoginRateLimiter_SuccessResetsBucket(t *testing.T) {
	l := NewLoginRateLimiter(LoginRateLimiterConfig{
		MaxAttempts: 2,
		Window:      time.Minute,
		Lockout:     time.Minute,
	})

	l.RecordFailure("alice", "1.2.3.4")
	l.RecordSuccess("alice", "1.2.3.4")

	// After success, the next failure starts a fresh count — wouldn't lock
	// out until MaxAttempts, not MaxAttempts-1.
	l.RecordFailure("alice", "1.2.3.4")
	if ok, _ := l.Allow("alice", "1.2.3.4"); !ok {
		t.Error("expected allowed after success reset; only one fresh failure recorded")
	}
}

func TestLoginRateLimiter_PrunesStaleBucketOnAllow(t *testing.T) {
	l := NewLoginRateLimiter(LoginRateLimiterConfig{
		MaxAttempts: 3,
		Window:      20 * time.Millisecond,
		Lockout:     20 * time.Millisecond,
	})

	l.RecordFailure("alice", "1.2.3.4")
	if len(l.buckets) != 1 {
		t.Fatalf("expected 1 bucket after failure, got %d", len(l.buckets))
	}

	time.Sleep(40 * time.Millisecond) // the single attempt ages out of the window

	// Allow encounters a bucket with no live attempts and an expired lockout —
	// it must delete it in the same lock rather than leave it lingering.
	if ok, _ := l.Allow("alice", "1.2.3.4"); !ok {
		t.Fatal("a fully-cooled bucket should allow")
	}
	if len(l.buckets) != 0 {
		t.Errorf("stale bucket not pruned on Allow: %d remain", len(l.buckets))
	}
}

func TestLoginRateLimiter_SweepPrunesStaleBucketsOnFailure(t *testing.T) {
	l := NewLoginRateLimiter(LoginRateLimiterConfig{
		MaxAttempts: 3,
		Window:      20 * time.Millisecond,
		Lockout:     20 * time.Millisecond,
	})

	// Spray distinct (username, ip) keys — the credential-stuffing shape that
	// would otherwise grow the map without bound. The first call sweeps the
	// (empty) map and stamps lastSweep; the next two land inside the window so
	// no further sweep runs, leaving all three buckets present.
	l.RecordFailure("a", "1.1.1.1")
	l.RecordFailure("b", "2.2.2.2")
	l.RecordFailure("c", "3.3.3.3")
	if len(l.buckets) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(l.buckets))
	}

	time.Sleep(40 * time.Millisecond) // every attempt ages out and lockouts expire

	// A failure for a new key more than a window after the last sweep triggers
	// the sweep, which drops the three now-stale buckets before adding the
	// fresh one.
	l.RecordFailure("d", "4.4.4.4")
	if len(l.buckets) != 1 {
		t.Errorf("sweep should leave only the fresh bucket, got %d", len(l.buckets))
	}
	if _, ok := l.buckets[bucketKey("d", "4.4.4.4")]; !ok {
		t.Error("fresh bucket missing after sweep")
	}
}

func TestLoginRateLimiter_NilSafe(t *testing.T) {
	var l *LoginRateLimiter
	ok, retry := l.Allow("alice", "1.2.3.4")
	if !ok || retry != 0 {
		t.Errorf("nil limiter should allow with zero retry, got ok=%v retry=%s", ok, retry)
	}
	// RecordFailure / RecordSuccess on nil are no-ops.
	l.RecordFailure("alice", "1.2.3.4")
	l.RecordSuccess("alice", "1.2.3.4")
}

func TestLoginRateLimiter_DefaultsAppliedOnZero(t *testing.T) {
	l := NewLoginRateLimiter(LoginRateLimiterConfig{}) // all zeros
	def := DefaultLoginRateLimiterConfig()
	if l.maxAttempts != def.MaxAttempts || l.window != def.Window || l.lockout != def.Lockout {
		t.Errorf("zero config not defaulted: got max=%d win=%s lock=%s",
			l.maxAttempts, l.window, l.lockout)
	}
}
