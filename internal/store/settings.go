package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/muty/nexus/internal/crypto"
)

// encryptSettingValue returns the value to write to the DB for the given key.
// Sensitive keys are AES-256-GCM encrypted when an encryption key is configured.
// Already-encrypted values are passed through unchanged.
func (s *Store) encryptSettingValue(key, value string) (string, error) {
	if s.encryptionKey == nil || value == "" || !crypto.IsSensitiveSettingsKey(key) {
		return value, nil
	}
	if crypto.IsEncrypted(value) {
		return value, nil
	}
	return crypto.Encrypt(s.encryptionKey, value)
}

// decryptSettingValue is the inverse of encryptSettingValue. Plaintext values
// (legacy or non-sensitive) are returned unchanged.
func (s *Store) decryptSettingValue(key, value string) (string, error) {
	if s.encryptionKey == nil || value == "" || !crypto.IsSensitiveSettingsKey(key) {
		return value, nil
	}
	if !crypto.IsEncrypted(value) {
		return value, nil
	}
	return crypto.Decrypt(s.encryptionKey, value)
}

// GetSetting returns the value of a setting by key. Returns empty string if not found.
// Sensitive values are decrypted on read.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("store: get setting: %w", err)
	}
	decrypted, err := s.decryptSettingValue(key, value)
	if err != nil {
		return "", fmt.Errorf("store: decrypt setting %q: %w", key, err)
	}
	return decrypted, nil
}

// SetSetting upserts a setting. Sensitive values are encrypted before insert.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	stored, err := s.encryptSettingValue(key, value)
	if err != nil {
		return fmt.Errorf("store: encrypt setting %q: %w", key, err)
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, stored,
	)
	if err != nil {
		return fmt.Errorf("store: set setting: %w", err)
	}
	return nil
}

// GetSettings returns multiple settings by keys. Missing keys are omitted from the result.
// Sensitive values are decrypted on read.
func (s *Store) GetSettings(ctx context.Context, keys []string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings WHERE key = ANY($1)`, keys)
	if err != nil {
		return nil, fmt.Errorf("store: get settings: %w", err)
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("store: scan setting: %w", err)
		}
		decrypted, err := s.decryptSettingValue(k, v)
		if err != nil {
			return nil, fmt.Errorf("store: decrypt setting %q: %w", k, err)
		}
		result[k] = decrypted
	}
	return result, rows.Err()
}

// SetSettings upserts multiple settings.
func (s *Store) SetSettings(ctx context.Context, settings map[string]string) error {
	for k, v := range settings {
		if err := s.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}

// EncryptExistingSettings encrypts any sensitive setting that is currently
// stored as plaintext. Idempotent: already-encrypted rows are skipped.
// Returns the number of rows updated.
func (s *Store) EncryptExistingSettings(ctx context.Context) (int, error) {
	if s.encryptionKey == nil {
		return 0, nil
	}

	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return 0, fmt.Errorf("store: list settings for encryption: %w", err)
	}
	defer rows.Close()

	type kv struct{ k, v string }
	var toEncrypt []kv
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return 0, fmt.Errorf("store: scan setting for encryption: %w", err)
		}
		if !crypto.IsSensitiveSettingsKey(k) || v == "" || crypto.IsEncrypted(v) {
			continue
		}
		toEncrypt = append(toEncrypt, kv{k, v})
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("store: scan settings rows: %w", err)
	}

	for _, row := range toEncrypt {
		encrypted, err := crypto.Encrypt(s.encryptionKey, row.v)
		if err != nil {
			return 0, fmt.Errorf("store: encrypt setting %q: %w", row.k, err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE settings SET value = $1 WHERE key = $2`, encrypted, row.k); err != nil {
			return 0, fmt.Errorf("store: update encrypted setting %q: %w", row.k, err)
		}
	}
	return len(toEncrypt), nil
}
