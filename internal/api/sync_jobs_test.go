package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// newTestSyncJobManager returns a SyncJobManager with no persister — the
// in-memory path only. Use for unit tests that don't care about sync_runs.
func newTestSyncJobManager() *SyncJobManager {
	return NewSyncJobManager(nil, zap.NewNop())
}

// mustStart calls Start and fails the test if it returns an error. Returns
// only the job and runCtx for brevity in cases that don't exercise
// ErrAlreadyRunning.
func mustStart(t *testing.T, m *SyncJobManager, connID uuid.UUID, name, typ string) (*SyncJob, context.Context) {
	t.Helper()
	job, ctx, err := m.Start(connID, name, typ)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return job, ctx
}

func TestSyncJobManager_StartAndGet(t *testing.T) {
	m := newTestSyncJobManager()
	job, _ := mustStart(t, m, uuid.New(), "test-conn", "filesystem")

	if job.ID == "" {
		t.Error("expected non-empty job ID")
	}
	if job.ConnectorName != "test-conn" {
		t.Errorf("ConnectorName = %q, want %q", job.ConnectorName, "test-conn")
	}
	if job.Status != SyncStatusRunning {
		t.Errorf("Status = %q, want running", job.Status)
	}

	got := m.Get(job.ID)
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != job.ID {
		t.Errorf("Get ID = %q, want %q", got.ID, job.ID)
	}
}

func TestSyncJobManager_Start_AlreadyRunning(t *testing.T) {
	m := newTestSyncJobManager()
	connID := uuid.New()
	_, _ = mustStart(t, m, connID, "conn-a", "filesystem")

	_, _, err := m.Start(connID, "conn-a", "filesystem")
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("want ErrAlreadyRunning, got %v", err)
	}
}

func TestSyncJobManager_Start_AfterComplete_Succeeds(t *testing.T) {
	m := newTestSyncJobManager()
	connID := uuid.New()
	job1, _ := mustStart(t, m, connID, "conn-a", "filesystem")
	m.Complete(job1.ID, nil)

	// After completion, a new run should be allowed for the same connector.
	job2, _, err := m.Start(connID, "conn-a", "filesystem")
	if err != nil {
		t.Fatalf("Start after complete: %v", err)
	}
	if job2.ID == job1.ID {
		t.Error("new run should get a fresh job id")
	}
}

func TestSyncJobManager_Start_ConcurrentOnSameConnector(t *testing.T) {
	m := newTestSyncJobManager()
	connID := uuid.New()

	var wg sync.WaitGroup
	successes := 0
	var mu sync.Mutex
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := m.Start(connID, "race", "filesystem")
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			} else if !errors.Is(err, ErrAlreadyRunning) {
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Errorf("expected exactly 1 Start to succeed, got %d", successes)
	}
}

func TestSyncJobManager_GetByConnector(t *testing.T) {
	m := newTestSyncJobManager()
	connID := uuid.New()
	_, _ = mustStart(t, m, connID, "conn-a", "filesystem")

	got := m.GetByConnector(connID)
	if got == nil {
		t.Fatal("GetByConnector returned nil")
	}
	if got.ConnectorName != "conn-a" {
		t.Errorf("ConnectorName = %q, want conn-a", got.ConnectorName)
	}

	got = m.GetByConnector(uuid.New())
	if got != nil {
		t.Error("expected nil for nonexistent connector")
	}
}

func TestSyncJobManager_GetByConnector_NotRunning(t *testing.T) {
	m := newTestSyncJobManager()
	connID := uuid.New()
	job, _ := mustStart(t, m, connID, "conn-a", "filesystem")
	m.Complete(job.ID, nil)

	got := m.GetByConnector(connID)
	if got != nil {
		t.Error("expected nil for completed connector")
	}
}

func TestSyncJobManager_Update(t *testing.T) {
	m := newTestSyncJobManager()
	job, _ := mustStart(t, m, uuid.New(), "test", "filesystem")

	m.Update(job.ID, 100, 50, 2, "")

	got := m.Get(job.ID)
	if got.DocsTotal != 100 {
		t.Errorf("DocsTotal = %d, want 100", got.DocsTotal)
	}
	if got.DocsProcessed != 50 {
		t.Errorf("DocsProcessed = %d, want 50", got.DocsProcessed)
	}
	if got.Errors != 2 {
		t.Errorf("Errors = %d, want 2", got.Errors)
	}
}

