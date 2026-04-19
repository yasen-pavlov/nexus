//go:build integration

package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/muty/nexus/internal/pipeline"
	"github.com/muty/nexus/internal/syncruns"
	"go.uber.org/zap"
)

func retentionRouter(t *testing.T, withSweeper bool) (http.Handler, string, string) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	var sw *syncruns.Sweeper
	if withSweeper {
		sw = syncruns.NewSweeper(st, st, zap.NewNop())
	}
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, sw, nil, testJWTSecret, nil, zap.NewNop())
	_, adminToken := createTestAdmin(t, st)
	_, userToken := createTestUser(t, st)
	return router, adminToken, userToken
}

func TestGetRetentionSettings_NonAdminForbidden(t *testing.T) {
	router, _, userToken := retentionRouter(t, false)
	w := doJSON(t, router, http.MethodGet, "/api/settings/retention", "", userToken)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin expected 403, got %d", w.Code)
	}
}

func TestGetRetentionSettings_Defaults(t *testing.T) {
	router, adminToken, _ := retentionRouter(t, false)
	w := doJSON(t, router, http.MethodGet, "/api/settings/retention", "", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	if got := data["retention_days"].(float64); int(got) != syncruns.DefaultRetentionDays {
		t.Errorf("retention_days = %v, want %d", got, syncruns.DefaultRetentionDays)
	}
	if got := data["retention_per_connector"].(float64); int(got) != syncruns.DefaultRetentionPerConn {
		t.Errorf("retention_per_connector = %v, want %d", got, syncruns.DefaultRetentionPerConn)
	}
	if got := data["sweep_interval_minutes"].(float64); int(got) != syncruns.DefaultSweepIntervalMins {
		t.Errorf("sweep_interval_minutes = %v, want %d", got, syncruns.DefaultSweepIntervalMins)
	}
	if got := data["min_sweep_interval_minutes"].(float64); int(got) != syncruns.MinSweepIntervalMins {
		t.Errorf("min_sweep_interval_minutes = %v, want %d", got, syncruns.MinSweepIntervalMins)
	}
}

func TestUpdateRetentionSettings_RoundTrip(t *testing.T) {
	router, adminToken, _ := retentionRouter(t, false)
	body := fmt.Sprintf(`{"retention_days":30,"retention_per_connector":50,"sweep_interval_minutes":%d}`, syncruns.MinSweepIntervalMins+10)

	w := doJSON(t, router, http.MethodPut, "/api/settings/retention", body, adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("put expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	// Re-read and verify persistence.
	w = doJSON(t, router, http.MethodGet, "/api/settings/retention", "", adminToken)
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	if got := data["retention_days"].(float64); got != 30 {
		t.Errorf("retention_days = %v, want 30", got)
	}
	if got := data["retention_per_connector"].(float64); got != 50 {
		t.Errorf("retention_per_connector = %v, want 50", got)
	}
	if got := data["sweep_interval_minutes"].(float64); int(got) != syncruns.MinSweepIntervalMins+10 {
		t.Errorf("sweep_interval_minutes = %v, want %d", got, syncruns.MinSweepIntervalMins+10)
	}
}

func TestUpdateRetentionSettings_RejectsNegativeRetentionDays(t *testing.T) {
	router, adminToken, _ := retentionRouter(t, false)
	w := doJSON(t, router, http.MethodPut, "/api/settings/retention", `{"retention_days":-1,"retention_per_connector":50,"sweep_interval_minutes":60}`, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on negative retention_days, got %d", w.Code)
	}
}

func TestUpdateRetentionSettings_RejectsTooShortSweepInterval(t *testing.T) {
	router, adminToken, _ := retentionRouter(t, false)
	// One minute below the floor — the sweeper will never accept this; the
	// handler must short-circuit so the user sees a clear 400 instead of
	// silent clamping.
	body := fmt.Sprintf(`{"retention_days":30,"retention_per_connector":50,"sweep_interval_minutes":%d}`, syncruns.MinSweepIntervalMins-1)
	w := doJSON(t, router, http.MethodPut, "/api/settings/retention", body, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when below min sweep interval, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestRunRetentionSweep_NoSweeperConfigured(t *testing.T) {
	router, adminToken, _ := retentionRouter(t, false)
	w := doJSON(t, router, http.MethodPost, "/api/settings/retention/sweep", "", adminToken)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when sweeper not wired, got %d", w.Code)
	}
}

func TestRunRetentionSweep_HappyPath(t *testing.T) {
	router, adminToken, _ := retentionRouter(t, true)
	w := doJSON(t, router, http.MethodPost, "/api/settings/retention/sweep", "", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	if data["ok"] != true {
		t.Errorf("expected ok:true, got %v", data)
	}
}

func TestUpdateRetentionSettings_RejectsInvalidJSON(t *testing.T) {
	router, adminToken, _ := retentionRouter(t, false)
	w := doJSON(t, router, http.MethodPut, "/api/settings/retention", `{broken`, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", w.Code)
	}
}

func TestUpdateRetentionSettings_RejectsNegativePerConnector(t *testing.T) {
	router, adminToken, _ := retentionRouter(t, false)
	body := `{"retention_days":30,"retention_per_connector":-1,"sweep_interval_minutes":60}`
	w := doJSON(t, router, http.MethodPut, "/api/settings/retention", body, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for negative retention_per_connector, got %d", w.Code)
	}
}

func TestRunRetentionSweep_NonAdminForbidden(t *testing.T) {
	router, _, userToken := retentionRouter(t, true)
	w := doJSON(t, router, http.MethodPost, "/api/settings/retention/sweep", "", userToken)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin expected 403, got %d", w.Code)
	}
}

func TestGetRetentionSettings_StoreClosedSurfacesError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, zap.NewNop())
	_, adminToken := createTestAdmin(t, st)

	st.Close()

	w := doJSON(t, router, http.MethodGet, "/api/settings/retention", "", adminToken)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when store fails, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestUpdateRetentionSettings_StoreClosedSurfacesError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, zap.NewNop())
	_, adminToken := createTestAdmin(t, st)

	st.Close()

	body := fmt.Sprintf(`{"retention_days":30,"retention_per_connector":50,"sweep_interval_minutes":%d}`, syncruns.MinSweepIntervalMins+10)
	w := doJSON(t, router, http.MethodPut, "/api/settings/retention", body, adminToken)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when store fails, got %d; body: %s", w.Code, w.Body.String())
	}
}
