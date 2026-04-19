//go:build integration

package api

import (
	"context"
	"testing"

	"github.com/muty/nexus/internal/search"
	"go.uber.org/zap"
)

func TestRankingManager_LoadFromDB_EmptyFallsBackToDefaults(t *testing.T) {
	st, _, _ := newTestDeps(t)
	mgr := NewRankingManager(st, zap.NewNop())
	if err := mgr.LoadFromDB(context.Background()); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	def := search.DefaultRankingConfig()
	got := mgr.Get()
	if got.RerankerMinScore != def.RerankerMinScore {
		t.Errorf("RerankerMinScore = %v, want default %v", got.RerankerMinScore, def.RerankerMinScore)
	}
	if got.SourceHalfLifeDays["telegram"] != def.SourceHalfLifeDays["telegram"] {
		t.Errorf("telegram half-life = %v, want default %v",
			got.SourceHalfLifeDays["telegram"], def.SourceHalfLifeDays["telegram"])
	}
}

func TestRankingManager_LoadFromDB_OverlaysPersistedOverrides(t *testing.T) {
	st, _, _ := newTestDeps(t)
	ctx := context.Background()

	// Seed a mix of valid overrides + a corrupt entry. The corrupt half-life
	// row must be ignored without taking down the rest of the load.
	if err := st.SetSettings(ctx, map[string]string{
		settingRankSourceHalfLife:     `{"telegram": 7}`,
		settingRankSourceRecencyFloor: `{not-json`,
		settingRankRerankMinScore:     `0.55`,
		settingRankMetadataBonus:      `false`,
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	mgr := NewRankingManager(st, zap.NewNop())
	if err := mgr.LoadFromDB(ctx); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	got := mgr.Get()

	if got.SourceHalfLifeDays["telegram"] != 7 {
		t.Errorf("telegram half-life override = %v, want 7", got.SourceHalfLifeDays["telegram"])
	}
	if got.SourceHalfLifeDays["filesystem"] != 90 {
		t.Errorf("filesystem half-life should be default 90, got %v",
			got.SourceHalfLifeDays["filesystem"])
	}
	// Corrupt floor row ignored — defaults kept.
	if got.SourceRecencyFloor["telegram"] != 0.65 {
		t.Errorf("telegram recency floor = %v, want default 0.65 after ignoring corrupt row",
			got.SourceRecencyFloor["telegram"])
	}
	if got.RerankerMinScore != 0.55 {
		t.Errorf("RerankerMinScore = %v, want 0.55", got.RerankerMinScore)
	}
	if got.MetadataBonusEnabled {
		t.Errorf("MetadataBonusEnabled should be false after override")
	}
}

// TestRankingManager_LoadFromDB_StoreErrorPropagates closes the store so
// the next GetSettings call fails. LoadFromDB must bubble the error up so
// boot doesn't silently proceed with a stale default config when the DB
// is actually broken.
func TestRankingManager_LoadFromDB_StoreErrorPropagates(t *testing.T) {
	st, _, _ := newTestDeps(t)
	mgr := NewRankingManager(st, zap.NewNop())
	st.Close()
	if err := mgr.LoadFromDB(context.Background()); err == nil {
		t.Error("expected error when store is closed, got nil")
	}
}

func TestRankingManager_SetRerankerMinScore_Persists(t *testing.T) {
	st, _, _ := newTestDeps(t)
	ctx := context.Background()
	mgr := NewRankingManager(st, zap.NewNop())

	if err := mgr.SetRerankerMinScore(ctx, 0.7); err != nil {
		t.Fatalf("SetRerankerMinScore: %v", err)
	}
	if got := mgr.Get().RerankerMinScore; got != 0.7 {
		t.Errorf("in-memory min score = %v, want 0.7", got)
	}

	// A fresh manager loading from the same store should see the value.
	fresh := NewRankingManager(st, zap.NewNop())
	if err := fresh.LoadFromDB(ctx); err != nil {
		t.Fatalf("fresh LoadFromDB: %v", err)
	}
	if got := fresh.Get().RerankerMinScore; got != 0.7 {
		t.Errorf("persisted min score = %v, want 0.7", got)
	}
}

// NewRankingManager with a nil logger shouldn't panic — callers in tests
// frequently pass nil rather than a real zap instance.
func TestNewRankingManager_NilLoggerTolerated(t *testing.T) {
	mgr := NewRankingManager(nil, nil)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
}
