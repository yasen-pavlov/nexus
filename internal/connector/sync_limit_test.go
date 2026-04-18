package connector

import (
	"testing"
	"time"
)

func TestComputeSyncSince_Empty(t *testing.T) {
	result := ComputeSyncSince(Config{})
	if !result.IsZero() {
		t.Errorf("expected zero time, got %v", result)
	}
}

func TestComputeSyncSince_Date(t *testing.T) {
	result := ComputeSyncSince(Config{"sync_since": "2025-06-01"})
	expected := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestComputeSyncSince_InvalidDate(t *testing.T) {
	result := ComputeSyncSince(Config{"sync_since": "not-a-date"})
	if !result.IsZero() {
		t.Errorf("expected zero for invalid date, got %v", result)
	}
}

// The legacy `sync_since_days` key is no longer honored. If a row
// somehow still has it (pre-migration), we ignore it rather than
// silently take precedence over a proper `sync_since`.
func TestComputeSyncSince_LegacyDaysIgnored(t *testing.T) {
	result := ComputeSyncSince(Config{"sync_since_days": "7"})
	if !result.IsZero() {
		t.Errorf("expected zero (legacy key ignored), got %v", result)
	}
}

func TestStringVal(t *testing.T) {
	cfg := Config{"key": "value", "num": 42}
	if cfg.StringVal("key") != "value" {
		t.Errorf("expected 'value'")
	}
	if cfg.StringVal("num") != "" {
		t.Errorf("expected empty for non-string")
	}
	if cfg.StringVal("missing") != "" {
		t.Errorf("expected empty for missing")
	}
}
