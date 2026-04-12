//go:build integration

package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/muty/nexus/internal/store"
	"github.com/muty/nexus/internal/testutil"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

// testPools maps a BinaryStore to the underlying pgxpool.Pool so
// eviction tests can run direct SQL (backdating last_accessed_at) that
// the BinaryStore API intentionally doesn't expose.
var testPools = map[*BinaryStore]*pgxpool.Pool{}

// newTestStoreFromTestDB spins up a fresh test database (isolated per
// storage package) and returns a BinaryStore pointed at a temp dir plus
// the temp dir path for assertions.
func newTestStoreFromTestDB(t *testing.T) (*BinaryStore, string) {
	t.Helper()
	tdb := testutil.NewTestDB(t, "storage", migrations.FS)
	st, err := store.New(context.Background(), tdb.URL, zap.NewNop())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	base := filepath.Join(t.TempDir(), "bin")
	bs, err := New(base, st, zap.NewNop())
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	testPools[bs] = tdb.Pool
	t.Cleanup(func() { delete(testPools, bs) })
	return bs, base
}

// execRaw runs a raw SQL statement against the test DB bound to bs.
// Used by eviction tests to backdate last_accessed_at so the eviction
// logic can be exercised without waiting through the real TTL.
func execRaw(t *testing.T, bs *BinaryStore, sql string) {
	t.Helper()
	pool, ok := testPools[bs]
	if !ok {
		t.Fatal("no test pool registered for this BinaryStore")
	}
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("execRaw: %v", err)
	}
}

func TestPut_CreatesFileAndRow(t *testing.T) {
	bs, base := newTestStoreFromTestDB(t)
	ctx := context.Background()

	payload := []byte("hello binary")
	if err := bs.Put(ctx, "imap", "icloud", "msg-1", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}

	// Blob lives under base/imap/icloud/<hash>.bin
	entries, err := os.ReadDir(filepath.Join(base, "imap", "icloud"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 blob file, got %d", len(entries))
	}

	// Get should return the same bytes.
	r, err := bs.Get(ctx, "imap", "icloud", "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("got %q, want %q", got, payload)
	}
}

func TestGet_NotCached_ReturnsErrNotExist(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	ctx := context.Background()

	_, err := bs.Get(ctx, "imap", "icloud", "missing")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want os.ErrNotExist, got %v", err)
	}
}

func TestPut_Idempotent(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	ctx := context.Background()

	if err := bs.Put(ctx, "imap", "icloud", "msg-1", bytes.NewReader([]byte("v1")), 2); err != nil {
		t.Fatal(err)
	}
	if err := bs.Put(ctx, "imap", "icloud", "msg-1", bytes.NewReader([]byte("version-two")), 11); err != nil {
		t.Fatal(err)
	}

	r, err := bs.Get(ctx, "imap", "icloud", "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup

	got, _ := io.ReadAll(r)
	if string(got) != "version-two" {
		t.Errorf("got %q, want 'version-two'", got)
	}
}

func TestExists(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	ctx := context.Background()

	ok, err := bs.Exists(ctx, "imap", "icloud", "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("should not exist before Put")
	}

	if err := bs.Put(ctx, "imap", "icloud", "msg-1", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatal(err)
	}
	ok, err = bs.Exists(ctx, "imap", "icloud", "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("should exist after Put")
	}
}

func TestDelete_RemovesFileAndRow(t *testing.T) {
	bs, base := newTestStoreFromTestDB(t)
	ctx := context.Background()

	if err := bs.Put(ctx, "imap", "icloud", "msg-1", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatal(err)
	}
	if err := bs.Delete(ctx, "imap", "icloud", "msg-1"); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(filepath.Join(base, "imap", "icloud"))
	if len(entries) != 0 {
		t.Errorf("expected empty dir after delete, got %d files", len(entries))
	}

	ok, _ := bs.Exists(ctx, "imap", "icloud", "msg-1")
	if ok {
		t.Error("should not exist after delete")
	}
}

func TestDelete_Missing_NoError(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	if err := bs.Delete(context.Background(), "imap", "icloud", "nope"); err != nil {
		t.Errorf("deleting missing entry should not error, got %v", err)
	}
}

func TestDeleteBySource_RemovesAll(t *testing.T) {
	bs, base := newTestStoreFromTestDB(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c"} {
		if err := bs.Put(ctx, "imap", "icloud", id, bytes.NewReader([]byte(id)), 1); err != nil {
			t.Fatal(err)
		}
	}
	// Different source should survive.
	if err := bs.Put(ctx, "imap", "work", "z", bytes.NewReader([]byte("z")), 1); err != nil {
		t.Fatal(err)
	}

	if err := bs.DeleteBySource(ctx, "imap", "icloud"); err != nil {
		t.Fatal(err)
	}

	// icloud dir should be gone (or empty).
	if entries, err := os.ReadDir(filepath.Join(base, "imap", "icloud")); err == nil && len(entries) != 0 {
		t.Errorf("expected icloud dir empty or absent, got %d files", len(entries))
	}
	// work entry still exists.
	ok, err := bs.Exists(ctx, "imap", "work", "z")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("unrelated source should survive DeleteBySource")
	}
}

