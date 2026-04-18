package connector

import (
	"time"
)

// ComputeSyncSince returns the earliest time to sync from based on config.
// Reads the `sync_since` key as YYYY-MM-DD. Returns zero time if absent or
// unparseable — connectors treat that as "sync full history".
//
// The legacy `sync_since_days` key was removed in Phase 3 (migration 013
// converts any existing rows in the database). Don't re-introduce it.
func ComputeSyncSince(cfg Config) time.Time {
	if since := cfg.StringVal("sync_since"); since != "" {
		if t, err := time.Parse("2006-01-02", since); err == nil {
			return t
		}
	}
	return time.Time{}
}

// StringVal returns the string value of a config key, or empty string.
func (c Config) StringVal(key string) string {
	v, _ := c[key].(string)
	return v
}
