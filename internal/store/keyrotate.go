package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
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

	// --- connector_configs ---
	type rawConfig struct {
		id     uuid.UUID
		typ    string
		config map[string]any
	}
	var configs []rawConfig
	rows, err := tx.Query(ctx, `SELECT id, type, config FROM connector_configs`)
	if err != nil {
		return report, fmt.Errorf("store: list configs for rotation: %w", err)
	}
	for rows.Next() {
		var id uuid.UUID
		var typ string
		var configJSON []byte
		if err := rows.Scan(&id, &typ, &configJSON); err != nil {
			rows.Close()
			return report, fmt.Errorf("store: scan config for rotation: %w", err)
		}
		var config map[string]any
		if err := json.Unmarshal(configJSON, &config); err != nil {
			rows.Close()
			return report, fmt.Errorf("store: unmarshal config for rotation: %w", err)
		}
		configs = append(configs, rawConfig{id: id, typ: typ, config: config})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("store: config rows for rotation: %w", err)
	}

	for _, rc := range configs {
		changed := false
		for _, field := range crypto.SensitiveFields[rc.typ] {
			val, ok := rc.config[field].(string)
			if !ok || val == "" || !crypto.IsEncrypted(val) {
				continue
			}
			plain, err := crypto.Decrypt(oldKey, val)
			if err != nil {
				return report, fmt.Errorf("store: decrypt connector %s field %q with old key: %w", rc.id, field, err)
			}
			reenc, err := crypto.Encrypt(newKey, plain)
			if err != nil {
				return report, fmt.Errorf("store: re-encrypt connector %s field %q: %w", rc.id, field, err)
			}
			rc.config[field] = reenc
			changed = true
		}
		if !changed {
			continue
		}
		configJSON, err := json.Marshal(rc.config)
		if err != nil {
			return report, fmt.Errorf("store: marshal rotated config %s: %w", rc.id, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE connector_configs SET config = $1 WHERE id = $2`, configJSON, rc.id); err != nil {
			return report, fmt.Errorf("store: update rotated config %s: %w", rc.id, err)
		}
		report.Connectors++
	}

	// --- settings ---
	type kv struct{ k, v string }
	var settings []kv
	srows, err := tx.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return report, fmt.Errorf("store: list settings for rotation: %w", err)
	}
	for srows.Next() {
		var k, v string
		if err := srows.Scan(&k, &v); err != nil {
			srows.Close()
			return report, fmt.Errorf("store: scan setting for rotation: %w", err)
		}
		if !crypto.IsSensitiveSettingsKey(k) || v == "" || !crypto.IsEncrypted(v) {
			continue
		}
		settings = append(settings, kv{k, v})
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return report, fmt.Errorf("store: settings rows for rotation: %w", err)
	}

	for _, row := range settings {
		plain, err := crypto.Decrypt(oldKey, row.v)
		if err != nil {
			return report, fmt.Errorf("store: decrypt setting %q with old key: %w", row.k, err)
		}
		reenc, err := crypto.Encrypt(newKey, plain)
		if err != nil {
			return report, fmt.Errorf("store: re-encrypt setting %q: %w", row.k, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE settings SET value = $1 WHERE key = $2`, reenc, row.k); err != nil {
			return report, fmt.Errorf("store: update rotated setting %q: %w", row.k, err)
		}
		report.Settings++
	}

	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("store: commit key rotation: %w", err)
	}
	return report, nil
}
