package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/muty/nexus/internal/model"
)

// UpsertBinaryStoreEntry inserts or replaces a cached-binary metadata row.
// Called after the underlying blob has been written to disk.
func (s *Store) UpsertBinaryStoreEntry(ctx context.Context, e *model.BinaryStoreEntry) error {
	query := `
		INSERT INTO binary_store_entries (source_type, source_name, source_id, file_path, size, stored_at, last_accessed_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		ON CONFLICT (source_type, source_name, source_id)
		DO UPDATE SET
			file_path = EXCLUDED.file_path,
			size = EXCLUDED.size,
			stored_at = NOW(),
			last_accessed_at = NOW()
	`
	_, err := s.pool.Exec(ctx, query,
		e.SourceType, e.SourceName, e.SourceID, e.FilePath, e.Size,
	)
	if err != nil {
		return fmt.Errorf("store: upsert binary store entry: %w", err)
	}
	return nil
}

// TouchBinaryStoreEntry updates last_accessed_at to NOW() for a cached entry.
// Called on every Get so eviction uses accurate LRU timestamps. Returns
// whether a row was updated — false means the entry is not cached.
func (s *Store) TouchBinaryStoreEntry(ctx context.Context, sourceType, sourceName, sourceID string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE binary_store_entries SET last_accessed_at = NOW()
		 WHERE source_type = $1 AND source_name = $2 AND source_id = $3`,
		sourceType, sourceName, sourceID,
	)
	if err != nil {
		return false, fmt.Errorf("store: touch binary store entry: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// GetBinaryStoreEntry returns the metadata row for a cached entry or
// ErrNotFound if absent.
func (s *Store) GetBinaryStoreEntry(ctx context.Context, sourceType, sourceName, sourceID string) (*model.BinaryStoreEntry, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT source_type, source_name, source_id, file_path, size, stored_at, last_accessed_at
		 FROM binary_store_entries
		 WHERE source_type = $1 AND source_name = $2 AND source_id = $3`,
		sourceType, sourceName, sourceID,
	)
	var e model.BinaryStoreEntry
	if err := row.Scan(&e.SourceType, &e.SourceName, &e.SourceID, &e.FilePath, &e.Size, &e.StoredAt, &e.LastAccessedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get binary store entry: %w", err)
	}
	return &e, nil
}

// DeleteBinaryStoreEntry removes a single entry and returns its file_path
// so the caller can delete the blob on disk. Returns ("", nil) if the
// entry didn't exist.
func (s *Store) DeleteBinaryStoreEntry(ctx context.Context, sourceType, sourceName, sourceID string) (string, error) {
	var filePath string
	err := s.pool.QueryRow(ctx,
		`DELETE FROM binary_store_entries
		 WHERE source_type = $1 AND source_name = $2 AND source_id = $3
		 RETURNING file_path`,
		sourceType, sourceName, sourceID,
	).Scan(&filePath)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("store: delete binary store entry: %w", err)
	}
	return filePath, nil
}

// DeleteAllBinaryStoreEntries removes every entry and returns the list
// of file paths so the caller can delete the blobs on disk. Used by the
// admin "wipe cache" endpoint; normal eviction uses targeted deletes.
func (s *Store) DeleteAllBinaryStoreEntries(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`DELETE FROM binary_store_entries RETURNING file_path`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: delete all binary store entries: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("store: scan deleted path: %w", err)
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate deleted paths: %w", err)
	}
	return paths, nil
}

// DeleteBinaryStoreBySource removes all entries for a connector (source_type
// + source_name) and returns the list of file_paths so the caller can
// delete the blobs on disk. Mirrors search.DeleteBySource pattern.
func (s *Store) DeleteBinaryStoreBySource(ctx context.Context, sourceType, sourceName string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`DELETE FROM binary_store_entries
		 WHERE source_type = $1 AND source_name = $2
		 RETURNING file_path`,
		sourceType, sourceName,
	)
	if err != nil {
		return nil, fmt.Errorf("store: delete binary store by source: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("store: scan deleted path: %w", err)
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate deleted paths: %w", err)
	}
	return paths, nil
}

// ListExpiredBinaryStoreEntries returns entries for sourceType whose
// last_accessed_at is older than (now - maxAge). The eviction goroutine
// uses this to find entries past the MaxAge policy.
func (s *Store) ListExpiredBinaryStoreEntries(ctx context.Context, sourceType string, maxAge time.Duration) ([]model.BinaryStoreEntry, error) {
	cutoff := time.Now().Add(-maxAge)
	rows, err := s.pool.Query(ctx,
		`SELECT source_type, source_name, source_id, file_path, size, stored_at, last_accessed_at
		 FROM binary_store_entries
		 WHERE source_type = $1 AND last_accessed_at < $2
		 ORDER BY last_accessed_at ASC`,
		sourceType, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list expired binary store entries: %w", err)
	}
	defer rows.Close()

	return scanBinaryStoreEntries(rows)
}

// ListLRUBinaryStoreEntries returns entries for sourceType ordered by
// last_accessed_at ASC (oldest first). Used by the eviction goroutine to
// reclaim space when a source exceeds its MaxSize budget — the caller
// iterates and deletes until the reclaimed size covers the excess.
func (s *Store) ListLRUBinaryStoreEntries(ctx context.Context, sourceType string) ([]model.BinaryStoreEntry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT source_type, source_name, source_id, file_path, size, stored_at, last_accessed_at
		 FROM binary_store_entries
		 WHERE source_type = $1
		 ORDER BY last_accessed_at ASC`,
		sourceType,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list LRU binary store entries: %w", err)
	}
	defer rows.Close()

	return scanBinaryStoreEntries(rows)
}

// BinaryStoreTotalSize returns the sum of sizes for all entries of the
// given sourceType. Used by the eviction goroutine to decide whether the
// source exceeds its MaxSize budget.
func (s *Store) BinaryStoreTotalSize(ctx context.Context, sourceType string) (int64, error) {
	var total int64
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(size), 0) FROM binary_store_entries WHERE source_type = $1`,
		sourceType,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("store: binary store total size: %w", err)
	}
	return total, nil
}

// BinaryStoreStats returns per-(source_type, source_name) aggregated
// counts and sizes. Used by the admin stats endpoint.
func (s *Store) BinaryStoreStats(ctx context.Context) ([]model.BinaryStoreStats, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT source_type, source_name, COUNT(*), COALESCE(SUM(size), 0)
		 FROM binary_store_entries
		 GROUP BY source_type, source_name
		 ORDER BY source_type, source_name`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: binary store stats: %w", err)
	}
	defer rows.Close()

	var stats []model.BinaryStoreStats
	for rows.Next() {
		var st model.BinaryStoreStats
		if err := rows.Scan(&st.SourceType, &st.SourceName, &st.Count, &st.TotalSize); err != nil {
			return nil, fmt.Errorf("store: scan stats row: %w", err)
		}
		stats = append(stats, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate stats rows: %w", err)
	}
	return stats, nil
}

// scanBinaryStoreEntries reads rows into a slice. Used by both ListExpired
// and ListLRU which share the same projection.
func scanBinaryStoreEntries(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]model.BinaryStoreEntry, error) {
	var out []model.BinaryStoreEntry
	for rows.Next() {
		var e model.BinaryStoreEntry
		if err := rows.Scan(&e.SourceType, &e.SourceName, &e.SourceID, &e.FilePath, &e.Size, &e.StoredAt, &e.LastAccessedAt); err != nil {
			return nil, fmt.Errorf("store: scan binary store entry: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate binary store entries: %w", err)
	}
	return out, nil
}
