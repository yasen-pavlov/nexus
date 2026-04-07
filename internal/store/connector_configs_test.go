//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

func TestListConnectorConfigs_Empty(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	configs, err := st.ListConnectorConfigs(ctx)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if configs == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(configs) != 0 {
		t.Errorf("expected 0 configs, got %d", len(configs))
	}
}

func TestCreateAndGetConnectorConfig(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type:     "filesystem",
		Name:     "my-files",
		Config:   map[string]any{"root_path": "/data", "patterns": "*.txt"},
		Enabled:  true,
		Schedule: "*/30 * * * *",
	}

	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if cfg.ID == uuid.Nil {
		t.Error("expected ID to be set")
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Name != "my-files" {
		t.Errorf("expected name 'my-files', got %q", got.Name)
	}
	if got.Type != "filesystem" {
		t.Errorf("expected type 'filesystem', got %q", got.Type)
	}
	if got.Config["root_path"] != "/data" {
		t.Errorf("expected root_path '/data', got %v", got.Config["root_path"])
	}
	if !got.Enabled {
		t.Error("expected enabled to be true")
	}
}

func TestCreateConnectorConfig_DuplicateName(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg1 := &model.ConnectorConfig{Type: "filesystem", Name: "dupe", Config: map[string]any{}, Enabled: true}
	cfg2 := &model.ConnectorConfig{Type: "filesystem", Name: "dupe", Config: map[string]any{}, Enabled: true}

	if err := st.CreateConnectorConfig(ctx, cfg1); err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	err := st.CreateConnectorConfig(ctx, cfg2)
	if err != ErrDuplicateName {
		t.Errorf("expected ErrDuplicateName, got %v", err)
	}
}

func TestGetConnectorConfig_NotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_, err := st.GetConnectorConfig(ctx, uuid.New())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateConnectorConfig(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "update-test", Config: map[string]any{"root_path": "/old"}, Enabled: true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	cfg.Name = "updated-name"
	cfg.Config = map[string]any{"root_path": "/new"}
	cfg.Enabled = false

	if err := st.UpdateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Name != "updated-name" {
		t.Errorf("expected name 'updated-name', got %q", got.Name)
	}
	if got.Config["root_path"] != "/new" {
		t.Errorf("expected root_path '/new', got %v", got.Config["root_path"])
	}
	if got.Enabled {
		t.Error("expected enabled to be false")
	}
}

func TestUpdateConnectorConfig_NotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{ID: uuid.New(), Type: "filesystem", Name: "nope", Config: map[string]any{}}
	err := st.UpdateConnectorConfig(ctx, cfg)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateConnectorConfig_DuplicateName(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg1 := &model.ConnectorConfig{Type: "filesystem", Name: "first", Config: map[string]any{}, Enabled: true}
	cfg2 := &model.ConnectorConfig{Type: "filesystem", Name: "second", Config: map[string]any{}, Enabled: true}
	if err := st.CreateConnectorConfig(ctx, cfg1); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateConnectorConfig(ctx, cfg2); err != nil {
		t.Fatal(err)
	}

	cfg2.Name = "first" // try to rename to existing name
	err := st.UpdateConnectorConfig(ctx, cfg2)
	if err != ErrDuplicateName {
		t.Errorf("expected ErrDuplicateName, got %v", err)
	}
}

func TestDeleteConnectorConfig(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{Type: "filesystem", Name: "delete-me", Config: map[string]any{}, Enabled: true}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteConnectorConfig(ctx, cfg.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	_, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteConnectorConfig_NotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	err := st.DeleteConnectorConfig(ctx, uuid.New())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListConnectorConfigs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"bravo", "alpha", "charlie"} {
		cfg := &model.ConnectorConfig{Type: "filesystem", Name: name, Config: map[string]any{}, Enabled: true}
		if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
			t.Fatal(err)
		}
	}

	configs, err := st.ListConnectorConfigs(ctx)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(configs) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(configs))
	}
	// Should be ordered by name
	if configs[0].Name != "alpha" {
		t.Errorf("expected first config to be 'alpha', got %q", configs[0].Name)
	}
}

func TestScheduleRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "sched-test", Config: map[string]any{},
		Enabled: true, Schedule: "*/15 * * * *",
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schedule != "*/15 * * * *" {
		t.Errorf("expected schedule '*/15 * * * *', got %q", got.Schedule)
	}
	if got.LastRun != nil {
		t.Error("expected nil last_run")
	}
}

func TestUpdateLastRun(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "lastrun-test", Config: map[string]any{}, Enabled: true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Microsecond)
	if err := st.UpdateLastRun(ctx, cfg.ID, now); err != nil {
		t.Fatalf("update last_run failed: %v", err)
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRun == nil {
		t.Fatal("expected last_run to be set")
	}
	if !got.LastRun.Truncate(time.Microsecond).Equal(now) {
		t.Errorf("expected last_run %v, got %v", now, *got.LastRun)
	}
}
