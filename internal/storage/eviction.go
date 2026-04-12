package storage

import (
	"context"
	"errors"
	"os"
	"time"

	"go.uber.org/zap"
)

// RunEviction starts a background loop that enforces the configured
// per-source-type eviction policies. It runs immediately once, then
// every interval until ctx is canceled.
//
// Two reclamation passes per source type:
//
//  1. Expiration: delete entries where last_accessed_at is older than
//     the policy's MaxAge.
//  2. Size budget: if total cached bytes still exceed MaxSize, delete
//     entries in LRU order until under budget.
//
// Source types with MaxAge=0 and MaxSize=0 (e.g. Telegram) are never
// swept. Source types absent from the policy map are skipped entirely
// (no default — we'd rather not evict surprise-caches that some
// connector has started populating).
//
// Logs per-pass summaries (count + bytes freed). Individual file I/O
// errors are logged as warnings but don't abort the run.
func (s *BinaryStore) RunEviction(ctx context.Context, policies map[string]CacheConfig, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}

	s.evictOnce(ctx, policies)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.evictOnce(ctx, policies)
		}
	}
}

func (s *BinaryStore) evictOnce(ctx context.Context, policies map[string]CacheConfig) {
	for sourceType, pol := range policies {
		if pol.MaxAge == 0 && pol.MaxSize == 0 {
			continue
		}
		if pol.MaxAge > 0 {
			s.evictExpired(ctx, sourceType, pol.MaxAge)
		}
		if pol.MaxSize > 0 {
			s.evictOverBudget(ctx, sourceType, pol.MaxSize)
		}
	}
}

func (s *BinaryStore) evictExpired(ctx context.Context, sourceType string, maxAge time.Duration) {
	entries, err := s.db.ListExpiredBinaryStoreEntries(ctx, sourceType, maxAge)
	if err != nil {
		s.log.Warn("eviction: list expired failed", zap.String("source_type", sourceType), zap.Error(err))
		return
	}
	if len(entries) == 0 {
		return
	}

	var freed int64
	var count int
	for _, e := range entries {
		if err := s.Delete(ctx, e.SourceType, e.SourceName, e.SourceID); err != nil {
			s.log.Warn("eviction: delete expired failed",
				zap.String("source_type", e.SourceType),
				zap.String("source_name", e.SourceName),
				zap.String("source_id", e.SourceID),
				zap.Error(err),
			)
			continue
		}
		freed += e.Size
		count++
	}
	s.log.Info("eviction: expired entries reclaimed",
		zap.String("source_type", sourceType),
		zap.Duration("max_age", maxAge),
		zap.Int("count", count),
		zap.Int64("bytes_freed", freed),
	)
}

func (s *BinaryStore) evictOverBudget(ctx context.Context, sourceType string, maxSize int64) {
	total, err := s.db.BinaryStoreTotalSize(ctx, sourceType)
	if err != nil {
		s.log.Warn("eviction: total size failed", zap.String("source_type", sourceType), zap.Error(err))
		return
	}
	if total <= maxSize {
		return
	}

	entries, err := s.db.ListLRUBinaryStoreEntries(ctx, sourceType)
	if err != nil {
		s.log.Warn("eviction: list LRU failed", zap.String("source_type", sourceType), zap.Error(err))
		return
	}

	excess := total - maxSize
	var freed int64
	var count int
	for _, e := range entries {
		if freed >= excess {
			break
		}
		if err := s.Delete(ctx, e.SourceType, e.SourceName, e.SourceID); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// File vanished between listing and delete; still counts as progress.
				freed += e.Size
				count++
				continue
			}
			s.log.Warn("eviction: delete LRU failed",
				zap.String("source_type", e.SourceType),
				zap.String("source_name", e.SourceName),
				zap.String("source_id", e.SourceID),
				zap.Error(err),
			)
			continue
		}
		freed += e.Size
		count++
	}
	s.log.Info("eviction: LRU entries reclaimed",
		zap.String("source_type", sourceType),
		zap.Int64("max_size", maxSize),
		zap.Int64("before_total", total),
		zap.Int("count", count),
		zap.Int64("bytes_freed", freed),
	)
}
