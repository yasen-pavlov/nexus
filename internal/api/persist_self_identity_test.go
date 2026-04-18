//go:build integration

package api

import (
	"context"
	"testing"

	"github.com/muty/nexus/internal/model"
)

func TestPersistSelfIdentity_WritesExternalFields(t *testing.T) {
	st, _, cm := newTestDeps(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "persist-identity",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"},
		Enabled: true, Shared: true,
	}
	if err := cm.Add(ctx, cfg); err != nil {
		t.Fatalf("add connector: %v", err)
	}

	if err := persistSelfIdentity(ctx, cm, cfg, 9001, "Alice"); err != nil {
		t.Fatalf("persist: %v", err)
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.ExternalID != "9001" || got.ExternalName != "Alice" {
		t.Errorf("expected 9001 + Alice, got %q + %q", got.ExternalID, got.ExternalName)
	}
}

func TestPersistSelfIdentity_NoopOnZeroID(t *testing.T) {
	st, _, cm := newTestDeps(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "no-op",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"},
		Enabled: true, Shared: true,
	}
	if err := cm.Add(ctx, cfg); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := persistSelfIdentity(ctx, cm, cfg, 0, "ignored"); err != nil {
		t.Errorf("expected nil error for zero selfID, got %v", err)
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.ExternalID != "" || got.ExternalName != "" {
		t.Errorf("zero selfID should not populate identity fields: %+v", got)
	}
}

func TestPersistSelfIdentity_KeepsExistingNameWhenNewIsEmpty(t *testing.T) {
	st, _, cm := newTestDeps(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "keep-name",
		Config: map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"},
		Enabled: true, Shared: true,
		ExternalName: "Previous Name",
	}
	if err := cm.Add(ctx, cfg); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := persistSelfIdentity(ctx, cm, cfg, 9001, ""); err != nil {
		t.Fatalf("persist: %v", err)
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.ExternalID != "9001" {
		t.Errorf("external_id = %q, want 9001", got.ExternalID)
	}
	if got.ExternalName != "Previous Name" {
		t.Errorf("external_name should be unchanged when new is empty, got %q", got.ExternalName)
	}
}
