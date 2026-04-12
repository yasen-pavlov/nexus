//go:build integration

package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEviction_MaxAge_RemovesOldEntries(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	ctx := context.Background()

	// Put two imap entries and backdate one via direct SQL.
	if err := bs.Put(ctx, "imap", "icloud", "old", bytes.NewReader([]byte("old")), 3); err != nil {
		t.Fatal(err)
	}
	if err := bs.Put(ctx, "imap", "icloud", "new", bytes.NewReader([]byte("new")), 3); err != nil {
		t.Fatal(err)
	}

	// Backdate "old" via the StoreDB interface — cast to the concrete
	// store to run the raw update. Tests only.
	execRaw(t, bs, `UPDATE binary_store_entries
	                SET last_accessed_at = NOW() - INTERVAL '100 days'
	                WHERE source_id = 'old'`)

	// Run a single eviction pass.
	bs.evictOnce(ctx, map[string]CacheConfig{
		"imap": {Mode: CacheModeLazy, MaxAge: 30 * 24 * time.Hour},
	})

	// "old" should be gone.
	if ok, _ := bs.Exists(ctx, "imap", "icloud", "old"); ok {
		t.Error("old entry should have been evicted")
	}
	// "new" should survive.
	if ok, _ := bs.Exists(ctx, "imap", "icloud", "new"); !ok {
		t.Error("new entry should NOT be evicted")
	}
}

func TestEviction_MaxSize_RemovesLRU(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	ctx := context.Background()

	// Three entries, 1000 bytes each → 3000 total. Budget 2000 means
	// one oldest must go.
	for _, id := range []string{"a", "b", "c"} {
		payload := bytes.Repeat([]byte("x"), 1000)
		if err := bs.Put(ctx, "imap", "icloud", id, bytes.NewReader(payload), 1000); err != nil {
			t.Fatal(err)
		}
	}
	// Stagger access times: a oldest, c newest.
	execRaw(t, bs,
		`UPDATE binary_store_entries SET last_accessed_at = NOW() - INTERVAL '3 days' WHERE source_id = 'a';
		 UPDATE binary_store_entries SET last_accessed_at = NOW() - INTERVAL '2 days' WHERE source_id = 'b';
		 UPDATE binary_store_entries SET last_accessed_at = NOW() - INTERVAL '1 days' WHERE source_id = 'c';`)

	bs.evictOnce(ctx, map[string]CacheConfig{
		"imap": {Mode: CacheModeLazy, MaxSize: 2000},
	})

	// 'a' should be gone (oldest), 'b' and 'c' should survive.
	if ok, _ := bs.Exists(ctx, "imap", "icloud", "a"); ok {
		t.Error("oldest entry 'a' should have been evicted")
	}
	if ok, _ := bs.Exists(ctx, "imap", "icloud", "b"); !ok {
		t.Error("entry 'b' should survive")
	}
	if ok, _ := bs.Exists(ctx, "imap", "icloud", "c"); !ok {
		t.Error("newest entry 'c' should survive")
	}
}

func TestEviction_NoPolicy_Skipped(t *testing.T) {
	bs, base := newTestStoreFromTestDB(t)
	ctx := context.Background()

	if err := bs.Put(ctx, "imap", "icloud", "x", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatal(err)
	}
	execRaw(t, bs, `UPDATE binary_store_entries SET last_accessed_at = NOW() - INTERVAL '500 days'`)

	// Policy map is empty for imap — eviction should NOT touch it.
	bs.evictOnce(ctx, map[string]CacheConfig{
		"telegram": {Mode: CacheModeEager, MaxAge: 7 * 24 * time.Hour},
	})

	if ok, _ := bs.Exists(ctx, "imap", "icloud", "x"); !ok {
		t.Error("entry should survive when its source type has no policy")
	}
	entries, _ := os.ReadDir(filepath.Join(base, "imap", "icloud"))
	if len(entries) != 1 {
		t.Errorf("expected 1 blob file, got %d", len(entries))
	}
}

func TestEviction_ZeroMaxAgeZeroMaxSize_NoOp(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	ctx := context.Background()

	if err := bs.Put(ctx, "telegram", "personal", "x", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatal(err)
	}
	execRaw(t, bs, `UPDATE binary_store_entries SET last_accessed_at = NOW() - INTERVAL '500 days'`)

	// Telegram policy with MaxAge=0 and MaxSize=0 means permanent cache.
	bs.evictOnce(ctx, map[string]CacheConfig{
		"telegram": {Mode: CacheModeEager, MaxAge: 0, MaxSize: 0},
	})

	if ok, _ := bs.Exists(ctx, "telegram", "personal", "x"); !ok {
		t.Error("entry should be permanent when policy has no age or size limits")
	}
}

func TestEviction_UnderBudget_NoRemoval(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	ctx := context.Background()

	if err := bs.Put(ctx, "imap", "icloud", "x", bytes.NewReader([]byte("small")), 5); err != nil {
		t.Fatal(err)
	}
	// Budget 1000 is way above actual 5 bytes — should not evict.
	bs.evictOnce(ctx, map[string]CacheConfig{
		"imap": {Mode: CacheModeLazy, MaxSize: 1000},
	})

	if ok, _ := bs.Exists(ctx, "imap", "icloud", "x"); !ok {
		t.Error("entry under budget should not be evicted")
	}
}

// TestRunEviction_InitialPassReclaims verifies the goroutine performs
// an immediate reclaim pass before entering its tick loop. We seed an
// expired entry, start RunEviction with a fresh context plus a short
// tick interval, wait briefly for the initial pass, then cancel.
func TestRunEviction_InitialPassReclaims(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)

	if err := bs.Put(context.Background(), "imap", "icloud", "old", bytes.NewReader([]byte("old")), 3); err != nil {
		t.Fatal(err)
	}
	execRaw(t, bs, `UPDATE binary_store_entries
	                SET last_accessed_at = NOW() - INTERVAL '100 days'
	                WHERE source_id = 'old'`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		bs.RunEviction(ctx, map[string]CacheConfig{
			"imap": {Mode: CacheModeLazy, MaxAge: 30 * 24 * time.Hour},
		}, time.Hour) // tick irrelevant — we rely on the initial pass
		close(done)
	}()

	// Poll for reclamation rather than sleeping a fixed amount.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok, _ := bs.Exists(context.Background(), "imap", "icloud", "old"); !ok {
			break // reclaimed
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunEviction did not exit within 2s of cancel")
	}

	if ok, _ := bs.Exists(context.Background(), "imap", "icloud", "old"); ok {
		t.Error("expected expired entry to be reclaimed by initial eviction pass")
	}
}

// TestRunEviction_ExitsOnCancel verifies the goroutine exits promptly
// when the context is cancelled, even if the tick interval is long.
func TestRunEviction_ExitsOnCancel(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		bs.RunEviction(ctx, nil, time.Hour)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunEviction did not exit within 2s of cancel")
	}
}

// TestRunEviction_ZeroIntervalDefaults exercises the interval<=0 branch
// by passing a zero interval; the function should clamp to the default
// hour and still perform the initial pass before being cancelled.
func TestRunEviction_ZeroIntervalDefaults(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		bs.RunEviction(ctx, nil, 0) // zero interval + empty policy map
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunEviction did not exit with zero interval + cancelled ctx")
	}
}
