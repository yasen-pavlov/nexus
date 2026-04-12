//go:build integration

package store

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

func entry(sourceType, sourceName, sourceID, filePath string, size int64) *model.BinaryStoreEntry {
	return &model.BinaryStoreEntry{
		SourceType: sourceType,
		SourceName: sourceName,
		SourceID:   sourceID,
		FilePath:   filePath,
		Size:       size,
	}
}

func TestUpsertBinaryStoreEntry_Insert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := entry("imap", "icloud", "msg-1", "/data/binaries/imap/icloud/abc.bin", 2048)
	if err := s.UpsertBinaryStoreEntry(ctx, e); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetBinaryStoreEntry(ctx, "imap", "icloud", "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.FilePath != e.FilePath || got.Size != e.Size {
		t.Errorf("got %+v, want %+v", got, e)
	}
	if got.StoredAt.IsZero() || got.LastAccessedAt.IsZero() {
		t.Error("timestamps not set by DEFAULT NOW()")
	}
}

func TestUpsertBinaryStoreEntry_ReplacesRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", "msg-1", "/old", 100)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", "msg-1", "/new", 500)); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetBinaryStoreEntry(ctx, "imap", "icloud", "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.FilePath != "/new" || got.Size != 500 {
		t.Errorf("got %+v, want file_path=/new size=500", got)
	}
}

func TestGetBinaryStoreEntry_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.GetBinaryStoreEntry(ctx, "imap", "icloud", "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestTouchBinaryStoreEntry_UpdatesTimestamp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", "msg-1", "/p", 100)); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetBinaryStoreEntry(ctx, "imap", "icloud", "msg-1")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond)
	ok, err := s.TouchBinaryStoreEntry(ctx, "imap", "icloud", "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected touch to affect a row")
	}

	after, err := s.GetBinaryStoreEntry(ctx, "imap", "icloud", "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if !after.LastAccessedAt.After(before.LastAccessedAt) {
		t.Errorf("last_accessed_at not advanced: before=%v after=%v", before.LastAccessedAt, after.LastAccessedAt)
	}
}

func TestTouchBinaryStoreEntry_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ok, err := s.TouchBinaryStoreEntry(ctx, "imap", "icloud", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected false for missing entry")
	}
}

func TestDeleteBinaryStoreEntry_ReturnsFilePath(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", "msg-1", "/x/y/z", 100)); err != nil {
		t.Fatal(err)
	}

	path, err := s.DeleteBinaryStoreEntry(ctx, "imap", "icloud", "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/x/y/z" {
		t.Errorf("got %q, want /x/y/z", path)
	}

	if _, err := s.GetBinaryStoreEntry(ctx, "imap", "icloud", "msg-1"); !errors.Is(err, ErrNotFound) {
		t.Error("entry still exists after delete")
	}
}

func TestDeleteBinaryStoreEntry_Missing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	path, err := s.DeleteBinaryStoreEntry(ctx, "imap", "icloud", "nope")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("expected empty path for missing entry, got %q", path)
	}
}

func TestDeleteBinaryStoreBySource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i, id := range []string{"a", "b", "c"} {
		if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", id, "/f/"+id, int64(100*(i+1)))); err != nil {
			t.Fatal(err)
		}
	}
	// Different connector — should not be touched.
	if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "work", "z", "/other", 999)); err != nil {
		t.Fatal(err)
	}

	paths, err := s.DeleteBinaryStoreBySource(ctx, "imap", "icloud")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("got %d paths, want 3: %v", len(paths), paths)
	}
	sort.Strings(paths)
	want := []string{"/f/a", "/f/b", "/f/c"}
	for i, p := range paths {
		if p != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, p, want[i])
		}
	}

	// "work" entry should still exist.
	if _, err := s.GetBinaryStoreEntry(ctx, "imap", "work", "z"); err != nil {
		t.Errorf("work entry was deleted: %v", err)
	}
}

