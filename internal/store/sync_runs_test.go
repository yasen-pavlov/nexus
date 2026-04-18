//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

// seedConnector inserts a minimal connector row so the sync_runs FK has
// something to point at. Returns the connector ID.
func seedConnector(t *testing.T, s *Store, name string) uuid.UUID {
	t.Helper()
	cfg := &model.ConnectorConfig{
		Type:    "filesystem",
		Name:    name,
		Config:  map[string]any{"root_path": "/tmp"},
		Enabled: true,
		Shared:  true,
	}
	if err := s.CreateConnectorConfig(context.Background(), cfg); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	return cfg.ID
}

func TestInsertSyncRun_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connID := seedConnector(t, s, "fs-1")

	runID := uuid.New()
	start := time.Now().UTC().Truncate(time.Microsecond)
	run := &model.SyncRun{
		ID:          runID,
		ConnectorID: connID,
		Status:      "running",
		StartedAt:   start,
	}
	if err := s.InsertSyncRun(ctx, run); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := s.GetSyncRun(ctx, runID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "running" || got.ConnectorID != connID {
		t.Errorf("got %+v", got)
	}
	if got.CompletedAt != nil {
		t.Errorf("completed_at should be nil, got %v", got.CompletedAt)
	}
}

func TestGetSyncRun_NotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetSyncRun(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestUpdateSyncRunComplete_SetsTerminalFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connID := seedConnector(t, s, "fs-complete")

	runID := uuid.New()
	if err := s.InsertSyncRun(ctx, &model.SyncRun{
		ID: runID, ConnectorID: connID, Status: "running", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	completed := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
	if err := s.UpdateSyncRunComplete(ctx, runID, "completed", 100, 100, 3, 0, "", completed); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := s.GetSyncRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "completed" || got.DocsTotal != 100 || got.DocsProcessed != 100 || got.DocsDeleted != 3 {
		t.Errorf("counts wrong: %+v", got)
	}
	if got.CompletedAt == nil || !got.CompletedAt.Equal(completed) {
		t.Errorf("completed_at mismatch: got %v want %v", got.CompletedAt, completed)
	}
}

func TestUpdateSyncRunComplete_FailedPreservesErrorMessage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connID := seedConnector(t, s, "fs-fail")

	runID := uuid.New()
	if err := s.InsertSyncRun(ctx, &model.SyncRun{
		ID: runID, ConnectorID: connID, Status: "running", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSyncRunComplete(ctx, runID, "failed", 10, 4, 0, 1, "imap: connection refused", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSyncRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.ErrorMessage != "imap: connection refused" {
		t.Errorf("got %+v", got)
	}
}

func TestUpdateSyncRunComplete_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.UpdateSyncRunComplete(context.Background(), uuid.New(), "completed", 0, 0, 0, 0, "", time.Now().UTC())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestListSyncRunsByConnector_DescByStartedAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connID := seedConnector(t, s, "fs-list")

	now := time.Now().UTC().Truncate(time.Microsecond)
	for i, ago := range []time.Duration{3 * time.Hour, time.Hour, 2 * time.Hour} {
		run := &model.SyncRun{
			ID:          uuid.New(),
			ConnectorID: connID,
			Status:      "completed",
			StartedAt:   now.Add(-ago),
		}
		_ = i
		if err := s.InsertSyncRun(ctx, run); err != nil {
			t.Fatalf("seed run: %v", err)
		}
	}

	got, err := s.ListSyncRunsByConnector(ctx, connID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].StartedAt.Before(got[i].StartedAt) {
			t.Errorf("not sorted DESC at index %d: %v before %v", i, got[i-1].StartedAt, got[i].StartedAt)
		}
	}
}

func TestListSyncRunsByConnector_LimitApplied(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connID := seedConnector(t, s, "fs-limit")

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		_ = s.InsertSyncRun(ctx, &model.SyncRun{
			ID: uuid.New(), ConnectorID: connID, Status: "completed",
			StartedAt: now.Add(-time.Duration(i) * time.Hour),
		})
	}

	got, err := s.ListSyncRunsByConnector(ctx, connID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2, got %d", len(got))
	}
}

func TestListSyncRunsByConnector_LimitClampedToMax(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connID := seedConnector(t, s, "fs-clamp")

	// Request above the cap should not error; should just clamp silently.
	got, err := s.ListSyncRunsByConnector(ctx, connID, maxSyncRunListLimit+100)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("expected empty slice, got nil")
	}
}

func TestListSyncRunsByConnector_DefaultLimitWhenZero(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connID := seedConnector(t, s, "fs-default-limit")

	got, err := s.ListSyncRunsByConnector(ctx, connID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("expected empty slice, got nil")
	}
}

func TestInsertSyncRun_ClosedPool(t *testing.T) {
	s := newClosedStore(t)
	err := s.InsertSyncRun(context.Background(), &model.SyncRun{
		ID: uuid.New(), ConnectorID: uuid.New(), Status: "running", StartedAt: time.Now(),
	})
	if err == nil {
		t.Error("expected error from closed pool")
	}
}

func TestUpdateSyncRunComplete_ClosedPool(t *testing.T) {
	s := newClosedStore(t)
	err := s.UpdateSyncRunComplete(context.Background(), uuid.New(), "completed", 0, 0, 0, 0, "", time.Now())
	if err == nil {
		t.Error("expected error from closed pool")
	}
}

func TestGetSyncRun_ClosedPool(t *testing.T) {
	s := newClosedStore(t)
	_, err := s.GetSyncRun(context.Background(), uuid.New())
	if err == nil {
		t.Error("expected error from closed pool")
	}
}

