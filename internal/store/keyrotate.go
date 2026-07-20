package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/muty/nexus/internal/crypto"
)

// RotateReport summarizes how many rows were re-encrypted during a key rotation.
type RotateReport struct {
	Connectors int
	Settings   int
}

// RotateEncryptionKey re-encrypts every at-rest secret — connector config
// fields and sensitive settings (API keys + Telegram session blobs) — from
// oldKey to newKey inside a single transaction. It is all-or-nothing: if any
// value fails to decrypt with oldKey the whole rotation rolls back, so a
// partial rotation can never leave the store half-readable under either key.
// Values that aren't encrypted (plaintext/legacy) are skipped.
func (s *Store) RotateEncryptionKey(ctx context.Context, oldKey, newKey []byte) (RotateReport, error) {
	var report RotateReport

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return report, fmt.Errorf("store: begin key rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if report.Connectors, err = rotateConnectorConfigs(ctx, tx, oldKey, newKey); err != nil {
		return report, err
	}
	if report.Settings, err = rotateSettings(ctx, tx, oldKey, newKey); err != nil {
		return report, err
	}

	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("store: commit key rotation: %w", err)
	}
	return report, nil
}

// reencryptValue decrypts val with oldKey and re-encrypts it with newKey.
func reencryptValue(val string, oldKey, newKey []byte) (string, error) {
	plain, err := crypto.Decrypt(oldKey, val)
	if err != nil {
		return "", fmt.Errorf("decrypt with old key: %w", err)
	}
	reenc, err := crypto.Encrypt(newKey, plain)
	if err != nil {
		return "", fmt.Errorf("re-encrypt: %w", err)
	}
	return reenc, nil
}

// reencryptFields re-encrypts each named field of config in place, skipping
// fields that are missing, empty, or not encrypted. Returns whether anything
// changed.
func reencryptFields(config map[string]any, fields []string, oldKey, newKey []byte) (bool, error) {
	changed := false
	for _, field := range fields {
		val, ok := config[field].(string)
		if !ok || val == "" || !crypto.IsEncrypted(val) {
			continue
		}
		reenc, err := reencryptValue(val, oldKey, newKey)
		if err != nil {
			return false, fmt.Errorf("field %q: %w", field, err)
		}
		config[field] = reenc
		changed = true
	}
	return changed, nil
}

type rawRotateConfig struct {
	id     uuid.UUID
	typ    string
	config map[string]any
}

// loadConnectorConfigsForRotation reads every connector config row within tx.
func loadConnectorConfigsForRotation(ctx context.Context, tx pgx.Tx) ([]rawRotateConfig, error) {
	rows, err := tx.Query(ctx, `SELECT id, type, config FROM connector_configs`)
	if err != nil {
		return nil, fmt.Errorf("store: list configs for rotation: %w", err)
	}
	defer rows.Close()

	var out []rawRotateConfig
	for rows.Next() {
		var c rawRotateConfig
		var configJSON []byte
		if err := rows.Scan(&c.id, &c.typ, &configJSON); err != nil {
			return nil, fmt.Errorf("store: scan config for rotation: %w", err)
		}
		if err := json.Unmarshal(configJSON, &c.config); err != nil {
			return nil, fmt.Errorf("store: unmarshal config for rotation: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// rotateConnectorConfigs re-encrypts the sensitive fields of every connector
// config within tx, returning how many rows changed.
func rotateConnectorConfigs(ctx context.Context, tx pgx.Tx, oldKey, newKey []byte) (int, error) {
	configs, err := loadConnectorConfigsForRotation(ctx, tx)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, rc := range configs {
		changed, err := reencryptFields(rc.config, crypto.SensitiveFields[rc.typ], oldKey, newKey)
		if err != nil {
			return 0, fmt.Errorf("store: connector %s: %w", rc.id, err)
		}
		if !changed {
			continue
		}
		configJSON, err := json.Marshal(rc.config)
		if err != nil {
			return 0, fmt.Errorf("store: marshal rotated config %s: %w", rc.id, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE connector_configs SET config = $1 WHERE id = $2`, configJSON, rc.id); err != nil {
			return 0, fmt.Errorf("store: update rotated config %s: %w", rc.id, err)
		}
		count++
	}
	return count, nil
}

type rawRotateSetting struct{ key, value string }

// loadSensitiveSettingsForRotation reads every encrypted sensitive setting
// within tx (skipping plaintext/legacy and non-sensitive keys).
func loadSensitiveSettingsForRotation(ctx context.Context, tx pgx.Tx) ([]rawRotateSetting, error) {
	rows, err := tx.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("store: list settings for rotation: %w", err)
	}
	defer rows.Close()

	var out []rawRotateSetting
	for rows.Next() {
		var s rawRotateSetting
		if err := rows.Scan(&s.key, &s.value); err != nil {
			return nil, fmt.Errorf("store: scan setting for rotation: %w", err)
		}
		if !crypto.IsSensitiveSettingsKey(s.key) || s.value == "" || !crypto.IsEncrypted(s.value) {
			continue
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// rotateSettings re-encrypts every sensitive setting within tx, returning how
// many rows changed.
func rotateSettings(ctx context.Context, tx pgx.Tx, oldKey, newKey []byte) (int, error) {
	settings, err := loadSensitiveSettingsForRotation(ctx, tx)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, row := range settings {
		reenc, err := reencryptValue(row.value, oldKey, newKey)
		if err != nil {
			return 0, fmt.Errorf("store: setting %q: %w", row.key, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE settings SET value = $1 WHERE key = $2`, reenc, row.key); err != nil {
			return 0, fmt.Errorf("store: update rotated setting %q: %w", row.key, err)
		}
		count++
	}
	return count, nil
}
