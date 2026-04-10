// Package testutil provides shared test helpers.
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

var (
	mu      sync.Mutex
	created = map[string]string{} // pkgName -> database URL
)

func baseURL() string {
	url := os.Getenv("NEXUS_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://nexus:nexus@localhost:5432/nexus?sslmode=disable"
	}
	return url
}

// TestDB holds a test database connection pool and its URL.
type TestDB struct {
	Pool *pgxpool.Pool
	URL  string
}

// NewTestDB creates an isolated per-package test database and returns a connection pool.
// The database is created once per package and tables are truncated on each call.
// pkgName should be a short identifier like "store", "pipeline", "api".
func NewTestDB(t *testing.T, pkgName string, migrationsFS fs.FS) *TestDB {
	t.Helper()

	dbURL := getOrCreateDB(t, pkgName, migrationsFS)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}

	_, err = pool.Exec(ctx, "TRUNCATE sync_cursors, jobs, connector_configs, settings, users CASCADE")
	if err != nil {
		pool.Close()
		t.Fatalf("testdb: truncate: %v", err)
	}

	t.Cleanup(func() { pool.Close() })
	return &TestDB{Pool: pool, URL: dbURL}
}

func getOrCreateDB(t *testing.T, pkgName string, migrationsFS fs.FS) string {
	t.Helper()

	mu.Lock()
	defer mu.Unlock()

	if url, ok := created[pkgName]; ok {
		return url
	}

	base := baseURL()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("testdb: connect to base: %v", err)
	}
	defer conn.Close(ctx) //nolint:errcheck // test cleanup

	dbName := "nexus_test_" + pkgName

	_, _ = conn.Exec(ctx, fmt.Sprintf(
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()", dbName))
	_, _ = conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName))

	_, err = conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName))
	if err != nil {
		t.Fatalf("testdb: create %s: %v", dbName, err)
	}

	dbURL := replaceDBName(base, dbName)

	// Run migrations
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("testdb: open for migrations: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("testdb: set dialect: %v", err)
	}
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("testdb: migrations on %s: %v", dbName, err)
	}

	created[pkgName] = dbURL
	return dbURL
}

func replaceDBName(url, dbName string) string {
	lastSlash := strings.LastIndex(url, "/")
	if lastSlash == -1 {
		return url
	}
	base := url[:lastSlash+1]
	params := ""
	if qmark := strings.Index(url[lastSlash:], "?"); qmark != -1 {
		params = url[lastSlash+qmark:]
	}
	return base + dbName + params
}
