//go:build integration

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/crypto"
)

func TestGetSetting_NotFound(t *testing.T) {
	st := newTestStore(t)
	val, err := st.GetSetting(context.Background(), "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("expected empty string, got %q", val)
	}
}

func TestSetAndGetSetting(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.SetSetting(ctx, "test_key", "test_value"); err != nil {
		t.Fatal(err)
	}

	val, err := st.GetSetting(ctx, "test_key")
	if err != nil {
		t.Fatal(err)
	}
	if val != "test_value" {
		t.Errorf("expected 'test_value', got %q", val)
	}

	// Upsert
	if err := st.SetSetting(ctx, "test_key", "updated"); err != nil {
		t.Fatal(err)
	}
	val, err = st.GetSetting(ctx, "test_key")
	if err != nil {
		t.Fatal(err)
	}
	if val != "updated" {
		t.Errorf("expected 'updated', got %q", val)
	}
}

func TestGetSettings_Batch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	st.SetSetting(ctx, "a", "1") //nolint:errcheck // test
	st.SetSetting(ctx, "b", "2") //nolint:errcheck // test

	result, err := st.GetSettings(ctx, []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if result["a"] != "1" {
		t.Errorf("expected a=1, got %q", result["a"])
	}
	if result["b"] != "2" {
		t.Errorf("expected b=2, got %q", result["b"])
	}
	if _, ok := result["c"]; ok {
		t.Error("expected c to be missing")
	}
}

func TestSetSettings_Batch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	err := st.SetSettings(ctx, map[string]string{"x": "10", "y": "20"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := st.GetSettings(ctx, []string{"x", "y"})
	if err != nil {
		t.Fatal(err)
	}
	if result["x"] != "10" || result["y"] != "20" {
		t.Errorf("unexpected: %v", result)
	}
}

// testEncryptionKey is the same key used by the docker-compose dev stack.
const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSetSetting_SensitiveIsEncryptedAtRest(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	key, err := crypto.NewKey(testEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	st.SetEncryptionKey(key)

	// Write a sensitive setting
	plaintext := "sk-real-secret-1234567890"
	if err := st.SetSetting(ctx, "embedding_api_key", plaintext); err != nil {
		t.Fatalf("set sensitive setting: %v", err)
	}

	// Raw read from the DB to verify it's encrypted on disk
	var rawValue string
	err = st.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, "embedding_api_key").Scan(&rawValue)
	if err != nil {
		t.Fatal(err)
	}
	if !crypto.IsEncrypted(rawValue) {
		t.Errorf("expected sensitive setting to be encrypted at rest, got plaintext: %q", rawValue)
	}
	if strings.Contains(rawValue, plaintext) {
		t.Errorf("encrypted value should not contain the plaintext, got %q", rawValue)
	}

	// GetSetting should transparently decrypt
	got, err := st.GetSetting(ctx, "embedding_api_key")
	if err != nil {
		t.Fatal(err)
	}
	if got != plaintext {
		t.Errorf("decrypted value mismatch: got %q, want %q", got, plaintext)
	}
}

func TestSetSetting_NonSensitivePassthrough(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	key, _ := crypto.NewKey(testEncryptionKey)
	st.SetEncryptionKey(key)

	if err := st.SetSetting(ctx, "embedding_provider", "openai"); err != nil {
		t.Fatal(err)
	}

	var rawValue string
	_ = st.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, "embedding_provider").Scan(&rawValue)
	if rawValue != "openai" {
		t.Errorf("non-sensitive setting should be plaintext, got %q", rawValue)
	}
}

func TestGetSettings_Batch_DecryptsSensitive(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	key, _ := crypto.NewKey(testEncryptionKey)
	st.SetEncryptionKey(key)

	if err := st.SetSetting(ctx, "embedding_api_key", "embed-key-secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "rerank_api_key", "rerank-key-secret"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "embedding_provider", "openai"); err != nil {
		t.Fatal(err)
	}

	result, err := st.GetSettings(ctx, []string{"embedding_api_key", "rerank_api_key", "embedding_provider"})
	if err != nil {
		t.Fatal(err)
	}
	if result["embedding_api_key"] != "embed-key-secret" {
		t.Errorf("expected decrypted embed key, got %q", result["embedding_api_key"])
	}
	if result["rerank_api_key"] != "rerank-key-secret" {
		t.Errorf("expected decrypted rerank key, got %q", result["rerank_api_key"])
	}
	if result["embedding_provider"] != "openai" {
		t.Errorf("expected provider to round-trip, got %q", result["embedding_provider"])
	}
}

