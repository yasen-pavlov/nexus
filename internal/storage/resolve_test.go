package storage

import (
	"testing"
	"time"
)

func TestResolveCacheConfig_DefaultForKnownType(t *testing.T) {
	got := ResolveCacheConfig("imap", nil)
	want := DefaultCacheConfig["imap"]
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestResolveCacheConfig_NoneForUnknownType(t *testing.T) {
	got := ResolveCacheConfig("bogus-source", nil)
	if got.Mode != CacheModeNone {
		t.Errorf("Mode = %q, want none for unknown source type", got.Mode)
	}
}

func TestResolveCacheConfig_OverridesMode(t *testing.T) {
	got := ResolveCacheConfig("imap", map[string]any{"cache_mode": "eager"})
	if got.Mode != CacheModeEager {
		t.Errorf("Mode = %q, want eager", got.Mode)
	}
}

func TestResolveCacheConfig_IgnoresUnknownMode(t *testing.T) {
	got := ResolveCacheConfig("imap", map[string]any{"cache_mode": "aggressive"})
	if got.Mode != DefaultCacheConfig["imap"].Mode {
		t.Errorf("unknown mode should fall back to default, got %q", got.Mode)
	}
}

func TestResolveCacheConfig_OverridesMaxAgeDays(t *testing.T) {
	got := ResolveCacheConfig("imap", map[string]any{"cache_max_age_days": float64(60)})
	if got.MaxAge != 60*24*time.Hour {
		t.Errorf("MaxAge = %v, want 60d", got.MaxAge)
	}
}

func TestResolveCacheConfig_OverridesMaxAgeDaysInt(t *testing.T) {
	got := ResolveCacheConfig("imap", map[string]any{"cache_max_age_days": 45})
	if got.MaxAge != 45*24*time.Hour {
		t.Errorf("MaxAge = %v, want 45d (int typed)", got.MaxAge)
	}
}

func TestResolveCacheConfig_IgnoresNegativeMaxAge(t *testing.T) {
	got := ResolveCacheConfig("imap", map[string]any{"cache_max_age_days": float64(-5)})
	if got.MaxAge != DefaultCacheConfig["imap"].MaxAge {
		t.Error("negative max age should fall back to default")
	}
}

func TestResolveCacheConfig_OverridesMaxSizeBytes(t *testing.T) {
	got := ResolveCacheConfig("telegram", map[string]any{"cache_max_size_bytes": float64(2 << 30)})
	if got.MaxSize != 2<<30 {
		t.Errorf("MaxSize = %d, want %d", got.MaxSize, int64(2<<30))
	}
}

func TestResolveCacheConfig_ZeroDisablesEviction(t *testing.T) {
	got := ResolveCacheConfig("imap", map[string]any{"cache_max_age_days": float64(0)})
	if got.MaxAge != 0 {
		t.Errorf("MaxAge = %v, want 0 (disables expiration)", got.MaxAge)
	}
}

func TestResolveCacheConfig_MaxAgeInt64(t *testing.T) {
	got := ResolveCacheConfig("imap", map[string]any{"cache_max_age_days": int64(7)})
	if got.MaxAge != 7*24*time.Hour {
		t.Errorf("MaxAge = %v, want 7d (int64 typed)", got.MaxAge)
	}
}

func TestResolveCacheConfig_MaxSizeInt(t *testing.T) {
	got := ResolveCacheConfig("telegram", map[string]any{"cache_max_size_bytes": 1024})
	if got.MaxSize != 1024 {
		t.Errorf("MaxSize = %d, want 1024 (int typed)", got.MaxSize)
	}
}

func TestResolveCacheConfig_MaxSizeInt64(t *testing.T) {
	got := ResolveCacheConfig("telegram", map[string]any{"cache_max_size_bytes": int64(2048)})
	if got.MaxSize != 2048 {
		t.Errorf("MaxSize = %d, want 2048 (int64 typed)", got.MaxSize)
	}
}

func TestResolveCacheConfig_IgnoresNegativeMaxSize(t *testing.T) {
	got := ResolveCacheConfig("telegram", map[string]any{"cache_max_size_bytes": float64(-100)})
	if got.MaxSize != DefaultCacheConfig["telegram"].MaxSize {
		t.Error("negative max size should fall back to default")
	}
}

func TestResolveCacheConfig_IgnoresWrongTypes(t *testing.T) {
	// Non-numeric / non-string values should be ignored, leaving defaults.
	got := ResolveCacheConfig("imap", map[string]any{
		"cache_mode":           123,
		"cache_max_age_days":   "thirty",
		"cache_max_size_bytes": true,
	})
	defaults := DefaultCacheConfig["imap"]
	if got != defaults {
		t.Errorf("got %+v, want defaults %+v when overrides are wrong types", got, defaults)
	}
}