func TestSyncJobManager_Complete_Success(t *testing.T) {
	m := newTestSyncJobManager()
	job, _ := mustStart(t, m, uuid.New(), "test", "filesystem")
	m.Update(job.ID, 10, 10, 0, "")
	m.Complete(job.ID, nil)

	got := m.Get(job.ID)
	if got.Status != SyncStatusCompleted {
		t.Errorf("Status = %q, want completed", got.Status)
	}
	if got.CompletedAt.IsZero() {
		t.Error("CompletedAt should not be zero")
	}
}

func TestSyncJobManager_Complete_Failure(t *testing.T) {
	m := newTestSyncJobManager()
	job, _ := mustStart(t, m, uuid.New(), "test", "filesystem")
	m.Complete(job.ID, fmt.Errorf("connection lost"))

	got := m.Get(job.ID)
	if got.Status != SyncStatusFailed {
		t.Errorf("Status = %q, want failed", got.Status)
	}
	if got.Error != "connection lost" {
		t.Errorf("Error = %q, want 'connection lost'", got.Error)
	}
}

func TestSyncJobManager_Complete_CanceledContext(t *testing.T) {
	m := newTestSyncJobManager()
	job, _ := mustStart(t, m, uuid.New(), "test", "filesystem")

	// Wrap so errors.Is still detects it — matches how the pipeline will
	// return a wrapped context.Canceled through the sync loop.
	wrapped := fmt.Errorf("pipeline: fetch: %w", context.Canceled)
	m.Complete(job.ID, wrapped)

	got := m.Get(job.ID)
	if got.Status != SyncStatusCanceled {
		t.Errorf("Status = %q, want canceled", got.Status)
	}
	if got.Error != "" {
		t.Errorf("Error should be empty for canceled jobs, got %q", got.Error)
	}
}

func TestSyncJobManager_Cancel_Idempotent(t *testing.T) {
	m := newTestSyncJobManager()
	job, runCtx := mustStart(t, m, uuid.New(), "test", "filesystem")

	if !m.Cancel(job.ID) {
		t.Error("first Cancel should report success")
	}
	// Second Cancel on the same running job is still a no-op but must
	// return true (cancel func is still registered until Complete).
	if !m.Cancel(job.ID) {
		t.Error("second Cancel should also report success before Complete")
	}

	// The context passed back from Start must be canceled.
	select {
	case <-runCtx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Error("runCtx should be canceled after Cancel")
	}
}

func TestSyncJobManager_Cancel_AfterComplete_NoOp(t *testing.T) {
	m := newTestSyncJobManager()
	job, _ := mustStart(t, m, uuid.New(), "test", "filesystem")
	m.Complete(job.ID, nil)

	if m.Cancel(job.ID) {
		t.Error("Cancel after Complete should report false (no running job)")
	}
}

func TestSyncJobManager_Cancel_UnknownID(t *testing.T) {
	m := newTestSyncJobManager()
	if m.Cancel("nonexistent") {
		t.Error("Cancel on unknown id should return false")
	}
}

func TestSyncJobManager_StartForSchedule_ReturnsJobIDAndCtx(t *testing.T) {
	m := newTestSyncJobManager()
	connID := uuid.New()
	id, ctx, err := m.StartForSchedule(connID, "scheduled", "filesystem")
	if err != nil {
		t.Fatalf("StartForSchedule: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty job id")
	}
	if ctx == nil {
		t.Fatal("expected non-nil runCtx")
	}
	if m.Get(id) == nil {
		t.Errorf("job %q should be registered", id)
	}
	// Subsequent Start on the same connector must refuse to double-schedule.
	if _, _, err := m.StartForSchedule(connID, "scheduled", "filesystem"); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("expected ErrAlreadyRunning, got %v", err)
	}
}

func TestSyncJobManager_Active(t *testing.T) {
	m := newTestSyncJobManager()
	_, _ = mustStart(t, m, uuid.New(), "a", "filesystem")
	_, _ = mustStart(t, m, uuid.New(), "b", "imap")

	jobs := m.Active()
	if len(jobs) != 2 {
		t.Errorf("Active() returned %d jobs, want 2", len(jobs))
	}
}