func TestSetSetting_AlreadyEncryptedNotDoubleEncrypted(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	key, _ := crypto.NewKey(testEncryptionKey)
	st.SetEncryptionKey(key)

	// First write — gets encrypted
	if err := st.SetSetting(ctx, "embedding_api_key", "secret-x"); err != nil {
		t.Fatal(err)
	}
	var firstStored string
	_ = st.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, "embedding_api_key").Scan(&firstStored)

	// Write the already-encrypted value back — should be no-op (not re-encrypted)
	if err := st.SetSetting(ctx, "embedding_api_key", firstStored); err != nil {
		t.Fatal(err)
	}
	var secondStored string
	_ = st.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, "embedding_api_key").Scan(&secondStored)
	if secondStored != firstStored {
		t.Errorf("re-writing an already-encrypted value should not double-encrypt: %q vs %q", firstStored, secondStored)
	}

	// Decrypt should still return the original plaintext
	got, _ := st.GetSetting(ctx, "embedding_api_key")
	if got != "secret-x" {
		t.Errorf("got %q, want secret-x", got)
	}
}

func TestEncryptExistingSettings(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Insert a sensitive setting in plaintext (no encryption key set)
	if err := st.SetSetting(ctx, "embedding_api_key", "plaintext-key"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "telegram_session_abc", "session-blob"); err != nil {
		t.Fatal(err)
	}
	// Non-sensitive — should be left alone
	if err := st.SetSetting(ctx, "embedding_provider", "openai"); err != nil {
		t.Fatal(err)
	}

	// Now configure the key and run the migration
	key, _ := crypto.NewKey(testEncryptionKey)
	st.SetEncryptionKey(key)

	n, err := st.EncryptExistingSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("expected 2 rows encrypted, got %d", n)
	}

	// Verify both sensitive rows are now encrypted at rest
	for _, k := range []string{"embedding_api_key", "telegram_session_abc"} {
		var raw string
		_ = st.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, k).Scan(&raw)
		if !crypto.IsEncrypted(raw) {
			t.Errorf("setting %q should be encrypted at rest, got %q", k, raw)
		}
	}

	// Non-sensitive row is unchanged
	var providerRaw string
	_ = st.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, "embedding_provider").Scan(&providerRaw)
	if providerRaw != "openai" {
		t.Errorf("non-sensitive setting should be unchanged, got %q", providerRaw)
	}

	// GetSetting still returns the right plaintext
	if got, _ := st.GetSetting(ctx, "embedding_api_key"); got != "plaintext-key" {
		t.Errorf("decrypted got %q, want plaintext-key", got)
	}

	// Re-running is a no-op (idempotent)
	n2, err := st.EncryptExistingSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("re-run should encrypt 0, got %d", n2)
	}
}

func TestEncryptExistingSettings_NoKey(t *testing.T) {
	st := newTestStore(t)
	// No SetEncryptionKey — should be a no-op
	n, err := st.EncryptExistingSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("no-key migration should encrypt 0 rows, got %d", n)
	}
}

func TestGetSetting_DecryptFails(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	key, _ := crypto.NewKey(testEncryptionKey)
	st.SetEncryptionKey(key)

	// Inject a row that LOOKS encrypted (has the enc: prefix) but contains
	// garbage. crypto.Decrypt should fail and the error should bubble up.
	_, err := st.pool.Exec(ctx,
		`INSERT INTO settings (key, value) VALUES ($1, $2)`,
		"embedding_api_key", "enc:not-valid-base64-or-ciphertext-data",
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = st.GetSetting(ctx, "embedding_api_key")
	if err == nil {
		t.Error("expected error decrypting garbage ciphertext")
	}
}

func TestGetSettings_DecryptFails(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	key, _ := crypto.NewKey(testEncryptionKey)
	st.SetEncryptionKey(key)

	_, err := st.pool.Exec(ctx,
		`INSERT INTO settings (key, value) VALUES ($1, $2)`,
		"rerank_api_key", "enc:garbage",
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = st.GetSettings(ctx, []string{"rerank_api_key"})
	if err == nil {
		t.Error("expected batch decrypt error")
	}
}
