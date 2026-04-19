//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

func TestNew_Success(t *testing.T) {
	st := newTestStore(t)
	if st == nil {
		t.Fatal("expected store to be non-nil")
	}
}

func TestNew_InvalidURL(t *testing.T) {
	ctx := context.Background()
	_, err := New(ctx, "postgres://invalid:invalid@localhost:59999/nonexistent?sslmode=disable&connect_timeout=1", zap.NewNop())
	if err == nil {
		t.Fatal("expected error for invalid connection")
	}
}

func TestRunMigrations_Success(t *testing.T) {
	st, url := newTestStoreWithURL(t)

	// Migrations already ran via testutil, but re-running should be a no-op success
	if err := st.RunMigrations(url, migrations.FS); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
}

func TestRunMigrations_InvalidURL(t *testing.T) {
	st := newTestStore(t)

	err := st.RunMigrations("postgres://invalid:invalid@localhost:59999/bad?sslmode=disable&connect_timeout=1", migrations.FS)
	if err == nil {
		t.Fatal("expected error for invalid migration URL")
	}
}
