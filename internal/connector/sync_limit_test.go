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

func TestComputeSyncSince_Days(t *testing.T) {
	result := ComputeSyncSince(Config{"sync_since_days": "30"})
	expected := time.Now().AddDate(0, 0, -30)
	diff := result.Sub(expected)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("expected ~30 days ago, got %v (diff %v)", result, diff)
	}
}

func TestComputeSyncSince_Date(t *testing.T) {
	result := ComputeSyncSince(Config{"sync_since": "2025-06-01"})
	expected := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if !result.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestComputeSyncSince_DaysTakesPrecedence(t *testing.T) {
	result := ComputeSyncSince(Config{
		"sync_since_days": "7",
		"sync_since":      "2020-01-01",
	})
	// Should use days, not the date
	expected := time.Now().AddDate(0, 0, -7)
	diff := result.Sub(expected)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("expected ~7 days ago, got %v", result)
	}
}

func TestComputeSyncSince_InvalidDate(t *testing.T) {
	result := ComputeSyncSince(Config{"sync_since": "not-a-date"})
	if !result.IsZero() {
		t.Errorf("expected zero for invalid date, got %v", result)
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
