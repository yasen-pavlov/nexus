//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"go.uber.org/zap"
)

// TestRankingRuntimeWiring_RecencyOverrideShiftsResults proves that saving
// a new per-source recency half-life through `PUT /api/settings/ranking`
// actually changes the order of `/api/search` hits on the next query —
// i.e. persistence + hot-swap + search-path consumption are all live.
//
// The setup: two filesystem docs, identical content, one recent and one
// 30 days old. With the aggressive half-life we persist (1 day + floor
// 0.1), the 30-day-old doc decays to a tiny multiplier while the recent
// one stays near 1.0 — the recent doc should rank strictly higher.
func TestRankingRuntimeWiring_RecencyOverrideShiftsResults(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	rankingMgr := NewRankingManager(st, zap.NewNop())
	router := NewRouter(
		st, sc, p, cm, em,
		NewRerankManager(st, zap.NewNop()),
		NewSyncJobManager(st, zap.NewNop()),
		nil, nil, rankingMgr,
		testJWTSecret, nil, zap.NewNop(),
	)
	_, adminToken := createTestAdmin(t, st)

	ctx := context.Background()
	now := time.Now()

	// Shared chunks so both docs pass the search ownership filter (the
	// Document struct has no Shared field — that lives on Chunk).
	chunks := []model.Chunk{
		{
			ID: "fresh.txt:0", ParentID: "fresh.txt", ChunkIndex: 0,
			Title: "fresh", Content: "matching keyword widget", FullContent: "matching keyword widget",
			SourceType: "filesystem", SourceName: "test", SourceID: "fresh.txt",
			Metadata: map[string]any{}, Visibility: "private",
			Shared: true, CreatedAt: now.Add(-time.Hour),
		},
		{
			ID: "old.txt:0", ParentID: "old.txt", ChunkIndex: 0,
			Title: "old", Content: "matching keyword widget", FullContent: "matching keyword widget",
			SourceType: "filesystem", SourceName: "test", SourceID: "old.txt",
			Metadata: map[string]any{}, Visibility: "private",
			Shared: true, CreatedAt: now.Add(-30 * 24 * time.Hour),
		},
	}
	if err := sc.IndexChunks(ctx, chunks); err != nil {
		t.Fatalf("index chunks: %v", err)
	}
	if err := sc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	// Persist an aggressive filesystem half-life (1 day + floor 0.1) —
	// with the default floor of 0.85 both docs would stay close in score.
	body := `{
		"source_half_life_days":  {"filesystem": 1},
		"source_recency_floor":   {"filesystem": 0.1},
		"source_trust_weight":    {},
		"metadata_bonus_enabled": true,
		"source_trust_enabled":   true
	}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", body, adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("put ranking: %d %s", w.Code, w.Body.String())
	}

	// Search for the matching keyword — the fresh doc must lead.
	w = doJSON(t, router, http.MethodGet, "/api/search?q=widget", "", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := resp.Data.(map[string]any)
	docs := data["documents"].([]any)
	if len(docs) < 2 {
		t.Fatalf("expected at least 2 docs, got %d: %+v", len(docs), docs)
	}
	first := docs[0].(map[string]any)
	second := docs[1].(map[string]any)
	if first["title"] != "fresh" {
		t.Errorf("expected fresh first after aggressive decay, got %q then %q",
			first["title"], second["title"])
	}
}
