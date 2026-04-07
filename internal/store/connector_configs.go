package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/muty/nexus/internal/model"
)

var ErrDuplicateName = errors.New("connector name already exists")

func (s *Store) ListConnectorConfigs(ctx context.Context) ([]model.ConnectorConfig, error) {
	query := `SELECT id, type, name, config, enabled, created_at, updated_at FROM connector_configs ORDER BY name`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list connector configs: %w", err)
	}
	defer rows.Close()

	var configs []model.ConnectorConfig
	for rows.Next() {
		var cfg model.ConnectorConfig
		var configJSON []byte
		err := rows.Scan(&cfg.ID, &cfg.Type, &cfg.Name, &configJSON, &cfg.Enabled, &cfg.CreatedAt, &cfg.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("store: scan connector config: %w", err)
		}
		if err := json.Unmarshal(configJSON, &cfg.Config); err != nil {
			return nil, fmt.Errorf("store: unmarshal connector config: %w", err)
		}
		configs = append(configs, cfg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list connector configs rows: %w", err)
	}

	if configs == nil {
		configs = []model.ConnectorConfig{}
	}
	return configs, nil
}

func (s *Store) GetConnectorConfig(ctx context.Context, id uuid.UUID) (*model.ConnectorConfig, error) {
	query := `SELECT id, type, name, config, enabled, created_at, updated_at FROM connector_configs WHERE id = $1`

	var cfg model.ConnectorConfig
	var configJSON []byte
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&cfg.ID, &cfg.Type, &cfg.Name, &configJSON, &cfg.Enabled, &cfg.CreatedAt, &cfg.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get connector config: %w", err)
	}
	if err := json.Unmarshal(configJSON, &cfg.Config); err != nil {
		return nil, fmt.Errorf("store: unmarshal connector config: %w", err)
	}
	return &cfg, nil
}

func (s *Store) CreateConnectorConfig(ctx context.Context, cfg *model.ConnectorConfig) error {
	if cfg.ID == uuid.Nil {
		cfg.ID = uuid.New()
	}
	now := time.Now()
	cfg.CreatedAt = now
	cfg.UpdatedAt = now

	configJSON, err := json.Marshal(cfg.Config)
	if err != nil {
		return fmt.Errorf("store: marshal connector config: %w", err)
	}

	query := `INSERT INTO connector_configs (id, type, name, config, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err = s.pool.Exec(ctx, query,
		cfg.ID, cfg.Type, cfg.Name, configJSON, cfg.Enabled, cfg.CreatedAt, cfg.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicateName
		}
		return fmt.Errorf("store: create connector config: %w", err)
	}
	return nil
}

func (s *Store) UpdateConnectorConfig(ctx context.Context, cfg *model.ConnectorConfig) error {
	cfg.UpdatedAt = time.Now()

	configJSON, err := json.Marshal(cfg.Config)
	if err != nil {
		return fmt.Errorf("store: marshal connector config: %w", err)
	}

	query := `UPDATE connector_configs SET type = $1, name = $2, config = $3, enabled = $4, updated_at = $5 WHERE id = $6`

	result, err := s.pool.Exec(ctx, query,
		cfg.Type, cfg.Name, configJSON, cfg.Enabled, cfg.UpdatedAt, cfg.ID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicateName
		}
		return fmt.Errorf("store: update connector config: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteConnectorConfig(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM connector_configs WHERE id = $1`

	result, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("store: delete connector config: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
