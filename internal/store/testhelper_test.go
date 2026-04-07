//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/muty/nexus/internal/testutil"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	tdb := testutil.NewTestDB(t, "store", migrations.FS)

	return &Store{pool: tdb.Pool, log: zap.NewNop()}
}

// newTestStoreWithURL returns a store and its database URL (for migration tests).
func newTestStoreWithURL(t *testing.T) (*Store, string) {
	t.Helper()

	tdb := testutil.NewTestDB(t, "store", migrations.FS)

	return &Store{pool: tdb.Pool, log: zap.NewNop()}, tdb.URL
}

// newClosedStore returns a store whose pool has been closed (for error path tests).
func newClosedStore(t *testing.T) *Store {
	t.Helper()

	ctx := context.Background()
	tdb := testutil.NewTestDB(t, "store_closed", migrations.FS)
	tdb.Pool.Close()

	// Re-create a Store with the closed pool
	_ = ctx
	return &Store{pool: tdb.Pool, log: zap.NewNop()}
}
