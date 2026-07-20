//go:build integration

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/crypto"
	"github.com/muty/nexus/internal/testutil"
	"github.com/muty/nexus/migrations"
	"go.uber.org/zap"
)

// testKey returns a deterministic 32-byte AES key built from a single hex
// nibble (e.g. "a" → 0xaa…). Distinct nibbles give distinct keys, so tests can
// seed under one key and read under another.
func testKey(t *testing.T, nibble string) []byte {
	t.Helper()
	key, err := crypto.NewKey(strings.Repeat(nibble, 64))
	if err != nil {
		t.Fatalf("test key %q: %v", nibble, err)
	}
	return key
}

// newSharedPoolStores returns two stores backed by the SAME database pool but
// configured with different encryption keys — for testing the decrypt-degrade
// and key-rotation paths where one key can't read data written under another.
func newSharedPoolStores(t *testing.T, keyA, keyB []byte) (*Store, *Store) {
	t.Helper()
	stores := newSharedPoolStoresN(t, keyA, keyB)
	return stores[0], stores[1]
}

// newSharedPoolStoresN returns N stores over one shared pool, each with the
// corresponding key — used when a test needs three keys (e.g. a partial-failure
// rotation where one row is encrypted under a third, unrelated key).
func newSharedPoolStoresN(t *testing.T, keys ...[]byte) []*Store {
	t.Helper()
	tdb := testutil.NewTestDB(t, "store", migrations.FS)
	stores := make([]*Store, len(keys))
	for i, k := range keys {
		stores[i] = &Store{pool: tdb.Pool, log: zap.NewNop(), encryptionKey: k}
	}
	return stores
}

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