func TestSyncJobManager_Subscribe_ReceivesUpdates(t *testing.T) {
	m := newTestSyncJobManager()
	job, _ := mustStart(t, m, uuid.New(), "test", "filesystem")

	ch := m.Subscribe(job.ID)

	// Should receive initial state
	select {
	case update := <-ch:
		if update.Status != SyncStatusRunning {
			t.Errorf("initial update status = %q, want running", update.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for initial update")
	}

	// Update and receive
	m.Update(job.ID, 50, 10, 0, "")
	select {
	case update := <-ch:
		if update.DocsTotal != 50 || update.DocsProcessed != 10 {
			t.Errorf("update = %d/%d, want 10/50", update.DocsProcessed, update.DocsTotal)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for progress update")
	}

	// Complete closes the channel
	m.Complete(job.ID, nil)

	// Drain the completion notification
	for update := range ch {
		if update.Status == SyncStatusCompleted {
			break
		}
	}
}

func TestSyncJobManager_Subscribe_CompletedJob(t *testing.T) {
	m := newTestSyncJobManager()
	job, _ := mustStart(t, m, uuid.New(), "test", "filesystem")
	m.Complete(job.ID, nil)

	ch := m.Subscribe(job.ID)

	// Should receive final state and channel should be closed
	update, ok := <-ch
	if !ok {
		t.Fatal("channel closed before receiving final state")
	}
	if update.Status != SyncStatusCompleted {
		t.Errorf("status = %q, want completed", update.Status)
	}

	// Channel should be closed now
	_, ok = <-ch
	if ok {
		t.Error("channel should be closed after completed job")
	}
}

func TestSyncJobManager_Get_ReturnsSnapshot(t *testing.T) {
	m := newTestSyncJobManager()
	job, _ := mustStart(t, m, uuid.New(), "test", "filesystem")

	got1 := m.Get(job.ID)
	m.Update(job.ID, 100, 50, 0, "")
	got2 := m.Get(job.ID)

	// got1 should still have old values (snapshot)
	if got1.DocsProcessed != 0 {
		t.Error("snapshot should not be affected by later updates")
	}
	if got2.DocsProcessed != 50 {
		t.Errorf("DocsProcessed = %d, want 50", got2.DocsProcessed)
	}
}

func TestSyncJobManager_Get_NotFound(t *testing.T) {
	m := newTestSyncJobManager()
	if m.Get("nonexistent") != nil {
		t.Error("expected nil for nonexistent job")
	}
}

func TestStreamSyncProgress_Handler(t *testing.T) {
	h := newTestHandler()
	testID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	job, _ := mustStart(t, h.syncJobs, testID, "test-fs", "filesystem")

	r := chi.NewRouter()
	// Inject admin auth context so canReadConnector passes
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, withAdminContext(req))
		})
	})
	r.Get("/api/sync/{id}/progress", h.StreamSyncProgress)

	// Start a test server so we get real HTTP with flushing
	srv := httptest.NewServer(r)
	defer srv.Close()

	// Update and complete the job in a goroutine
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.syncJobs.Update(job.ID, 10, 5, 0, "")
		time.Sleep(50 * time.Millisecond)
		h.syncJobs.Complete(job.ID, nil)
	}()

	resp, err := http.Get(srv.URL + "/api/sync/" + testID.String() + "/progress")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", resp.Header.Get("Content-Type"))
	}

	// Read until the stream closes — we just verify it doesn't hang
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	if n == 0 {
		t.Error("expected some SSE data")
	}
}

func TestStreamSyncProgress_NotFound(t *testing.T) {
	h := newTestHandler()

	r := chi.NewRouter()
	r.Get("/api/sync/{id}/progress", h.StreamSyncProgress)

	req := withAdminContext(httptest.NewRequest(http.MethodGet, "/api/sync/"+uuid.New().String()+"/progress", nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSyncJobManager_Notify_NonexistentJob(t *testing.T) {
	m := newTestSyncJobManager()
	// Should not panic
	m.notify("nonexistent")
}

func TestSyncJobManager_ConcurrentAccess(t *testing.T) {
	m := newTestSyncJobManager()
	job, _ := mustStart(t, m, uuid.New(), "test", "filesystem")

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m.Update(job.ID, 100, n, 0, "")
			_ = m.Get(job.ID)
			_ = m.GetByConnector(uuid.New())
			_ = m.Active()
		}(i)
	}
	wg.Wait()

	m.Complete(job.ID, nil)
	got := m.Get(job.ID)
	if got.Status != SyncStatusCompleted {
		t.Errorf("Status = %q, want completed", got.Status)
	}
}
