//go:build integration

package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"go.uber.org/zap"
)

// seedEnabledConnector creates a filesystem connector owned by ownerID
// (nil = shared). Returns the persisted config so the test has the real UUID.
func seedEnabledConnector(t *testing.T, cm *ConnectorManager, ownerID uuid.UUID, name string, shared bool) *model.ConnectorConfig {
	t.Helper()
	cfg := &model.ConnectorConfig{
		Type:    "filesystem",
		Name:    name,
		Config:  map[string]any{"root_path": t.TempDir()},
		Enabled: true,
		Shared:  shared,
	}
	if ownerID != uuid.Nil {
		cfg.UserID = &ownerID
	}
	if err := cm.Add(context.Background(), cfg); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	return cfg
}

// --- CancelSyncJob ---

func TestCancelSyncJob_ReturnsAccepted(t *testing.T) {
	st, _, cm, sjm, router := newTestRouterWithJobs(t)
	admin, _ := createTestAdmin(t, st)
	cfg := seedEnabledConnector(t, cm, admin, "cancel-accept", false)

	job, _, err := sjm.Start(cfg.ID, cfg.Name, "filesystem")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sync/jobs/"+job.ID+"/cancel", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestCancelSyncJob_DeletedConnector_Returns404(t *testing.T) {
	// Job exists in the manager but the connector config was removed.
	// Happens if the user deletes the connector while a sync is running;
	// the cancel endpoint must still fail closed.
	st, _, cm, sjm, router := newTestRouterWithJobs(t)
	admin, _ := createTestAdmin(t, st)
	cfg := seedEnabledConnector(t, cm, admin, "cancel-gone", false)

	job, _, err := sjm.Start(cfg.ID, cfg.Name, "filesystem")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := cm.Remove(context.Background(), cfg.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sync/jobs/"+job.ID+"/cancel", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 after connector removed, got %d", w.Code)
	}
}

