// Package store provides the database access layer for application state.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"go.uber.org/zap"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

type Store struct {
	pool          *pgxpool.Pool
	log           *zap.Logger
	encryptionKey []byte // AES-256 key for encrypting sensitive config fields; nil = disabled
}

func New(ctx context.Context, databaseURL string, log *zap.Logger) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool, log: log}, nil
}

// SetEncryptionKey sets the key used to encrypt/decrypt sensitive connector config fields.
func (s *Store) SetEncryptionKey(key []byte) {
	s.encryptionKey = key
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) RunMigrations(databaseURL string, migrationsFS fs.FS) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("store: open for migrations: %w", err)
	}
	defer db.Close() //nolint:errcheck // closing after migrations

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("store: set dialect: %w", err)
	}
	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("store: run migrations: %w", err)
	}
	s.log.Info("migrations completed")
	return nil
}
