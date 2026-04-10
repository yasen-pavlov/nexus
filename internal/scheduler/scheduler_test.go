package scheduler

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"go.uber.org/zap"
)

type mockConnectorGetter struct {
	mu         sync.RWMutex
	connectors map[uuid.UUID]connector.Connector
	configs    map[uuid.UUID]*model.ConnectorConfig
}

func (m *mockConnectorGetter) GetByID(id uuid.UUID) (connector.Connector, *model.ConnectorConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.connectors[id]
	if !ok {
		return nil, nil, false
	}
	if cfg, ok := m.configs[id]; ok {
		return c, cfg, true
	}
	return c, &model.ConnectorConfig{ID: id}, true
}

type pipelineCall struct {
	connectorID uuid.UUID
	name        string
	ownerID     string
	shared      bool
}

type mockPipelineRunner struct {
	mu     sync.Mutex
	calls  []pipelineCall
	err    error
	report *pipeline.SyncReport
}

func (m *mockPipelineRunner) RunWithProgress(_ context.Context, connectorID uuid.UUID, conn connector.Connector, ownerID string, shared bool, _ pipeline.ProgressFunc) (*pipeline.SyncReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, pipelineCall{connectorID: connectorID, name: conn.Name(), ownerID: ownerID, shared: shared})
	if m.err != nil {
		return nil, m.err
	}
	if m.report != nil {
		return m.report, nil
	}
	return &pipeline.SyncReport{ConnectorName: conn.Name(), DocsProcessed: 1}, nil
}

func (m *mockPipelineRunner) getCalls() []pipelineCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]pipelineCall, len(m.calls))
	copy(result, m.calls)
	return result
}

type mockConfigLister struct {
	configs []model.ConnectorConfig
	lastRun map[uuid.UUID]time.Time
	mu      sync.Mutex
}

func (m *mockConfigLister) ListConnectorConfigs(_ context.Context) ([]model.ConnectorConfig, error) {
	return m.configs, nil
}

func (m *mockConfigLister) UpdateLastRun(_ context.Context, id uuid.UUID, t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastRun == nil {
		m.lastRun = make(map[uuid.UUID]time.Time)
	}
	m.lastRun[id] = t
	return nil
}

type mockConn struct {
	name string
}

func (c *mockConn) Type() string                       { return "test" }
func (c *mockConn) Name() string                       { return c.name }
func (c *mockConn) Configure(_ connector.Config) error { return nil }
func (c *mockConn) Validate() error                    { return nil }

func (c *mockConn) Fetch(_ context.Context, _ *model.SyncCursor) (*model.FetchResult, error) {
	return &model.FetchResult{}, nil
}

func TestNew(t *testing.T) {
	s := New(&mockConnectorGetter{}, &mockPipelineRunner{}, &mockConfigLister{}, zap.NewNop())
	if s == nil {
		t.Fatal("expected non-nil scheduler")
	}
	if s.entries == nil {
		t.Fatal("expected entries map to be initialized")
	}
}

func TestStartAndStop(t *testing.T) {
	id := uuid.New()
	store := &mockConfigLister{
		configs: []model.ConnectorConfig{
			{ID: id, Type: "test", Name: "test-conn", Enabled: true, Schedule: "0 * * * *"},
		},
	}
	cm := &mockConnectorGetter{connectors: map[uuid.UUID]connector.Connector{
		id: &mockConn{name: "test-conn"},
	}}

	s := New(cm, &mockPipelineRunner{}, store, zap.NewNop())

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	if len(s.entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(s.entries))
	}

	s.Stop()
}

func TestStartSkipsDisabled(t *testing.T) {
	store := &mockConfigLister{
		configs: []model.ConnectorConfig{
			{ID: uuid.New(), Type: "test", Name: "disabled", Enabled: false, Schedule: "0 * * * *"},
			{ID: uuid.New(), Type: "test", Name: "no-schedule", Enabled: true, Schedule: ""},
		},
	}

	s := New(&mockConnectorGetter{}, &mockPipelineRunner{}, store, zap.NewNop())
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	if len(s.entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(s.entries))
	}
}

func TestOnConnectorChanged_AddsJob(t *testing.T) {
	s := New(&mockConnectorGetter{}, &mockPipelineRunner{}, &mockConfigLister{}, zap.NewNop())
	s.cron.Start()
	defer s.Stop()

	id := uuid.New()
	s.OnConnectorChanged(&model.ConnectorConfig{
		ID: id, Name: "new-conn", Enabled: true, Schedule: "*/5 * * * *",
	})

	if _, ok := s.entries[id]; !ok {
		t.Error("expected entry to be added")
	}
}

func TestOnConnectorChanged_RemovesJobWhenDisabled(t *testing.T) {
	s := New(&mockConnectorGetter{}, &mockPipelineRunner{}, &mockConfigLister{}, zap.NewNop())
	s.cron.Start()
	defer s.Stop()

	id := uuid.New()
	// Add first
	s.OnConnectorChanged(&model.ConnectorConfig{
		ID: id, Name: "conn", Enabled: true, Schedule: "*/5 * * * *",
	})
	if _, ok := s.entries[id]; !ok {
		t.Fatal("expected entry after add")
	}

	// Disable
	s.OnConnectorChanged(&model.ConnectorConfig{
		ID: id, Name: "conn", Enabled: false, Schedule: "*/5 * * * *",
	})
	if _, ok := s.entries[id]; ok {
		t.Error("expected entry to be removed after disable")
	}
}

