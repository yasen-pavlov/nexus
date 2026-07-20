package storage

import (
	"testing"
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

func TestResolveCacheConfig_MaxAgeMaxSizeFromDefaultOnly(t *testing.T) {
	// MaxAge / MaxSize are not overridable per connector — they always come
	// from the source-type default, regardless of what the config blob carries.
	got := ResolveCacheConfig("imap", map[string]any{
		"cache_max_age_days":   float64(60),
		"cache_max_size_bytes": float64(2 << 30),
	})
	defaults := DefaultCacheConfig["imap"]
	if got.MaxAge != defaults.MaxAge {
		t.Errorf("MaxAge = %v, want default %v (not overridable)", got.MaxAge, defaults.MaxAge)
	}
	if got.MaxSize != defaults.MaxSize {
		t.Errorf("MaxSize = %d, want default %d (not overridable)", got.MaxSize, defaults.MaxSize)
	}
}

func TestResolveCacheConfig_IgnoresWrongTypeMode(t *testing.T) {
	// A non-string cache_mode is ignored, leaving the source-type default.
	got := ResolveCacheConfig("imap", map[string]any{"cache_mode": 123})
	defaults := DefaultCacheConfig["imap"]
	if got != defaults {
		t.Errorf("got %+v, want defaults %+v when cache_mode is wrong type", got, defaults)
	}
}