func TestCancelSyncJob_UnknownID_Returns404(t *testing.T) {
	_, _, _, _, router := newTestRouterWithJobs(t)
	req := httptest.NewRequest(http.MethodPost, "/api/sync/jobs/"+uuid.New().String()+"/cancel", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestCancelSyncJob_InvalidID_Returns400(t *testing.T) {
	_, _, _, _, router := newTestRouterWithJobs(t)
	req := httptest.NewRequest(http.MethodPost, "/api/sync/jobs/not-a-uuid/cancel", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCancelSyncJob_UserCannotCancelOtherUsersJob(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), sjm, nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	otherOwner, _ := createTestAdmin(t, st)
	cfg := seedEnabledConnector(t, cm, otherOwner, "cancel-other", false)

	_, regularToken := createTestUser(t, st)

	job, _, err := sjm.Start(cfg.ID, cfg.Name, "filesystem")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/sync/jobs/"+job.ID+"/cancel", nil)
	req.Header.Set("Authorization", "Bearer "+regularToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for other-user job, got %d", w.Code)
	}
}

// --- ListSyncRunsForConnector ---

func TestListSyncRunsForConnector_EmptyList(t *testing.T) {
	st, _, cm, _, router := newTestRouterWithJobs(t)
	admin, _ := createTestAdmin(t, st)
	cfg := seedEnabledConnector(t, cm, admin, "runs-empty", false)

	req := httptest.NewRequest(http.MethodGet, "/api/connectors/"+cfg.ID.String()+"/runs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var wrapper struct {
		Data []model.SyncRun `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrapper); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	runs := wrapper.Data
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestListSyncRunsForConnector_PersistsStartCompleteRoundtrip(t *testing.T) {
	st, _, cm, sjm, router := newTestRouterWithJobs(t)
	admin, _ := createTestAdmin(t, st)
	cfg := seedEnabledConnector(t, cm, admin, "runs-persist", false)

	job, _, err := sjm.Start(cfg.ID, cfg.Name, "filesystem")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	sjm.Update(job.ID, 100, 100, 0)
	sjm.Complete(job.ID, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/connectors/"+cfg.ID.String()+"/runs?limit=10", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var wrapper struct {
		Data []model.SyncRun `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrapper); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	runs := wrapper.Data
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Status != SyncStatusCompleted {
		t.Errorf("status = %q, want completed", runs[0].Status)
	}
	if runs[0].DocsProcessed != 100 {
		t.Errorf("docs_processed = %d, want 100", runs[0].DocsProcessed)
	}
	if runs[0].CompletedAt == nil {
		t.Error("completed_at should be set")
	}
}

func TestListSyncRunsForConnector_UnknownConnector_Returns404(t *testing.T) {
	_, _, _, _, router := newTestRouterWithJobs(t)
	req := httptest.NewRequest(http.MethodGet, "/api/connectors/"+uuid.New().String()+"/runs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestListSyncRunsForConnector_InvalidID_Returns400(t *testing.T) {
	_, _, _, _, router := newTestRouterWithJobs(t)
	req := httptest.NewRequest(http.MethodGet, "/api/connectors/not-a-uuid/runs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestListSyncRunsForConnector_ClampsInvalidLimitString(t *testing.T) {
	st, _, cm, _, router := newTestRouterWithJobs(t)
	admin, _ := createTestAdmin(t, st)
	cfg := seedEnabledConnector(t, cm, admin, "runs-badlimit", false)

	// Non-numeric limit should fall through to the default; no 400.
	req := httptest.NewRequest(http.MethodGet, "/api/connectors/"+cfg.ID.String()+"/runs?limit=abc", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestListSyncRunsForConnector_UserCannotReadOtherUsersConnector(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), sjm, nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	otherOwner, _ := createTestAdmin(t, st)
	cfg := seedEnabledConnector(t, cm, otherOwner, "runs-hidden", false)

	_, regularToken := createTestUser(t, st)
	req := httptest.NewRequest(http.MethodGet, "/api/connectors/"+cfg.ID.String()+"/runs", nil)
	req.Header.Set("Authorization", "Bearer "+regularToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- StreamAllSyncProgress ---

func TestStreamAllSyncProgress_DeliversEventsForReadableJobs(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), sjm, nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	admin, adminToken := createTestAdmin(t, st)
	cfg := seedEnabledConnector(t, cm, admin, "sse-visible", false)

	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/sync/progress", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	// Kick off a job after a brief delay so the client's SubscribeAll is
	// already wired before Start fires notify.
	go func() {
		time.Sleep(80 * time.Millisecond)
		job, _, err := sjm.Start(cfg.ID, cfg.Name, "filesystem")
		if err != nil {
			return
		}
		sjm.Update(job.ID, 10, 5, 0)
		time.Sleep(80 * time.Millisecond)
		sjm.Complete(job.ID, nil)
	}()

	scanner := bufio.NewScanner(resp.Body)
	gotDataFrame := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var job SyncJob
		if err := json.Unmarshal([]byte(payload), &job); err == nil && job.ConnectorID == cfg.ID.String() {
			gotDataFrame = true
			break
		}
	}
	if !gotDataFrame {
		t.Error("expected at least one data frame matching the seeded connector")
	}
}

func TestStreamAllSyncProgress_AcceptsTokenQueryParam(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), sjm, nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	_, adminToken := createTestAdmin(t, st)

	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/sync/progress?token=%s", srv.URL, adminToken), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with token query, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
}

func TestStreamAllSyncProgress_FiltersOrphanedJobs(t *testing.T) {
	// Seed a job whose connector has since been removed from the
	// manager. canReadJob must fail closed — the stream emits nothing
	// for orphans rather than crashing or leaking cross-user events.
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), sjm, nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	admin, adminToken := createTestAdmin(t, st)
	cfg := seedEnabledConnector(t, cm, admin, "sse-orphan", false)

	// Start a job, then remove the connector under it.
	job, _, err := sjm.Start(cfg.ID, cfg.Name, "filesystem")
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.Remove(context.Background(), cfg.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	sjm.Update(job.ID, 5, 1, 0)

	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/sync/progress", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var sj SyncJob
		if err := json.Unmarshal([]byte(payload), &sj); err != nil {
			continue
		}
		if sj.ConnectorID == cfg.ID.String() {
			t.Errorf("orphaned-connector job should be filtered from the stream")
			return
		}
	}
}

func TestStreamAllSyncProgress_FiltersJobsUserCannotRead(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	sjm := NewSyncJobManager(st, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), sjm, nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	otherAdmin, _ := createTestAdmin(t, st)
	hiddenCfg := seedEnabledConnector(t, cm, otherAdmin, "sse-hidden", false)

	_, regularToken := createTestUser(t, st)

	// Prime a job on the hidden connector so the endpoint's initial
	// snapshot-emit path has something to (correctly) filter out.
	job, _, err := sjm.Start(hiddenCfg.ID, hiddenCfg.Name, "filesystem")
	if err != nil {
		t.Fatal(err)
	}
	sjm.Update(job.ID, 5, 1, 0)

	srv := httptest.NewServer(router)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/sync/progress", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+regularToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var sj SyncJob
		if err := json.Unmarshal([]byte(payload), &sj); err != nil {
			continue
		}
		if sj.ConnectorID == hiddenCfg.ID.String() {
			t.Errorf("user should not see hidden connector's job on SSE stream")
			return
		}
	}
}
