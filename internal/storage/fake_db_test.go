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
	"time"

	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

func bytesReaderOf(s string) io.Reader { return bytes.NewReader([]byte(s)) }

// fakeDB is a StoreDB stub that lets tests inject errors on specific
// method calls. The default zero-value is a pass-through that returns
// empty/nil for everything.
type fakeDB struct {
	listExpiredErr error
	listLRUErr     error
	totalSizeErr   error
	statsErr       error
	upsertErr      error
	touchErr       error
	getErr         error
	deleteErr      error
	deleteBySrcErr error
	deleteAllErr   error
	totalSize      int64
	lruEntries     []model.BinaryStoreEntry
	expiredEntries []model.BinaryStoreEntry
}

func (f *fakeDB) UpsertBinaryStoreEntry(context.Context, *model.BinaryStoreEntry) error {
	return f.upsertErr
}
func (f *fakeDB) TouchBinaryStoreEntry(context.Context, string, string, string) (bool, error) {
	return true, f.touchErr
}
func (f *fakeDB) GetBinaryStoreEntry(context.Context, string, string, string) (*model.BinaryStoreEntry, error) {
	return nil, f.getErr
}
func (f *fakeDB) DeleteBinaryStoreEntry(context.Context, string, string, string) (string, error) {
	return "", f.deleteErr
}
func (f *fakeDB) DeleteBinaryStoreBySource(context.Context, string, string) ([]string, error) {
	return nil, f.deleteBySrcErr
}
func (f *fakeDB) DeleteAllBinaryStoreEntries(context.Context) ([]string, error) {
	return nil, f.deleteAllErr
}
func (f *fakeDB) ListExpiredBinaryStoreEntries(context.Context, string, time.Duration) ([]model.BinaryStoreEntry, error) {
	return f.expiredEntries, f.listExpiredErr
}
func (f *fakeDB) ListLRUBinaryStoreEntries(context.Context, string) ([]model.BinaryStoreEntry, error) {
	return f.lruEntries, f.listLRUErr
}
func (f *fakeDB) BinaryStoreTotalSize(context.Context, string) (int64, error) {
	return f.totalSize, f.totalSizeErr
}
func (f *fakeDB) BinaryStoreStats(context.Context) ([]model.BinaryStoreStats, error) {
	return nil, f.statsErr
}

func newFakeBS(t *testing.T, db *fakeDB) *BinaryStore {
	t.Helper()
	bs, err := New(filepath.Join(t.TempDir(), "bin"), db, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return bs
}

// TestEviction_DBErrors_AreLoggedAndContinue verifies the eviction
// goroutine tolerates DB failures: a ListExpired error, a TotalSize
// error, and a ListLRU error each log a warning but don't crash.
func TestEviction_DBErrors_AreLoggedAndContinue(t *testing.T) {
	ctx := context.Background()
	oops := errors.New("db down")

	cases := []struct {
		name string
		db   *fakeDB
	}{
		{"list expired error", &fakeDB{listExpiredErr: oops}},
		{"total size error", &fakeDB{totalSizeErr: oops}},
		{"list lru error", &fakeDB{totalSize: 1 << 40, listLRUErr: oops}}, // force over-budget branch
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bs := newFakeBS(t, tc.db)
			// Should not panic or hang.
			bs.evictOnce(ctx, map[string]CacheConfig{
				"imap": {Mode: CacheModeLazy, MaxAge: 30 * 24 * time.Hour, MaxSize: 1000},
			})
		})
	}
}

// TestStats_DBError surfaces the error path in BinaryStore.Stats.
func TestStats_DBError(t *testing.T) {
	bs := newFakeBS(t, &fakeDB{statsErr: errors.New("boom")})
	if _, err := bs.Stats(context.Background()); err == nil {
		t.Error("expected error from Stats when DB fails")
	}
}

// TestDeleteBySource_DBError surfaces the error path in BinaryStore.
// DeleteBySource.
func TestDeleteBySource_DBError(t *testing.T) {
	bs := newFakeBS(t, &fakeDB{deleteBySrcErr: errors.New("boom")})
	if err := bs.DeleteBySource(context.Background(), "imap", "icloud"); err == nil {
		t.Error("expected error from DeleteBySource when DB fails")
	}
}

// TestDeleteAll_DBError surfaces the error path in BinaryStore.DeleteAll.
func TestDeleteAll_DBError(t *testing.T) {
	bs := newFakeBS(t, &fakeDB{deleteAllErr: errors.New("boom")})
	if err := bs.DeleteAll(context.Background()); err == nil {
		t.Error("expected error from DeleteAll when DB fails")
	}
}

