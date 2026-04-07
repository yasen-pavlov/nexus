package store

import (
	"context"
	"os"
	"testing"

	"go.uber.org/zap"
)

func getTestDBURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("NEXUS_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://nexus:nexus@localhost:5432/nexus?sslmode=disable"
	}
	return url
}

func TestPool_ReturnsPool(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, getTestDBURL(t), zap.NewNop())
	if err != nil {
		t.Skip("database not available, skipping")
	}
	defer st.Close()

	if st.pool == nil {
		t.Fatal("expected pool to be non-nil")
	}

	var result int
	err = st.pool.QueryRow(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("pool query failed: %v", err)
	}
	if result != 1 {
		t.Errorf("expected 1, got %d", result)
	}
}

func TestClose_ClosesPool(t *testing.T) {
	ctx := context.Background()
	st, err := New(ctx, getTestDBURL(t), zap.NewNop())
	if err != nil {
		t.Skip("database not available, skipping")
	}

	st.Close()

	err = st.pool.Ping(ctx)
	if err == nil {
		t.Error("expected error after close")
	}
}
