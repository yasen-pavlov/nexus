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
