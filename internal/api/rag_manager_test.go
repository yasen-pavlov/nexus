//go:build integration

package api

import (
	"context"
	"strconv"
	"testing"

	"go.uber.org/zap"
)

func TestRAGManager_LoadFromDB_EmptyFallsBackToDefault(t *testing.T) {
	st, _, _ := newTestDeps(t)
	mgr := NewRAGManager(st, zap.NewNop())
	if err := mgr.LoadFromDB(context.Background(), nil); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	if got := mgr.MaxToolRounds(); got != defaultMaxToolRounds {
		t.Errorf("MaxToolRounds = %d, want default %d", got, defaultMaxToolRounds)
	}
}

func TestRAGManager_LoadFromDB_OverlaysPersistedValue(t *testing.T) {
	st, _, _ := newTestDeps(t)
	ctx := context.Background()
	if err := st.SetSettings(ctx, map[string]string{ragKeyMaxToolRounds: "4"}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	mgr := NewRAGManager(st, zap.NewNop())
	if err := mgr.LoadFromDB(ctx, nil); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	if got := mgr.MaxToolRounds(); got != 4 {
		t.Errorf("MaxToolRounds = %d, want 4", got)
	}
}

// Corrupt persisted values must not take down boot — fall back to default
// and log. Mirrors the RankingManager behaviour for malformed JSON rows.
func TestRAGManager_LoadFromDB_IgnoresInvalidPersistedValue(t *testing.T) {
	st, _, _ := newTestDeps(t)
	ctx := context.Background()
	for _, raw := range []string{"not-a-number", "-1", strconv.Itoa(maxAllowedToolRounds + 1)} {
		if err := st.SetSettings(ctx, map[string]string{ragKeyMaxToolRounds: raw}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		mgr := NewRAGManager(st, zap.NewNop())
		if err := mgr.LoadFromDB(ctx, nil); err != nil {
			t.Fatalf("LoadFromDB(%q): %v", raw, err)
		}
		if got := mgr.MaxToolRounds(); got != defaultMaxToolRounds {
			t.Errorf("MaxToolRounds for raw=%q = %d, want default %d", raw, got, defaultMaxToolRounds)
		}
	}
}

func TestRAGManager_UpdateFromSettings_PersistsAndHotSwaps(t *testing.T) {
	st, _, _ := newTestDeps(t)
	ctx := context.Background()
	mgr := NewRAGManager(st, zap.NewNop())

	if err := mgr.UpdateFromSettings(ctx, RAGSnapshot{MaxToolRounds: 1}); err != nil {
		t.Fatalf("UpdateFromSettings: %v", err)
	}
	if got := mgr.MaxToolRounds(); got != 1 {
		t.Errorf("in-memory MaxToolRounds = %d, want 1", got)
	}
	// A fresh manager loading from the same store should see the value.
	fresh := NewRAGManager(st, zap.NewNop())
	if err := fresh.LoadFromDB(ctx, nil); err != nil {
		t.Fatalf("fresh LoadFromDB: %v", err)
	}
	if got := fresh.MaxToolRounds(); got != 1 {
		t.Errorf("persisted MaxToolRounds = %d, want 1", got)
	}
}

func TestRAGManager_UpdateFromSettings_RejectsOutOfRange(t *testing.T) {
	st, _, _ := newTestDeps(t)
	mgr := NewRAGManager(st, zap.NewNop())
	for _, n := range []int{-1, maxAllowedToolRounds + 1, 100} {
		err := mgr.UpdateFromSettings(context.Background(), RAGSnapshot{MaxToolRounds: n})
		if err == nil {
			t.Errorf("UpdateFromSettings(%d): expected error, got nil", n)
		}
	}
}

func TestRAGManager_UpdateFromSettings_AcceptsZero(t *testing.T) {
	st, _, _ := newTestDeps(t)
	mgr := NewRAGManager(st, zap.NewNop())
	if err := mgr.UpdateFromSettings(context.Background(), RAGSnapshot{MaxToolRounds: 0}); err != nil {
		t.Fatalf("UpdateFromSettings(0): %v", err)
	}
	if got := mgr.MaxToolRounds(); got != 0 {
		t.Errorf("MaxToolRounds = %d, want 0", got)
	}
}

func TestRAGManager_LoadFromDB_StoreErrorPropagates(t *testing.T) {
	st, _, _ := newTestDeps(t)
	mgr := NewRAGManager(st, zap.NewNop())
	st.Close()
	if err := mgr.LoadFromDB(context.Background(), nil); err == nil {
		t.Error("expected error when store is closed, got nil")
	}
}
