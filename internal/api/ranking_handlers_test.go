//go:build integration

package api

import (
	"net/http"
	"testing"

	"github.com/muty/nexus/internal/pipeline"
	"go.uber.org/zap"
)

func rankingRouter(t *testing.T) (http.Handler, *RankingManager, string, string) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	rankingMgr := NewRankingManager(st, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, rankingMgr, testJWTSecret, nil, nil, nil, zap.NewNop())
	_, admin := createTestAdmin(t, st)
	_, user := createTestUser(t, st)
	return router, rankingMgr, admin, user
}

func TestGetRankingSettings_NonAdminForbidden(t *testing.T) {
	router, _, _, userToken := rankingRouter(t)
	w := doJSON(t, router, http.MethodGet, "/api/settings/ranking", "", userToken)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin expected 403, got %d", w.Code)
	}
}

func TestGetRankingSettings_ReturnsDefaultsBeforeAnyPut(t *testing.T) {
	router, _, adminToken, _ := rankingRouter(t)
	w := doJSON(t, router, http.MethodGet, "/api/settings/ranking", "", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	halfLife := data["source_half_life_days"].(map[string]any)
	if got := halfLife["telegram"].(float64); got != 14 {
		t.Errorf("default telegram half-life = %v, want 14", got)
	}
	// reranker_min_score has moved to the /api/settings/rerank endpoint;
	// the ranking payload shouldn't surface it any more.
	if _, present := data["reranker_min_score"]; present {
		t.Errorf("reranker_min_score should not appear on ranking payload")
	}
	if _, present := data["runtime_wiring_active"]; present {
		t.Errorf("runtime_wiring_active should be removed now that ranking is live")
	}
}

func TestUpdateRankingSettings_RoundTrip(t *testing.T) {
	router, mgr, adminToken, _ := rankingRouter(t)
	body := `{
		"source_half_life_days": {"telegram": 7},
		"source_recency_floor": {},
		"source_trust_weight": {},
		"metadata_bonus_enabled": false,
		"source_trust_enabled": true
	}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", body, adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("put expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Round-trip via GET confirms persistence, and the RankingManager
	// snapshot confirms the hot-swap into runtime actually happened.
	w = doJSON(t, router, http.MethodGet, "/api/settings/ranking", "", adminToken)
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	halfLife := data["source_half_life_days"].(map[string]any)
	if got := halfLife["telegram"].(float64); got != 7 {
		t.Errorf("persisted telegram half-life = %v, want 7", got)
	}
	if got := halfLife["filesystem"].(float64); got != 90 {
		t.Errorf("default filesystem half-life after partial PUT = %v, want 90", got)
	}
	if data["metadata_bonus_enabled"] != false {
		t.Errorf("metadata_bonus_enabled = %v, want false", data["metadata_bonus_enabled"])
	}

	if got := mgr.Get().SourceHalfLifeDays["telegram"]; got != 7 {
		t.Errorf("in-memory telegram half-life = %v, want 7 (hot-swap broken)", got)
	}
	if mgr.Get().MetadataBonusEnabled {
		t.Errorf("in-memory MetadataBonusEnabled should be false after PUT")
	}
}

func TestUpdateRankingSettings_RejectsInvalidJSON(t *testing.T) {
	router, _, adminToken, _ := rankingRouter(t)
	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", `{not-json`, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", w.Code)
	}
}

func TestUpdateRankingSettings_RejectsUnknownSourceTypeInHalfLife(t *testing.T) {
	router, _, adminToken, _ := rankingRouter(t)
	body := `{"source_half_life_days": {"teleg": 7}}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", body, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown source_type, got %d", w.Code)
	}
}

func TestUpdateRankingSettings_RejectsUnknownSourceTypeInFloor(t *testing.T) {
	router, _, adminToken, _ := rankingRouter(t)
	body := `{"source_recency_floor": {"zzz": 0.5}}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", body, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown floor key, got %d", w.Code)
	}
}

func TestUpdateRankingSettings_RejectsUnknownSourceTypeInTrust(t *testing.T) {
	router, _, adminToken, _ := rankingRouter(t)
	body := `{"source_trust_weight": {"zzz": 1.0}}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", body, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown trust key, got %d", w.Code)
	}
}

func TestUpdateRankingSettings_RejectsOutOfBoundsFloor(t *testing.T) {
	router, _, adminToken, _ := rankingRouter(t)
	body := `{"source_recency_floor": {"imap": 1.5}}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", body, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for floor > 1, got %d", w.Code)
	}
}

func TestUpdateRankingSettings_RejectsNonPositiveHalfLife(t *testing.T) {
	router, _, adminToken, _ := rankingRouter(t)
	body := `{"source_half_life_days": {"telegram": 0}}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", body, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for zero half-life, got %d", w.Code)
	}
}

func TestUpdateRankingSettings_RejectsNegativeTrustWeight(t *testing.T) {
	router, _, adminToken, _ := rankingRouter(t)
	body := `{"source_trust_weight": {"imap": -0.1}}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", body, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative trust, got %d", w.Code)
	}
}

func TestUpdateRankingSettings_NonAdminForbidden(t *testing.T) {
	router, _, _, userToken := rankingRouter(t)
	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", `{}`, userToken)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin expected 403, got %d", w.Code)
	}
}

func TestUpdateRankingSettings_StoreClosedSurfacesError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, NewRankingManager(st, zap.NewNop()), testJWTSecret, nil, nil, nil, zap.NewNop())
	_, adminToken := createTestAdmin(t, st)

	st.Close()

	w := doJSON(t, router, http.MethodPut, "/api/settings/ranking", `{}`, adminToken)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when store fails, got %d; body: %s", w.Code, w.Body.String())
	}
}

// Rerank min-score moved — verify PUT /api/settings/rerank accepts + persists
// the new field, and that the RankingManager actually hot-swaps the value
// so the next search query would see it.
func TestUpdateRerankSettings_PersistsMinScoreIntoRankingManager(t *testing.T) {
	router, mgr, adminToken, _ := rankingRouter(t)

	body := `{"provider":"","model":"","api_key":"","min_score":0.6}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/rerank", body, adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	if got := mgr.Get().RerankerMinScore; got != 0.6 {
		t.Errorf("in-memory rerank floor = %v, want 0.6", got)
	}

	// GET surfaces the new floor.
	w = doJSON(t, router, http.MethodGet, "/api/settings/rerank", "", adminToken)
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	if got := data["min_score"].(float64); got != 0.6 {
		t.Errorf("GET min_score = %v, want 0.6", got)
	}
}

func TestUpdateRerankSettings_RejectsOutOfBoundsMinScore(t *testing.T) {
	router, _, adminToken, _ := rankingRouter(t)
	body := `{"provider":"","model":"","api_key":"","min_score":1.5}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/rerank", body, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for min_score > 1, got %d", w.Code)
	}
}
