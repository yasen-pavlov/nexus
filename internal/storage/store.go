// Package storage provides binary content caching for connector
// previews and downloads. Connectors with expensive or unreliable
// re-fetch (IMAP on a slow network, Telegram media with expiring URLs)
// populate the cache; connectors with local sources (filesystem,
// Paperless, Immich) skip it and re-fetch from source on every preview.
//
// Blobs live on the local filesystem under NEXUS_BINARY_STORE_PATH.
// Metadata (size, stored_at, last_accessed_at) is tracked in Postgres
// for efficient eviction queries and stats.
package storage

import (
	"context"
	"time"

	"github.com/muty/nexus/internal/model"
)

// CacheMode controls when and how a connector populates the cache.
type CacheMode string

const (
	// CacheModeNone disables caching — the connector always re-fetches
	// from the source. Use for connectors with always-available local
	// sources (filesystem, Paperless, Immich).
	CacheModeNone CacheMode = "none"

	// CacheModeLazy populates the cache on the first FetchBinary call
	// for each document. Subsequent calls read from the cache. Use for
	// connectors where re-fetch is reliable but slow (IMAP).
	CacheModeLazy CacheMode = "lazy"

	// CacheModeEager populates the cache during the sync's Fetch phase,
	// downloading every binary upfront. Use for connectors where
	// re-fetch is unreliable (Telegram media links expire on MTProto
	// servers so we must download before they're gone).
	CacheModeEager CacheMode = "eager"
)

// CacheConfig is the per-connector cache policy.
type CacheConfig struct {
	Mode    CacheMode     // none, lazy, or eager
	MaxAge  time.Duration // evict entries not accessed within this window; 0 = never evict
	MaxSize int64         // max total bytes for this connector type; 0 = unlimited
}

// DefaultCacheConfig returns the baseline per-source-type policy.
// Individual connector instances can override via the cache_mode /
// cache_max_age_days / cache_max_size_bytes keys in their config blob.
var DefaultCacheConfig = map[string]CacheConfig{
	"filesystem": {Mode: CacheModeNone},
	"paperless":  {Mode: CacheModeNone},
	"immich":     {Mode: CacheModeNone},
	"mealie":     {Mode: CacheModeNone},
	"imap":       {Mode: CacheModeLazy, MaxAge: 30 * 24 * time.Hour, MaxSize: 5 << 30},
	"telegram":   {Mode: CacheModeEager, MaxAge: 0, MaxSize: 10 << 30},
}

// ResolveCacheConfig returns the effective cache policy for a connector
// instance. It starts from the source-type default and applies any
// overrides present in the connector's JSONB config blob:
//
//	cache_mode              string   (none, lazy, eager)
//	cache_max_age_days      int      (0 = never evict)
//	cache_max_size_bytes    int64    (0 = unlimited)
//
// Unknown mode strings and negative numeric values are ignored with a
// silent fallback to the default — validation at the API level is left
// to the Settings UI when it lands.
func ResolveCacheConfig(sourceType string, cfg map[string]any) CacheConfig {
	out := DefaultCacheConfig[sourceType]
	// Unknown source type with no default — treat as no caching.
	// Connectors that don't implement CacheAware never receive this value.
	if out.Mode == "" {
		out.Mode = CacheModeNone
	}

	if v, ok := cfg["cache_mode"].(string); ok {
		switch CacheMode(v) {
		case CacheModeNone, CacheModeLazy, CacheModeEager:
			out.Mode = CacheMode(v)
		}
	}

	switch v := cfg["cache_max_age_days"].(type) {
	case float64:
		if v >= 0 {
			out.MaxAge = time.Duration(v) * 24 * time.Hour
		}
	case int:
		if v >= 0 {
			out.MaxAge = time.Duration(v) * 24 * time.Hour
		}
	case int64:
		if v >= 0 {
			out.MaxAge = time.Duration(v) * 24 * time.Hour
		}
	}

	switch v := cfg["cache_max_size_bytes"].(type) {
	case float64:
		if v >= 0 {
			out.MaxSize = int64(v)
		}
	case int:
		if v >= 0 {
			out.MaxSize = int64(v)
		}
	case int64:
		if v >= 0 {
			out.MaxSize = v
		}
	}

	return out
}

// StoreDB is the subset of internal/store methods the BinaryStore needs.
// Declared as an interface so tests can use a fake or a real store
// interchangeably, and so internal/storage doesn't take a hard dependency
// on the concrete Store type.
type StoreDB interface {
	UpsertBinaryStoreEntry(ctx context.Context, e *model.BinaryStoreEntry) error
	TouchBinaryStoreEntry(ctx context.Context, sourceType, sourceName, sourceID string) (bool, error)
	GetBinaryStoreEntry(ctx context.Context, sourceType, sourceName, sourceID string) (*model.BinaryStoreEntry, error)
	DeleteBinaryStoreEntry(ctx context.Context, sourceType, sourceName, sourceID string) (string, error)
	DeleteBinaryStoreBySource(ctx context.Context, sourceType, sourceName string) ([]string, error)
	DeleteAllBinaryStoreEntries(ctx context.Context) ([]string, error)
	ListExpiredBinaryStoreEntries(ctx context.Context, sourceType string, maxAge time.Duration) ([]model.BinaryStoreEntry, error)
	ListLRUBinaryStoreEntries(ctx context.Context, sourceType string) ([]model.BinaryStoreEntry, error)
	BinaryStoreTotalSize(ctx context.Context, sourceType string) (int64, error)
	BinaryStoreStats(ctx context.Context) ([]model.BinaryStoreStats, error)
}