func TestListExpiredBinaryStoreEntries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Three imap entries, one telegram entry (should never be in results).
	if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", "old", "/o", 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", "new", "/n", 20)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBinaryStoreEntry(ctx, entry("telegram", "personal", "tg", "/t", 30)); err != nil {
		t.Fatal(err)
	}

	// Backdate the "old" entry.
	if _, err := s.pool.Exec(ctx,
		`UPDATE binary_store_entries SET last_accessed_at = NOW() - INTERVAL '100 days'
		 WHERE source_id = 'old'`); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListExpiredBinaryStoreEntries(ctx, "imap", 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != "old" {
		t.Errorf("got %+v, want one entry with source_id=old", got)
	}
}

func TestListLRUBinaryStoreEntries_OrderedByAccess(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Insert three entries with staggered access times so the ordering is
	// deterministic (relying on NOW() alone produces near-identical timestamps).
	if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", "a", "/a", 100)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", "b", "/b", 100)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", "c", "/c", 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE binary_store_entries SET last_accessed_at = NOW() - INTERVAL '3 days' WHERE source_id = 'a';
		 UPDATE binary_store_entries SET last_accessed_at = NOW() - INTERVAL '2 days' WHERE source_id = 'b';
		 UPDATE binary_store_entries SET last_accessed_at = NOW() - INTERVAL '1 days' WHERE source_id = 'c';`); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListLRUBinaryStoreEntries(ctx, "imap")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	want := []string{"a", "b", "c"} // oldest first
	for i, e := range got {
		if e.SourceID != want[i] {
			t.Errorf("got[%d].SourceID = %q, want %q", i, e.SourceID, want[i])
		}
	}
}

func TestBinaryStoreTotalSize(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i, id := range []string{"a", "b", "c"} {
		if err := s.UpsertBinaryStoreEntry(ctx, entry("imap", "icloud", id, "/"+id, int64(100*(i+1)))); err != nil {
			t.Fatal(err)
		}
	}
	total, err := s.BinaryStoreTotalSize(ctx, "imap")
	if err != nil {
		t.Fatal(err)
	}
	if total != 100+200+300 {
		t.Errorf("got %d, want 600", total)
	}

	empty, err := s.BinaryStoreTotalSize(ctx, "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if empty != 0 {
		t.Errorf("empty source got %d, want 0", empty)
	}
}

func TestDeleteAllBinaryStoreEntries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, e := range []*model.BinaryStoreEntry{
		entry("imap", "icloud", "a", "/x/a", 100),
		entry("imap", "icloud", "b", "/x/b", 200),
		entry("telegram", "personal", "c", "/x/c", 500),
	} {
		if err := s.UpsertBinaryStoreEntry(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := s.DeleteAllBinaryStoreEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Errorf("got %d paths, want 3: %v", len(paths), paths)
	}

	stats, err := s.BinaryStoreStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Errorf("expected empty stats after DeleteAll, got %+v", stats)
	}
}

func TestDeleteAllBinaryStoreEntries_EmptyTable(t *testing.T) {
	s := newTestStore(t)
	paths, err := s.DeleteAllBinaryStoreEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Errorf("expected no paths on empty table, got %d", len(paths))
	}
}

func TestBinaryStoreStats_AggregatesBySource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Mix of connectors.
	entries := []*model.BinaryStoreEntry{
		entry("imap", "icloud", "a", "/a", 100),
		entry("imap", "icloud", "b", "/b", 200),
		entry("imap", "work", "c", "/c", 500),
		entry("telegram", "personal", "d", "/d", 1000),
	}
	for _, e := range entries {
		if err := s.UpsertBinaryStoreEntry(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := s.BinaryStoreStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 3 {
		t.Fatalf("got %d rows, want 3 grouped by (type, name): %+v", len(stats), stats)
	}

	index := make(map[string]model.BinaryStoreStats)
	for _, st := range stats {
		index[st.SourceType+"/"+st.SourceName] = st
	}
	if got := index["imap/icloud"]; got.Count != 2 || got.TotalSize != 300 {
		t.Errorf("imap/icloud: got %+v, want count=2 size=300", got)
	}
	if got := index["imap/work"]; got.Count != 1 || got.TotalSize != 500 {
		t.Errorf("imap/work: got %+v, want count=1 size=500", got)
	}
	if got := index["telegram/personal"]; got.Count != 1 || got.TotalSize != 1000 {
		t.Errorf("telegram/personal: got %+v, want count=1 size=1000", got)
	}
}
