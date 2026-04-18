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
	"github.com/muty/nexus/internal/crypto"
	"github.com/muty/nexus/internal/model"
)

var ErrDuplicateName = errors.New("connector name already exists")

const connectorCols = `id, type, name, config, enabled, schedule, shared, user_id, external_id, external_name, last_run, created_at, updated_at`

func (s *Store) scanConnectorConfig(scan func(dest ...any) error) (model.ConnectorConfig, error) {
	var cfg model.ConnectorConfig
	var configJSON []byte
	err := scan(
		&cfg.ID, &cfg.Type, &cfg.Name, &configJSON, &cfg.Enabled, &cfg.Schedule,
		&cfg.Shared, &cfg.UserID, &cfg.ExternalID, &cfg.ExternalName,
		&cfg.LastRun, &cfg.CreatedAt, &cfg.UpdatedAt,
	)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(configJSON, &cfg.Config); err != nil {
		return cfg, fmt.Errorf("store: unmarshal connector config: %w", err)
	}
	decrypted, err := crypto.DecryptConfig(s.encryptionKey, cfg.Type, cfg.Config)
	if err != nil {
		return cfg, fmt.Errorf("store: decrypt connector config %q: %w", cfg.Name, err)
	}
	cfg.Config = decrypted
	return cfg, nil
}

// ListConnectorConfigs returns all connector configs (for scheduler, which needs all users' connectors).
func (s *Store) ListConnectorConfigs(ctx context.Context) ([]model.ConnectorConfig, error) {
	query := `SELECT ` + connectorCols + ` FROM connector_configs ORDER BY name`
	return s.listConnectorConfigs(ctx, query)
}

// ListUserConnectorConfigs returns connectors owned by the given user plus shared connectors.
func (s *Store) ListUserConnectorConfigs(ctx context.Context, userID uuid.UUID) ([]model.ConnectorConfig, error) {
	query := `SELECT ` + connectorCols + ` FROM connector_configs WHERE user_id = $1 OR shared = true ORDER BY name`
	return s.listConnectorConfigsWithArg(ctx, query, userID)
}

func (s *Store) listConnectorConfigs(ctx context.Context, query string) ([]model.ConnectorConfig, error) {
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list connector configs: %w", err)
	}
	defer rows.Close()

	var configs []model.ConnectorConfig
	for rows.Next() {
		cfg, err := s.scanConnectorConfig(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: scan connector config: %w", err)
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

func (s *Store) listConnectorConfigsWithArg(ctx context.Context, query string, arg any) ([]model.ConnectorConfig, error) {
	rows, err := s.pool.Query(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("store: list connector configs: %w", err)
	}
	defer rows.Close()

	var configs []model.ConnectorConfig
	for rows.Next() {
		cfg, err := s.scanConnectorConfig(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: scan connector config: %w", err)
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
	query := `SELECT ` + connectorCols + ` FROM connector_configs WHERE id = $1`

	cfg, err := s.scanConnectorConfig(func(dest ...any) error {
		return s.pool.QueryRow(ctx, query, id).Scan(dest...)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get connector config: %w", err)
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

	encConfig, err := crypto.EncryptConfig(s.encryptionKey, cfg.Type, cfg.Config)
	if err != nil {
		return fmt.Errorf("store: encrypt connector config: %w", err)
	}
	configJSON, err := json.Marshal(encConfig)
	if err != nil {
		return fmt.Errorf("store: marshal connector config: %w", err)
	}

	query := `INSERT INTO connector_configs (id, type, name, config, enabled, schedule, shared, user_id, external_id, external_name, last_run, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`

	_, err = s.pool.Exec(ctx, query,
		cfg.ID, cfg.Type, cfg.Name, configJSON, cfg.Enabled, cfg.Schedule,
		cfg.Shared, cfg.UserID, cfg.ExternalID, cfg.ExternalName,
		cfg.LastRun, cfg.CreatedAt, cfg.UpdatedAt,
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

	encConfig, err := crypto.EncryptConfig(s.encryptionKey, cfg.Type, cfg.Config)
	if err != nil {
		return fmt.Errorf("store: encrypt connector config: %w", err)
	}
	configJSON, err := json.Marshal(encConfig)
	if err != nil {
		return fmt.Errorf("store: marshal connector config: %w", err)
	}

	query := `UPDATE connector_configs SET type = $1, name = $2, config = $3, enabled = $4, schedule = $5, shared = $6, external_id = $7, external_name = $8, updated_at = $9 WHERE id = $10`

	result, err := s.pool.Exec(ctx, query,
		cfg.Type, cfg.Name, configJSON, cfg.Enabled, cfg.Schedule, cfg.Shared,
		cfg.ExternalID, cfg.ExternalName, cfg.UpdatedAt, cfg.ID,
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

func (s *Store) UpdateLastRun(ctx context.Context, id uuid.UUID, t time.Time) error {
	query := `UPDATE connector_configs SET last_run = $1 WHERE id = $2`
	_, err := s.pool.Exec(ctx, query, t, id)
	if err != nil {
		return fmt.Errorf("store: update last_run: %w", err)
	}
	return nil
}

// EncryptExistingConfigs encrypts any sensitive config fields that are still stored as plaintext.
func (s *Store) EncryptExistingConfigs(ctx context.Context) (int, error) {
	if s.encryptionKey == nil {
		return 0, nil
	}

	query := `SELECT id, type, config FROM connector_configs`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("store: list configs for encryption: %w", err)
	}
	defer rows.Close()

	type rawConfig struct {
		id       uuid.UUID
		connType string
		config   map[string]any
	}
	var toEncrypt []rawConfig

	for rows.Next() {
		var id uuid.UUID
		var connType string
		var configJSON []byte
		if err := rows.Scan(&id, &connType, &configJSON); err != nil {
			return 0, fmt.Errorf("store: scan config for encryption: %w", err)
		}
		var config map[string]any
		if err := json.Unmarshal(configJSON, &config); err != nil {
			return 0, fmt.Errorf("store: unmarshal config for encryption: %w", err)
		}

		fields := crypto.SensitiveFields[connType]
		needsEncrypt := false
		for _, field := range fields {
			if val, ok := config[field].(string); ok && val != "" && !crypto.IsEncrypted(val) {
				needsEncrypt = true
				break
			}
		}
		if needsEncrypt {
			toEncrypt = append(toEncrypt, rawConfig{id: id, connType: connType, config: config})
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: rows error: %w", err)
	}

	for _, rc := range toEncrypt {
		encrypted, err := crypto.EncryptConfig(s.encryptionKey, rc.connType, rc.config)
		if err != nil {
			return 0, fmt.Errorf("store: encrypt config %s: %w", rc.id, err)
		}
		configJSON, err := json.Marshal(encrypted)
		if err != nil {
			return 0, fmt.Errorf("store: marshal encrypted config: %w", err)
		}
		_, err = s.pool.Exec(ctx, `UPDATE connector_configs SET config = $1 WHERE id = $2`, configJSON, rc.id)
		if err != nil {
			return 0, fmt.Errorf("store: update encrypted config %s: %w", rc.id, err)
		}
	}

	return len(toEncrypt), nil
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