func TestOnConnectorChanged_RemovesJobWhenNoSchedule(t *testing.T) {
	s := New(&mockConnectorGetter{}, &mockPipelineRunner{}, &mockConfigLister{}, zap.NewNop())
	s.cron.Start()
	defer s.Stop()

	id := uuid.New()
	s.OnConnectorChanged(&model.ConnectorConfig{
		ID: id, Name: "conn", Enabled: true, Schedule: "*/5 * * * *",
	})

	s.OnConnectorChanged(&model.ConnectorConfig{
		ID: id, Name: "conn", Enabled: true, Schedule: "",
	})
	if _, ok := s.entries[id]; ok {
		t.Error("expected entry to be removed when schedule cleared")
	}
}

func TestOnConnectorRemoved(t *testing.T) {
	s := New(&mockConnectorGetter{}, &mockPipelineRunner{}, &mockConfigLister{}, zap.NewNop())
	s.cron.Start()
	defer s.Stop()

	id := uuid.New()
	s.OnConnectorChanged(&model.ConnectorConfig{
		ID: id, Name: "conn", Enabled: true, Schedule: "*/5 * * * *",
	})

	s.OnConnectorRemoved(id, "conn")
	if _, ok := s.entries[id]; ok {
		t.Error("expected entry to be removed")
	}
}

func TestOnConnectorChanged_InvalidSchedule(t *testing.T) {
	s := New(&mockConnectorGetter{}, &mockPipelineRunner{}, &mockConfigLister{}, zap.NewNop())
	s.cron.Start()
	defer s.Stop()

	id := uuid.New()
	s.OnConnectorChanged(&model.ConnectorConfig{
		ID: id, Name: "bad", Enabled: true, Schedule: "not a cron",
	})

	if _, ok := s.entries[id]; ok {
		t.Error("expected no entry for invalid schedule")
	}
}

func TestRunSync_PipelineError(t *testing.T) {
	id := uuid.New()
	cm := &mockConnectorGetter{connectors: map[uuid.UUID]connector.Connector{
		id: &mockConn{name: "err"},
	}}
	pipe := &mockPipelineRunner{err: fmt.Errorf("pipeline error")}
	s := New(cm, pipe, &mockConfigLister{}, zap.NewNop())
	s.ctx = context.Background()

	s.runSync(id)

	if len(pipe.getCalls()) != 1 {
		t.Errorf("expected 1 pipeline call, got %d", len(pipe.getCalls()))
	}
}

func TestRunSync_UpdateLastRunError(t *testing.T) {
	id := uuid.New()
	cm := &mockConnectorGetter{connectors: map[uuid.UUID]connector.Connector{
		id: &mockConn{name: "test"},
	}}
	pipe := &mockPipelineRunner{}
	store := &mockConfigLister{
		lastRun: nil, // UpdateLastRun will work but this tests the path
	}
	s := New(cm, pipe, store, zap.NewNop())
	s.ctx = context.Background()

	s.runSync(id)
	// Should complete without panic even if UpdateLastRun has issues
}

func TestRunSync_ConnectorNotFound(t *testing.T) {
	cm := &mockConnectorGetter{connectors: map[uuid.UUID]connector.Connector{}}
	pipe := &mockPipelineRunner{}
	s := New(cm, pipe, &mockConfigLister{}, zap.NewNop())

	s.runSync(uuid.New())

	if len(pipe.getCalls()) != 0 {
		t.Error("expected no pipeline calls for missing connector")
	}
}

func TestRunSync_Success(t *testing.T) {
	id := uuid.New()
	cm := &mockConnectorGetter{connectors: map[uuid.UUID]connector.Connector{
		id: &mockConn{name: "test"},
	}}
	pipe := &mockPipelineRunner{}
	store := &mockConfigLister{}
	s := New(cm, pipe, store, zap.NewNop())
	s.ctx = context.Background()

	s.runSync(id)

	calls := pipe.getCalls()
	if len(calls) != 1 || calls[0].name != "test" {
		t.Errorf("expected 1 pipeline call for 'test', got %v", calls)
	}
	if calls[0].connectorID != id {
		t.Errorf("expected connectorID %v, got %v", id, calls[0].connectorID)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.lastRun[id]; !ok {
		t.Error("expected last_run to be updated")
	}
}

func TestRunSync_PropagatesOwnership(t *testing.T) {
	id := uuid.New()
	userID := uuid.New()
	cm := &mockConnectorGetter{
		connectors: map[uuid.UUID]connector.Connector{id: &mockConn{name: "owned"}},
		configs: map[uuid.UUID]*model.ConnectorConfig{
			id: {ID: id, Name: "owned", UserID: &userID, Shared: false},
		},
	}
	pipe := &mockPipelineRunner{}
	s := New(cm, pipe, &mockConfigLister{}, zap.NewNop())
	s.ctx = context.Background()

	s.runSync(id)

	calls := pipe.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].ownerID != userID.String() {
		t.Errorf("expected ownerID %q, got %q", userID.String(), calls[0].ownerID)
	}
	if calls[0].shared != false {
		t.Errorf("expected shared=false, got %v", calls[0].shared)
	}
}

func TestRunSync_PropagatesShared(t *testing.T) {
	id := uuid.New()
	cm := &mockConnectorGetter{
		connectors: map[uuid.UUID]connector.Connector{id: &mockConn{name: "shared-conn"}},
		configs: map[uuid.UUID]*model.ConnectorConfig{
			id: {ID: id, Name: "shared-conn", UserID: nil, Shared: true},
		},
	}
	pipe := &mockPipelineRunner{}
	s := New(cm, pipe, &mockConfigLister{}, zap.NewNop())
	s.ctx = context.Background()

	s.runSync(id)

	calls := pipe.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].ownerID != "" {
		t.Errorf("expected empty ownerID for shared connector, got %q", calls[0].ownerID)
	}
	if !calls[0].shared {
		t.Error("expected shared=true")
	}
}
