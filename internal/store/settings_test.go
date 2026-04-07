//go:build integration

package store

import (
	"context"
	"testing"
)

func TestGetSetting_NotFound(t *testing.T) {
	st := newTestStore(t)
	val, err := st.GetSetting(context.Background(), "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("expected empty string, got %q", val)
	}
}

func TestSetAndGetSetting(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.SetSetting(ctx, "test_key", "test_value"); err != nil {
		t.Fatal(err)
	}

	val, err := st.GetSetting(ctx, "test_key")
	if err != nil {
		t.Fatal(err)
	}
	if val != "test_value" {
		t.Errorf("expected 'test_value', got %q", val)
	}

	// Upsert
	if err := st.SetSetting(ctx, "test_key", "updated"); err != nil {
		t.Fatal(err)
	}
	val, err = st.GetSetting(ctx, "test_key")
	if err != nil {
		t.Fatal(err)
	}
	if val != "updated" {
		t.Errorf("expected 'updated', got %q", val)
	}
}

func TestGetSettings_Batch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	st.SetSetting(ctx, "a", "1") //nolint:errcheck // test
	st.SetSetting(ctx, "b", "2") //nolint:errcheck // test

	result, err := st.GetSettings(ctx, []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if result["a"] != "1" {
		t.Errorf("expected a=1, got %q", result["a"])
	}
	if result["b"] != "2" {
		t.Errorf("expected b=2, got %q", result["b"])
	}
	if _, ok := result["c"]; ok {
		t.Error("expected c to be missing")
	}
}

func TestSetSettings_Batch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	err := st.SetSettings(ctx, map[string]string{"x": "10", "y": "20"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := st.GetSettings(ctx, []string{"x", "y"})
	if err != nil {
		t.Fatal(err)
	}
	if result["x"] != "10" || result["y"] != "20" {
		t.Errorf("unexpected: %v", result)
	}
}
