package connector

import (
	"strconv"
	"time"
)

// ComputeSyncSince returns the earliest time to sync from based on config.
// sync_since_days takes precedence over sync_since if both are set.
// Returns zero time if no limit is configured.
func ComputeSyncSince(cfg Config) time.Time {
	if days, _ := strconv.Atoi(cfg.StringVal("sync_since_days")); days > 0 {
		return time.Now().AddDate(0, 0, -days)
	}
	if since := cfg.StringVal("sync_since"); since != "" {
		t, err := time.Parse("2006-01-02", since)
		if err == nil {
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