// TestDelete_DBError surfaces the error path in BinaryStore.Delete.
func TestDelete_DBError(t *testing.T) {
	bs := newFakeBS(t, &fakeDB{deleteErr: errors.New("boom")})
	if err := bs.Delete(context.Background(), "imap", "icloud", "x"); err == nil {
		t.Error("expected error from Delete when DB fails")
	}
}

// TestExists_DBError surfaces the error path in BinaryStore.Exists.
func TestExists_DBError(t *testing.T) {
	bs := newFakeBS(t, &fakeDB{getErr: errors.New("boom")})
	if _, err := bs.Exists(context.Background(), "imap", "icloud", "x"); err == nil {
		t.Error("expected error from Exists when DB fails")
	}
}

// TestGet_TouchError surfaces the error path in BinaryStore.Get when
// the metadata touch fails.
func TestGet_TouchError(t *testing.T) {
	bs := newFakeBS(t, &fakeDB{touchErr: errors.New("boom")})
	if _, err := bs.Get(context.Background(), "imap", "icloud", "x"); err == nil {
		t.Error("expected error from Get when touch fails")
	}
}

// TestPut_RollsBackBlobOnUpsertError: if writing the DB metadata row
// fails after the blob is on disk, Put rolls back by deleting the
// blob so we don't leak orphaned files.
func TestPut_RollsBackBlobOnUpsertError(t *testing.T) {
	db := &fakeDB{upsertErr: errors.New("db down")}
	bs := newFakeBS(t, db)
	err := bs.Put(context.Background(), "imap", "icloud", "msg-1", bytesReaderOf("hi"), 2)
	if err == nil {
		t.Fatal("expected error when upsert fails")
	}
	// Blob file should have been removed by the rollback.
	probe := filepath.Join(bs.basePath, "imap", "icloud")
	entries, _ := os.ReadDir(probe)
	for _, e := range entries {
		if e.Name() != "." && e.Name() != ".." {
			t.Errorf("expected empty dir after rollback, found %q", e.Name())
		}
	}
}

// TestPut_SizeFallsBackToWrittenBytes covers the `finalSize <= 0` branch
// of Put — when size is 0 (unknown), the method records the actual
// bytes written as the row's size.
func TestPut_SizeFallsBackToWrittenBytes(t *testing.T) {
	db := &fakeDB{}
	bs := newFakeBS(t, db)
	if err := bs.Put(context.Background(), "imap", "icloud", "msg-2",
		bytesReaderOf("twelve-bytes"), 0); err != nil {
		t.Fatal(err)
	}
}

// TestEvictOverBudget_DeletePathExercised drives the over-budget branch:
// total size exceeds the policy budget, ListLRU returns real rows, and
// the store proceeds to delete the oldest until under budget. This
// covers the bulk of evictOverBudget.
func TestEvictOverBudget_DeletePathExercised(t *testing.T) {
	// Two entries. Budget is 1 byte; both get evicted.
	lru := []model.BinaryStoreEntry{
		{SourceType: "imap", SourceName: "icloud", SourceID: "a", FilePath: "/tmp/a", Size: 100},
		{SourceType: "imap", SourceName: "icloud", SourceID: "b", FilePath: "/tmp/b", Size: 100},
	}
	db := &fakeDB{totalSize: 200, lruEntries: lru}
	bs := newFakeBS(t, db)
	bs.evictOnce(context.Background(), map[string]CacheConfig{
		"imap": {Mode: CacheModeLazy, MaxAge: 0, MaxSize: 1},
	})
}

// TestPut_UnwritableBasePath verifies Put surfaces an error when the
// base directory can't be created or written. We point the store at a
// base path that already exists as a regular file, so MkdirAll fails.
func TestPut_UnwritableBasePath(t *testing.T) {
	// Create a file where the base dir's source_type subdir should go.
	base := filepath.Join(t.TempDir(), "bin")
	bs, err := New(base, &fakeDB{}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	// Poison the parent dir by making it a file after construction.
	sourceTypeDir := filepath.Join(base, "imap")
	if f, err := os.Create(sourceTypeDir); err == nil {
		_ = f.Close()
	} else {
		t.Fatal(err)
	}

	err = bs.Put(context.Background(), "imap", "icloud", "x",
		bytesReaderOf("hello"), 5)
	if err == nil {
		t.Error("expected error when parent directory is not a directory")
	}
}
