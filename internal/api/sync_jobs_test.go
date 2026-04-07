package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestSyncJobManager_StartAndGet(t *testing.T) {
	m := NewSyncJobManager()
	job := m.Start("test-conn", "filesystem")

	if job.ID == "" {
		t.Error("expected non-empty job ID")
	}
	if job.ConnectorName != "test-conn" {
		t.Errorf("ConnectorName = %q, want %q", job.ConnectorName, "test-conn")
	}
	if job.Status != "running" {
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

func TestSyncJobManager_GetByConnector(t *testing.T) {
	m := NewSyncJobManager()
	m.Start("conn-a", "filesystem")

	got := m.GetByConnector("conn-a")
	if got == nil {
		t.Fatal("GetByConnector returned nil")
	}
	if got.ConnectorName != "conn-a" {
		t.Errorf("ConnectorName = %q, want conn-a", got.ConnectorName)
	}

	got = m.GetByConnector("nonexistent")
	if got != nil {
		t.Error("expected nil for nonexistent connector")
	}
}

func TestSyncJobManager_GetByConnector_NotRunning(t *testing.T) {
	m := NewSyncJobManager()
	job := m.Start("conn-a", "filesystem")
	m.Complete(job.ID, nil)

	got := m.GetByConnector("conn-a")
	if got != nil {
		t.Error("expected nil for completed connector")
	}
}

func TestSyncJobManager_Update(t *testing.T) {
	m := NewSyncJobManager()
	job := m.Start("test", "filesystem")

	m.Update(job.ID, 100, 50, 2)

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
	m := NewSyncJobManager()
	job := m.Start("test", "filesystem")
	m.Update(job.ID, 10, 10, 0)
	m.Complete(job.ID, nil)

	got := m.Get(job.ID)
	if got.Status != "completed" {
		t.Errorf("Status = %q, want completed", got.Status)
	}
	if got.CompletedAt.IsZero() {
		t.Error("CompletedAt should not be zero")
	}
}

func TestSyncJobManager_Complete_Failure(t *testing.T) {
	m := NewSyncJobManager()
	job := m.Start("test", "filesystem")
	m.Complete(job.ID, fmt.Errorf("connection lost"))

	got := m.Get(job.ID)
	if got.Status != "failed" {
		t.Errorf("Status = %q, want failed", got.Status)
	}
	if got.Error != "connection lost" {
		t.Errorf("Error = %q, want 'connection lost'", got.Error)
	}
}

func TestSyncJobManager_Active(t *testing.T) {
	m := NewSyncJobManager()
	m.Start("a", "filesystem")
	m.Start("b", "imap")

	jobs := m.Active()
	if len(jobs) != 2 {
		t.Errorf("Active() returned %d jobs, want 2", len(jobs))
	}
}

func TestSyncJobManager_Subscribe_ReceivesUpdates(t *testing.T) {
	m := NewSyncJobManager()
	job := m.Start("test", "filesystem")

	ch := m.Subscribe(job.ID)

	// Should receive initial state
	select {
	case update := <-ch:
		if update.Status != "running" {
			t.Errorf("initial update status = %q, want running", update.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for initial update")
	}

	// Update and receive
	m.Update(job.ID, 50, 10, 0)
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
		if update.Status == "completed" {
			break
		}
	}
}

func TestSyncJobManager_Subscribe_CompletedJob(t *testing.T) {
	m := NewSyncJobManager()
	job := m.Start("test", "filesystem")
	m.Complete(job.ID, nil)

	ch := m.Subscribe(job.ID)

	// Should receive final state and channel should be closed
	update, ok := <-ch
	if !ok {
		t.Fatal("channel closed before receiving final state")
	}
	if update.Status != "completed" {
		t.Errorf("status = %q, want completed", update.Status)
	}

	// Channel should be closed now
	_, ok = <-ch
	if ok {
		t.Error("channel should be closed after completed job")
	}
}

func TestSyncJobManager_Get_ReturnsSnapshot(t *testing.T) {
	m := NewSyncJobManager()
	job := m.Start("test", "filesystem")

	got1 := m.Get(job.ID)
	m.Update(job.ID, 100, 50, 0)
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
	m := NewSyncJobManager()
	if m.Get("nonexistent") != nil {
		t.Error("expected nil for nonexistent job")
	}
}

func TestStreamSyncProgress_Handler(t *testing.T) {
	h := newTestHandler()
	job := h.syncJobs.Start("test-fs", "filesystem")

	r := chi.NewRouter()
	r.Get("/api/sync/{connector}/progress", h.StreamSyncProgress)

	// Start a test server so we get real HTTP with flushing
	srv := httptest.NewServer(r)
	defer srv.Close()

	// Update and complete the job in a goroutine
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.syncJobs.Update(job.ID, 10, 5, 0)
		time.Sleep(50 * time.Millisecond)
		h.syncJobs.Complete(job.ID, nil)
	}()

	resp, err := http.Get(srv.URL + "/api/sync/test-fs/progress")
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
	r.Get("/api/sync/{connector}/progress", h.StreamSyncProgress)

	req := httptest.NewRequest(http.MethodGet, "/api/sync/nonexistent/progress", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSyncJobManager_ConcurrentAccess(t *testing.T) {
	m := NewSyncJobManager()
	job := m.Start("test", "filesystem")

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m.Update(job.ID, 100, n, 0)
			_ = m.Get(job.ID)
			_ = m.GetByConnector("test")
			_ = m.Active()
		}(i)
	}
	wg.Wait()

	m.Complete(job.ID, nil)
	got := m.Get(job.ID)
	if got.Status != "completed" {
		t.Errorf("Status = %q, want completed", got.Status)
	}
}
