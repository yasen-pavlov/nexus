package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// BinaryStore is a filesystem-backed blob store with metadata tracked
// in Postgres. See package docs for policy.
type BinaryStore struct {
	basePath string
	db       StoreDB
	log      *zap.Logger
}

// New constructs a BinaryStore. basePath is created on disk if it
// doesn't exist. Returns an error only if the directory cannot be
// created; missing permissions or read-only mounts surface here so
// the app fails fast at startup rather than on first Put.
func New(basePath string, db StoreDB, log *zap.Logger) (*BinaryStore, error) {
	if log == nil {
		log = zap.NewNop()
	}
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create base path %q: %w", basePath, err)
	}
	return &BinaryStore{basePath: basePath, db: db, log: log}, nil
}

// keyPath returns the on-disk path for a cached blob. Directory
// sharding by sourceType/sourceName keeps any single directory from
// growing without bound and makes `du -sh` per-connector easy. The
// SHA256 prefix of sourceID avoids filename issues (slashes, control
// chars in IMAP UIDs, Telegram message IDs with colons, etc.).
func (s *BinaryStore) keyPath(sourceType, sourceName, sourceID string) string {
	sum := sha256.Sum256([]byte(sourceID))
	return filepath.Join(s.basePath, sourceType, sourceName, hex.EncodeToString(sum[:8])+".bin")
}

// Put writes a binary blob to disk and upserts its metadata row. The
// write is atomic: content goes to a temp file first, then renamed
// into place, so a crash mid-write never leaves a corrupted cached
// entry.
//
// If size is 0 (unknown), it's computed from the bytes actually
// written. The metadata row's size reflects the final blob size.
func (s *BinaryStore) Put(ctx context.Context, sourceType, sourceName, sourceID string, r io.Reader, size int64) error {
	dst := s.keyPath(sourceType, sourceName, sourceID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("storage: mkdir %q: %w", filepath.Dir(dst), err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".bin-*")
	if err != nil {
		return fmt.Errorf("storage: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup on error paths.
	defer func() {
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	written, err := io.Copy(tmp, r)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("storage: write blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("storage: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("storage: rename blob into place: %w", err)
	}

	finalSize := size
	if finalSize <= 0 {
		finalSize = written
	}

	if err := s.db.UpsertBinaryStoreEntry(ctx, &model.BinaryStoreEntry{
		SourceType: sourceType,
		SourceName: sourceName,
		SourceID:   sourceID,
		FilePath:   dst,
		Size:       finalSize,
	}); err != nil {
		// Metadata row failed but blob is written. Roll back the blob
		// so we don't leak orphaned files. A retry will succeed cleanly.
		_ = os.Remove(dst)
		return fmt.Errorf("storage: record metadata: %w", err)
	}
	return nil
}

// Get opens a cached blob for reading. Returns os.ErrNotExist when the
// entry is not cached. Updates last_accessed_at so eviction uses
// accurate LRU information.
//
// The caller must Close() the returned reader.
func (s *BinaryStore) Get(ctx context.Context, sourceType, sourceName, sourceID string) (io.ReadCloser, error) {
	ok, err := s.db.TouchBinaryStoreEntry(ctx, sourceType, sourceName, sourceID)
	if err != nil {
		return nil, fmt.Errorf("storage: touch entry: %w", err)
	}
	if !ok {
		return nil, os.ErrNotExist
	}

	f, err := os.Open(s.keyPath(sourceType, sourceName, sourceID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Metadata row exists but file is gone (manual deletion,
			// volume corruption, etc.). Clean up the stale row so
			// future Gets don't keep lying about existence.
			_, _ = s.db.DeleteBinaryStoreEntry(ctx, sourceType, sourceName, sourceID)
		}
		return nil, fmt.Errorf("storage: open blob: %w", err)
	}
	return f, nil
}

// Exists reports whether a blob is cached without opening it or
// updating last_accessed_at. Useful for eager connectors checking
// whether to skip re-downloading during sync.
func (s *BinaryStore) Exists(ctx context.Context, sourceType, sourceName, sourceID string) (bool, error) {
	_, err := s.db.GetBinaryStoreEntry(ctx, sourceType, sourceName, sourceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Delete removes a single cached entry. Missing entries are a no-op
// (deleting what isn't there is not an error).
func (s *BinaryStore) Delete(ctx context.Context, sourceType, sourceName, sourceID string) error {
	path, err := s.db.DeleteBinaryStoreEntry(ctx, sourceType, sourceName, sourceID)
	if err != nil {
		return fmt.Errorf("storage: delete entry: %w", err)
	}
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: remove blob %q: %w", path, err)
	}
	return nil
}

// DeleteBySource removes every cached entry for a given connector
// (source_type + source_name). Called when a connector is removed so
// its cached binaries don't linger. Mirrors search.DeleteBySource.
//
// Logs per-file removal errors but continues — the DB rows are already
// gone, so returning early on an I/O error would leave orphaned files
// with no way to find them later.
func (s *BinaryStore) DeleteBySource(ctx context.Context, sourceType, sourceName string) error {
	paths, err := s.db.DeleteBinaryStoreBySource(ctx, sourceType, sourceName)
	if err != nil {
		return fmt.Errorf("storage: delete by source: %w", err)
	}
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("failed to remove cached blob",
				zap.String("path", p),
				zap.String("source_type", sourceType),
				zap.String("source_name", sourceName),
				zap.Error(err),
			)
		}
	}
	// Best-effort: remove the now-empty source-name directory so the
	// directory listing doesn't accumulate stubs across connector
	// lifetimes. Silently ignored if it's not actually empty.
	_ = os.Remove(filepath.Join(s.basePath, sourceType, sourceName))
	return nil
}

// Stats returns per-source-type/name cache aggregates for the admin
// stats endpoint.
func (s *BinaryStore) Stats(ctx context.Context) ([]model.BinaryStoreStats, error) {
	stats, err := s.db.BinaryStoreStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: stats: %w", err)
	}
	return stats, nil
}

// DeleteAll removes every cached entry across all connectors. Used by
// the admin "wipe cache" endpoint. Like DeleteBySource, logs per-file
// removal errors but continues so one bad file doesn't block cleanup
// of the rest.
//
// After every row is deleted, walks the base directory to prune empty
// source_type / source_name subdirectories left behind.
func (s *BinaryStore) DeleteAll(ctx context.Context) error {
	paths, err := s.db.DeleteAllBinaryStoreEntries(ctx)
	if err != nil {
		return fmt.Errorf("storage: delete all: %w", err)
	}
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("failed to remove cached blob", zap.String("path", p), zap.Error(err))
		}
	}
	// Prune empty directories left behind. filepath.Walk visits in
	// lexical pre-order, but we need post-order so children are removed
	// before their parents — otherwise os.Remove on a still-populated
	// parent silently fails and the parent lingers forever. Collect
	// directory paths first, then remove them deepest-first.
	var dirs []string
	_ = filepath.Walk(s.basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || path == s.basePath {
			return nil
		}
		dirs = append(dirs, path)
		return nil
	})
	// Longest paths first = deepest first. os.Remove on a non-empty dir
	// still silently fails, which is what we want for safety.
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = os.Remove(d)
	}
	return nil
}
