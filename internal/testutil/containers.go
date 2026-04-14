package testutil

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/opensearch"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	pgImage = "pgvector/pgvector:pg17"
	osImage = "opensearchproject/opensearch:2"
)

var (
	pgOnce sync.Once
	pgURL  string
	pgErr  error

	osOnce sync.Once
	osURL  string
	osErr  error
)

// baseDatabaseURL returns a base Postgres URL usable by NewTestDB.
// If NEXUS_TEST_DATABASE_URL is set, it's used verbatim (fast path for
// developers running `make dev-db`). Otherwise a pgvector container is
// started on first call and reused for the rest of the test process.
func baseDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("NEXUS_TEST_DATABASE_URL"); url != "" {
		return url
	}
	pgOnce.Do(func() {
		ctx := context.Background()
		ctr, err := postgres.Run(ctx, pgImage,
			postgres.WithDatabase("nexus"),
			postgres.WithUsername("nexus"),
			postgres.WithPassword("nexus"),
			postgres.BasicWaitStrategies(),
		)
		if err != nil {
			pgErr = err
			return
		}
		pgURL, pgErr = ctr.ConnectionString(ctx, "sslmode=disable")
	})
	if pgErr != nil {
		t.Fatalf("testutil: start postgres container: %v", pgErr)
	}
	return pgURL
}

// baseOpenSearchURL returns an OpenSearch base URL usable by TestOSConfig.
// Same env-var fast path as baseDatabaseURL.
func baseOpenSearchURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("NEXUS_TEST_OPENSEARCH_URL"); url != "" {
		return url
	}
	osOnce.Do(func() {
		ctx := context.Background()
		ctr, err := opensearch.Run(ctx, osImage)
		if err != nil {
			osErr = err
			return
		}
		osURL, osErr = ctr.Address(ctx)
	})
	if osErr != nil {
		t.Fatalf("testutil: start opensearch container: %v", osErr)
	}
	return osURL
}