func TestListSyncRunsByConnector_ClosedPool(t *testing.T) {
	s := newClosedStore(t)
	_, err := s.ListSyncRunsByConnector(context.Background(), uuid.New(), 10)
	if err == nil {
		t.Error("expected error from closed pool")
	}
}

func TestMarkInterruptedStuckRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connID := seedConnector(t, s, "fs-interrupted")

	runningID := uuid.New()
	completedID := uuid.New()
	if err := s.InsertSyncRun(ctx, &model.SyncRun{
		ID: runningID, ConnectorID: connID, Status: "running",
		StartedAt: time.Now().UTC().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().UTC()
	if err := s.InsertSyncRun(ctx, &model.SyncRun{
		ID: completedID, ConnectorID: connID, Status: "completed",
		StartedAt: time.Now().UTC().Add(-30 * time.Minute), CompletedAt: &completedAt,
	}); err != nil {
		t.Fatal(err)
	}

	n, err := s.MarkInterruptedStuckRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 row marked, got %d", n)
	}

	// The previously-running row is now interrupted with a completed_at set.
	got, err := s.GetSyncRun(ctx, runningID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "interrupted" {
		t.Errorf("status = %q, want interrupted", got.Status)
	}
	if got.CompletedAt == nil {
		t.Error("completed_at should be set after sweep")
	}
	if got.ErrorMessage == "" {
		t.Error("error_message should carry the crash explanation")
	}

	// The completed row is untouched.
	untouched, err := s.GetSyncRun(ctx, completedID)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.Status != "completed" {
		t.Errorf("completed run should be untouched, got %q", untouched.Status)
	}
}

func TestDeleteSyncRunsOlderThan(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connID := seedConnector(t, s, "fs-retention")

	now := time.Now().UTC()
	old := uuid.New()
	fresh := uuid.New()
	running := uuid.New()
	if err := s.InsertSyncRun(ctx, &model.SyncRun{
		ID: old, ConnectorID: connID, Status: "completed",
		StartedAt: now.Add(-100 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertSyncRun(ctx, &model.SyncRun{
		ID: fresh, ConnectorID: connID, Status: "completed",
		StartedAt: now.Add(-10 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// Running rows should NEVER be deleted by retention, regardless of age.
	if err := s.InsertSyncRun(ctx, &model.SyncRun{
		ID: running, ConnectorID: connID, Status: "running",
		StartedAt: now.Add(-200 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	n, err := s.DeleteSyncRunsOlderThan(ctx, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 deletion, got %d", n)
	}

	if _, err := s.GetSyncRun(ctx, old); !errors.Is(err, ErrNotFound) {
		t.Errorf("old row should be gone, got %v", err)
	}
	if _, err := s.GetSyncRun(ctx, fresh); err != nil {
		t.Errorf("fresh row should stay, got %v", err)
	}
	if _, err := s.GetSyncRun(ctx, running); err != nil {
		t.Errorf("running row should never be swept, got %v", err)
	}
}

func TestTrimSyncRunsPerConnector(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connA := seedConnector(t, s, "fs-trim-a")
	connB := seedConnector(t, s, "fs-trim-b")

	now := time.Now().UTC()
	seed := func(id uuid.UUID, conn uuid.UUID, offset time.Duration, status string) {
		if err := s.InsertSyncRun(ctx, &model.SyncRun{
			ID: id, ConnectorID: conn, Status: status,
			StartedAt: now.Add(-offset),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// connA: 5 completed runs. Keep=2 → 3 deletions.
	aIDs := make([]uuid.UUID, 5)
	for i := range aIDs {
		aIDs[i] = uuid.New()
		seed(aIDs[i], connA, time.Duration(i+1)*time.Hour, "completed")
	}
	// connB: 1 running + 1 completed. Keep=2 → 0 deletions.
	bRunning := uuid.New()
	bCompleted := uuid.New()
	seed(bRunning, connB, time.Minute, "running")
	seed(bCompleted, connB, 2*time.Minute, "completed")

	n, err := s.TrimSyncRunsPerConnector(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("expected 3 deletions, got %d", n)
	}

	remaining, err := s.ListSyncRunsByConnector(ctx, connA, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Errorf("connA should have 2 rows left, got %d", len(remaining))
	}
	// Newest two (offsets 1h, 2h) survive.
	if remaining[0].ID != aIDs[0] || remaining[1].ID != aIDs[1] {
		t.Errorf("wrong rows survived: %v, %v", remaining[0].ID, remaining[1].ID)
	}

	// connB's running row is always preserved.
	bRows, err := s.ListSyncRunsByConnector(ctx, connB, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bRows) != 2 {
		t.Errorf("connB should have 2 rows left, got %d", len(bRows))
	}
}

func TestTrimSyncRunsPerConnector_ZeroKeepIsNoOp(t *testing.T) {
	s := newTestStore(t)
	n, err := s.TrimSyncRunsPerConnector(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected no-op, got %d deletions", n)
	}
}

func TestCascadeDeleteOnConnectorRemoval(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	connID := seedConnector(t, s, "fs-cascade")

	runID := uuid.New()
	if err := s.InsertSyncRun(ctx, &model.SyncRun{
		ID: runID, ConnectorID: connID, Status: "completed", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteConnectorConfig(ctx, connID); err != nil {
		t.Fatalf("delete connector: %v", err)
	}

	if _, err := s.GetSyncRun(ctx, runID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected cascade-delete to remove sync_run, got err=%v", err)
	}
}