func TestGet_StaleRow_CleanedUp(t *testing.T) {
	bs, base := newTestStoreFromTestDB(t)
	ctx := context.Background()

	if err := bs.Put(ctx, "imap", "icloud", "msg-1", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatal(err)
	}
	// Simulate out-of-band file removal (e.g., volume corruption).
	entries, _ := os.ReadDir(filepath.Join(base, "imap", "icloud"))
	for _, e := range entries {
		_ = os.Remove(filepath.Join(base, "imap", "icloud", e.Name()))
	}

	_, err := bs.Get(ctx, "imap", "icloud", "msg-1")
	if err == nil {
		t.Fatal("expected error when blob file is missing")
	}
	// Second Get should now report not-cached because the stale row was cleaned up.
	_, err = bs.Get(ctx, "imap", "icloud", "msg-1")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist after stale row cleanup, got %v", err)
	}
}

func TestDeleteAll_RemovesAllBlobsAndRows(t *testing.T) {
	bs, base := newTestStoreFromTestDB(t)
	ctx := context.Background()

	for _, p := range []struct{ st, sn, sid string }{
		{"imap", "icloud", "a"},
		{"imap", "icloud", "b"},
		{"telegram", "personal", "c"},
	} {
		if err := bs.Put(ctx, p.st, p.sn, p.sid, bytes.NewReader([]byte("x")), 1); err != nil {
			t.Fatal(err)
		}
	}

	if err := bs.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}

	// No cached entries.
	stats, err := bs.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty stats after DeleteAll, got %+v", stats)
	}

	// Empty source_type subdirectories should be pruned.
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("expected empty base dir after DeleteAll, still has %q", e.Name())
		}
	}
}

func TestDeleteAll_EmptyCache(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	if err := bs.DeleteAll(context.Background()); err != nil {
		t.Errorf("DeleteAll on empty cache should not error, got %v", err)
	}
}

// errReader is a Reader that returns an error mid-stream. Used to
// verify Put cleans up its temp file on copy failure.
type errReader struct{ n int }

func (e *errReader) Read(p []byte) (int, error) {
	if e.n == 0 {
		e.n = 1
		return copy(p, []byte("prefix")), nil
	}
	return 0, io.ErrUnexpectedEOF
}

func TestPut_ReaderError_CleansUpTempFile(t *testing.T) {
	bs, base := newTestStoreFromTestDB(t)
	ctx := context.Background()

	err := bs.Put(ctx, "imap", "icloud", "broken", &errReader{}, 0)
	if err == nil {
		t.Fatal("expected error when reader fails mid-copy")
	}

	// No blob should have been committed.
	entries, _ := os.ReadDir(filepath.Join(base, "imap", "icloud"))
	for _, e := range entries {
		// Only leftover temp files (.bin-*) would fail this — the deferred
		// cleanup in Put should have removed them.
		if filepath.Ext(e.Name()) == ".bin" {
			t.Errorf("unexpected committed blob after error: %s", e.Name())
		}
	}
	// Also — no DB row should exist.
	if ok, _ := bs.Exists(ctx, "imap", "icloud", "broken"); ok {
		t.Error("no metadata row should be written when Put fails")
	}
}

func TestNew_UnwritableBasePath(t *testing.T) {
	// Try to create the store under a path whose parent is a file, not a
	// directory — MkdirAll will fail.
	notDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New(filepath.Join(notDir, "nested"), nil, nil)
	if err == nil {
		t.Error("expected error when base path parent is a regular file")
	}
}

func TestStats_AggregatesBySource(t *testing.T) {
	bs, _ := newTestStoreFromTestDB(t)
	ctx := context.Background()

	puts := []struct {
		st, sn, sid string
		size        int
	}{
		{"imap", "icloud", "a", 100},
		{"imap", "icloud", "b", 200},
		{"telegram", "personal", "c", 500},
	}
	for _, p := range puts {
		buf := bytes.Repeat([]byte("x"), p.size)
		if err := bs.Put(ctx, p.st, p.sn, p.sid, bytes.NewReader(buf), int64(p.size)); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := bs.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 aggregated groups, got %d: %+v", len(stats), stats)
	}
	// The DB returns them ordered by source_type, source_name.
	if stats[0].SourceType != "imap" || stats[0].Count != 2 || stats[0].TotalSize != 300 {
		t.Errorf("imap aggregate = %+v, want count=2 size=300", stats[0])
	}
	if stats[1].SourceType != "telegram" || stats[1].Count != 1 || stats[1].TotalSize != 500 {
		t.Errorf("telegram aggregate = %+v, want count=1 size=500", stats[1])
	}
}
