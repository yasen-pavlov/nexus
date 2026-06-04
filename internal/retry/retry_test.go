package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestBackoff_GrowsWithJitter(t *testing.T) {
	base := 100 * time.Millisecond
	d0 := Backoff(0, base)
	d2 := Backoff(2, base)
	if d0 < base || d0 > 2*base {
		t.Errorf("Backoff(0) = %v, want [%v, %v]", d0, base, 2*base)
	}
	if d2 < 4*base { // 100<<2 = 400ms minimum
		t.Errorf("Backoff(2) = %v, want >= %v", d2, 4*base)
	}
}

func TestRetryable_ContextErrorsNeverRetry(t *testing.T) {
	always := func(error) bool { return true }
	if Retryable(context.Canceled, always) {
		t.Error("context.Canceled should not be retryable")
	}
	if Retryable(context.DeadlineExceeded, always) {
		t.Error("context.DeadlineExceeded should not be retryable")
	}
	if !Retryable(errors.New("boom"), always) {
		t.Error("predicate=true should make a plain error retryable")
	}
}

func TestDo_SucceedsAfterRetries(t *testing.T) {
	calls := 0
	got, err := Do(context.Background(), nil, "test", 3, time.Microsecond,
		func() (int, error) {
			calls++
			if calls < 3 {
				return 0, errors.New("transient")
			}
			return 42, nil
		},
		func(error) bool { return true },
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != 42 || calls != 3 {
		t.Errorf("got=%d calls=%d, want 42 / 3", got, calls)
	}
}

func TestDo_LogsBetweenRetries(t *testing.T) {
	// Exercise the log != nil branch with a real (no-op) logger.
	calls := 0
	got, err := Do(context.Background(), zap.NewNop(), "test", 2, time.Microsecond,
		func() (string, error) {
			calls++
			if calls < 2 {
				return "", errors.New("transient")
			}
			return "ok", nil
		},
		func(error) bool { return true },
	)
	if err != nil || got != "ok" || calls != 2 {
		t.Fatalf("got=%q err=%v calls=%d", got, err, calls)
	}
}

func TestDo_StopsOnNonRetryable(t *testing.T) {
	calls := 0
	_, err := Do(context.Background(), nil, "test", 3, time.Microsecond,
		func() (int, error) { calls++; return 0, errors.New("fatal") },
		func(error) bool { return false }, // never retry
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("calls=%d, want 1 (no retries on non-retryable)", calls)
	}
}

func TestDo_ExhaustsAndReturnsLastErr(t *testing.T) {
	calls := 0
	sentinel := errors.New("still failing")
	_, err := Do(context.Background(), nil, "test", 2, time.Microsecond,
		func() (int, error) { calls++; return 0, sentinel },
		func(error) bool { return true },
	)
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want sentinel", err)
	}
	if calls != 3 { // maxRetries(2) + 1 initial
		t.Errorf("calls=%d, want 3", calls)
	}
}

func TestDo_CancelledDuringBackoffReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Do(ctx, nil, "test", 3, time.Hour, // long delay; cancel should short-circuit
		func() (int, error) { return 0, errors.New("transient") },
		func(error) bool { return true },
	)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
