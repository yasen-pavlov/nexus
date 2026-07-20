//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/muty/nexus/internal/model"
)

func TestRotateEncryptionKey(t *testing.T) {
	keyA := testKey(t, "a")
	keyB := testKey(t, "b")
	stA, stB := newSharedPoolStores(t, keyA, keyB)
	ctx := context.Background()

	// Seed a connector secret and two sensitive settings under key A.
	cfg := &model.ConnectorConfig{
		Type:    "imap",
		Name:    "rotate-me",
		Config:  map[string]any{"host": "mail.example.com", "password": "hunter2"},
		Enabled: true,
		Shared:  true,
	}
	if err := stA.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if err := stA.SetSetting(ctx, "llm_anthropic_api_key", "sk-secret"); err != nil {
		t.Fatalf("set api key: %v", err)
	}
	if err := stA.SetSetting(ctx, "telegram_session_42", "session-blob"); err != nil {
		t.Fatalf("set session: %v", err)
	}

	// Rotate A -> B.
	report, err := stA.RotateEncryptionKey(ctx, keyA, keyB)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if report.Connectors != 1 {
		t.Errorf("expected 1 connector rotated, got %d", report.Connectors)
	}
	if report.Settings != 2 {
		t.Errorf("expected 2 settings rotated, got %d", report.Settings)
	}

	// Under the NEW key everything decrypts cleanly.
	got, err := stB.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("get under new key: %v", err)
	}
	if got.CredentialsUnreadable {
		t.Error("connector should be readable under the new key after rotation")
	}
	if got.Config["password"] != "hunter2" {
		t.Errorf("expected decrypted password under new key, got %v", got.Config["password"])
	}
	if v, _ := stB.GetSetting(ctx, "llm_anthropic_api_key"); v != "sk-secret" {
		t.Errorf("expected api key readable under new key, got %q", v)
	}
	if v, _ := stB.GetSetting(ctx, "telegram_session_42"); v != "session-blob" {
		t.Errorf("expected session readable under new key, got %q", v)
	}

	// Under the OLD key the same row is now unreadable (re-encrypted under B).
	degraded, err := stA.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("get under old key should degrade, not error: %v", err)
	}
	if !degraded.CredentialsUnreadable {
		t.Error("connector should be unreadable under the old key after rotation")
	}
	// The settings were re-encrypted too — the old key can no longer read them.
	if _, err := stA.GetSetting(ctx, "llm_anthropic_api_key"); err == nil {
		t.Error("api key setting should be unreadable under the old key after rotation")
	}
	if _, err := stA.GetSetting(ctx, "telegram_session_42"); err == nil {
		t.Error("telegram session setting should be unreadable under the old key after rotation")
	}
}

// TestRotateEncryptionKey_PartialFailureRollsBack proves the all-or-nothing
// contract: a row that decrypts fine (a connector under the old key) is UPDATEd
// inside the tx BEFORE a later row (a setting under an unrelated third key)
// fails to decrypt — the whole transaction must roll back, leaving the
// connector still readable under the old key. A non-transactional
// implementation would leave the connector permanently under the new key.
func TestRotateEncryptionKey_PartialFailureRollsBack(t *testing.T) {
	keyA := testKey(t, "a")
	keyB := testKey(t, "b")
	keyC := testKey(t, "c")
	stores := newSharedPoolStoresN(t, keyA, keyB, keyC)
	stA, stC := stores[0], stores[2]
	ctx := context.Background()

	// Connector under keyA — its UPDATE runs first, inside the tx.
	cfg := &model.ConnectorConfig{
		Type:    "imap",
		Name:    "rollback",
		Config:  map[string]any{"host": "h", "password": "pw"},
		Enabled: true,
		Shared:  true,
	}
	if err := stA.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	// Sensitive setting under keyC — decrypting it with keyA fails, aborting
	// the tx AFTER the connector UPDATE already executed.
	if err := stC.SetSetting(ctx, "llm_anthropic_api_key", "sk-secret"); err != nil {
		t.Fatalf("set setting: %v", err)
	}

	if _, err := stA.RotateEncryptionKey(ctx, keyA, keyB); err == nil {
		t.Fatal("expected rotation to fail when a row can't be decrypted with the old key")
	}

	// Rollback proof: the connector still decrypts cleanly under keyA.
	got, err := stA.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("get after failed rotation: %v", err)
	}
	if got.CredentialsUnreadable {
		t.Error("connector UPDATE was not rolled back — no longer readable under the old key")
	}
	if got.Config["password"] != "pw" {
		t.Errorf("expected original password intact after rollback, got %v", got.Config["password"])
	}
}

func TestRotateEncryptionKey_WrongOldKey(t *testing.T) {
	keyA := testKey(t, "a")
	keyB := testKey(t, "b")
	keyC := testKey(t, "c")
	stA, _ := newSharedPoolStores(t, keyA, keyB)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type:    "imap",
		Name:    "wrong-old",
		Config:  map[string]any{"host": "h", "password": "pw"},
		Enabled: true,
		Shared:  true,
	}
	if err := stA.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Rotating with the wrong OLD key must abort and roll back.
	if _, err := stA.RotateEncryptionKey(ctx, keyC, keyB); err == nil {
		t.Fatal("expected rotation to fail with a wrong old key")
	}

	// The row is untouched — still readable under the original key A.
	got, err := stA.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("get after failed rotation: %v", err)
	}
	if got.CredentialsUnreadable {
		t.Error("failed rotation must roll back — row should still decrypt under key A")
	}
	if got.Config["password"] != "pw" {
		t.Errorf("expected original password intact, got %v", got.Config["password"])
	}
}
