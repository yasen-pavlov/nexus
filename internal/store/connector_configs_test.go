//go:build integration

package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/crypto"
	"github.com/muty/nexus/internal/model"
)

func TestListConnectorConfigs_Empty(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	configs, err := st.ListConnectorConfigs(ctx)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if configs == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(configs) != 0 {
		t.Errorf("expected 0 configs, got %d", len(configs))
	}
}

func TestCreateAndGetConnectorConfig(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type:     "filesystem",
		Name:     "my-files",
		Config:   map[string]any{"root_path": "/data", "patterns": "*.txt"},
		Enabled:  true,
		Schedule: "*/30 * * * *",
		Shared:   true,
	}

	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if cfg.ID == uuid.Nil {
		t.Error("expected ID to be set")
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Name != "my-files" {
		t.Errorf("expected name 'my-files', got %q", got.Name)
	}
	if got.Type != "filesystem" {
		t.Errorf("expected type 'filesystem', got %q", got.Type)
	}
	if got.Config["root_path"] != "/data" {
		t.Errorf("expected root_path '/data', got %v", got.Config["root_path"])
	}
	if !got.Enabled {
		t.Error("expected enabled to be true")
	}
}

func TestCreateConnectorConfig_DuplicateName(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	user, err := st.CreateUser(ctx, "dupe-owner", "hash", "user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	cfg1 := &model.ConnectorConfig{Type: "filesystem", Name: "dupe", Config: map[string]any{}, Enabled: true, UserID: &user.ID}
	cfg2 := &model.ConnectorConfig{Type: "filesystem", Name: "dupe", Config: map[string]any{}, Enabled: true, UserID: &user.ID}

	if err := st.CreateConnectorConfig(ctx, cfg1); err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	err = st.CreateConnectorConfig(ctx, cfg2)
	if err != ErrDuplicateName {
		t.Errorf("expected ErrDuplicateName, got %v", err)
	}
}

// TestCreateConnectorConfig_DuplicateAcrossUsers pins GLOBAL (type, name)
// uniqueness: two different users must not both own a same-type, same-name
// connector, because their indexed documents, cached blobs, and ownership
// operations all collide on (type, name). Regression for the cross-user
// overwrite/delete hazard.
func TestCreateConnectorConfig_DuplicateAcrossUsers(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	alice, err := st.CreateUser(ctx, "alice-dupe", "hash", "user")
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := st.CreateUser(ctx, "bob-dupe", "hash", "user")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	cfgA := &model.ConnectorConfig{Type: "telegram", Name: "telegram", Config: map[string]any{}, Enabled: true, UserID: &alice.ID}
	cfgB := &model.ConnectorConfig{Type: "telegram", Name: "telegram", Config: map[string]any{}, Enabled: true, UserID: &bob.ID}

	if err := st.CreateConnectorConfig(ctx, cfgA); err != nil {
		t.Fatalf("alice create: %v", err)
	}
	if err := st.CreateConnectorConfig(ctx, cfgB); err != ErrDuplicateName {
		t.Errorf("bob create with alice's (type,name): expected ErrDuplicateName, got %v", err)
	}

	// A different type with the same name is fine — document ids differ by type.
	cfgC := &model.ConnectorConfig{Type: "filesystem", Name: "telegram", Config: map[string]any{}, Enabled: true, UserID: &bob.ID}
	if err := st.CreateConnectorConfig(ctx, cfgC); err != nil {
		t.Errorf("different-type same-name should be allowed, got %v", err)
	}
}

func TestGetConnectorConfig_NotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_, err := st.GetConnectorConfig(ctx, uuid.New())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateConnectorConfig(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "update-test", Config: map[string]any{"root_path": "/old"}, Enabled: true, Shared: true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	cfg.Name = "updated-name"
	cfg.Config = map[string]any{"root_path": "/new"}
	cfg.Enabled = false

	if err := st.UpdateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Name != "updated-name" {
		t.Errorf("expected name 'updated-name', got %q", got.Name)
	}
	if got.Config["root_path"] != "/new" {
		t.Errorf("expected root_path '/new', got %v", got.Config["root_path"])
	}
	if got.Enabled {
		t.Error("expected enabled to be false")
	}
}

func TestUpdateConnectorConfig_NotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{ID: uuid.New(), Type: "filesystem", Name: "nope", Config: map[string]any{}}
	err := st.UpdateConnectorConfig(ctx, cfg)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateConnectorConfig_DuplicateName(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	user, err := st.CreateUser(ctx, "rename-owner", "hash", "user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	cfg1 := &model.ConnectorConfig{Type: "filesystem", Name: "first", Config: map[string]any{}, Enabled: true, UserID: &user.ID}
	cfg2 := &model.ConnectorConfig{Type: "filesystem", Name: "second", Config: map[string]any{}, Enabled: true, UserID: &user.ID}
	if err := st.CreateConnectorConfig(ctx, cfg1); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateConnectorConfig(ctx, cfg2); err != nil {
		t.Fatal(err)
	}

	cfg2.Name = "first" // try to rename to existing name
	err = st.UpdateConnectorConfig(ctx, cfg2)
	if err != ErrDuplicateName {
		t.Errorf("expected ErrDuplicateName, got %v", err)
	}
}

func TestDeleteConnectorConfig(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{Type: "filesystem", Name: "delete-me", Config: map[string]any{}, Enabled: true, Shared: true}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteConnectorConfig(ctx, cfg.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	_, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteConnectorConfig_NotFound(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	err := st.DeleteConnectorConfig(ctx, uuid.New())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListConnectorConfigs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"bravo", "alpha", "charlie"} {
		cfg := &model.ConnectorConfig{Type: "filesystem", Name: name, Config: map[string]any{}, Enabled: true, Shared: true}
		if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
			t.Fatal(err)
		}
	}

	configs, err := st.ListConnectorConfigs(ctx)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(configs) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(configs))
	}
	// Should be ordered by name
	if configs[0].Name != "alpha" {
		t.Errorf("expected first config to be 'alpha', got %q", configs[0].Name)
	}
}

func TestScheduleRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "sched-test", Config: map[string]any{},
		Enabled: true, Schedule: "*/15 * * * *", Shared: true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schedule != "*/15 * * * *" {
		t.Errorf("expected schedule '*/15 * * * *', got %q", got.Schedule)
	}
	if got.LastRun != nil {
		t.Error("expected nil last_run")
	}
}

func TestUpdateLastRun(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "lastrun-test", Config: map[string]any{}, Enabled: true, Shared: true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Microsecond)
	if err := st.UpdateLastRun(ctx, cfg.ID, now); err != nil {
		t.Fatalf("update last_run failed: %v", err)
	}

	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRun == nil {
		t.Fatal("expected last_run to be set")
	}
	if !got.LastRun.Truncate(time.Microsecond).Equal(now) {
		t.Errorf("expected last_run %v, got %v", now, *got.LastRun)
	}
}

func TestEncryptExistingConfigs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Create a connector with plaintext secrets
	cfg := &model.ConnectorConfig{
		Type:    "imap",
		Name:    "encrypt-test",
		Config:  map[string]any{"server": "imap.example.com", "password": "my-secret"},
		Enabled: true,
		Shared:  true,
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Set encryption key and run migration
	key, err := crypto.NewKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewKey failed: %v", err)
	}
	st.SetEncryptionKey(key)

	n, err := st.EncryptExistingConfigs(ctx)
	if err != nil {
		t.Fatalf("EncryptExistingConfigs failed: %v", err)
	}
	if n != 1 {
		t.Errorf("encrypted %d configs, want 1", n)
	}

	// Verify the password is encrypted in the DB (read raw)
	var configJSON []byte
	err = st.pool.QueryRow(ctx, `SELECT config FROM connector_configs WHERE id = $1`, cfg.ID).Scan(&configJSON)
	if err != nil {
		t.Fatalf("raw query failed: %v", err)
	}
	var rawConfig map[string]any
	json.Unmarshal(configJSON, &rawConfig) //nolint:errcheck // test
	pw, _ := rawConfig["password"].(string)
	if !crypto.IsEncrypted(pw) {
		t.Errorf("password in DB should be encrypted, got %q", pw)
	}

	// server should still be plaintext
	if rawConfig["server"] != "imap.example.com" {
		t.Errorf("server should be plaintext, got %v", rawConfig["server"])
	}

	// Read via store (should be decrypted)
	got, err := st.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Config["password"] != "my-secret" {
		t.Errorf("decrypted password = %v, want my-secret", got.Config["password"])
	}

	// Running again should not re-encrypt
	n, err = st.EncryptExistingConfigs(ctx)
	if err != nil {
		t.Fatalf("second EncryptExistingConfigs failed: %v", err)
	}
	if n != 0 {
		t.Errorf("encrypted %d configs on second run, want 0", n)
	}
}

func TestListUserConnectorConfigs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	alice, err := st.CreateUser(ctx, "alice-list", "h", "user")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, "bob-list", "h", "user")
	if err != nil {
		t.Fatal(err)
	}

	// alice's private connector
	aliceCfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "alice-private", Config: map[string]any{}, Enabled: true, UserID: &alice.ID,
	}
	if err := st.CreateConnectorConfig(ctx, aliceCfg); err != nil {
		t.Fatal(err)
	}
	// bob's private connector
	bobCfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "bob-private", Config: map[string]any{}, Enabled: true, UserID: &bob.ID,
	}
	if err := st.CreateConnectorConfig(ctx, bobCfg); err != nil {
		t.Fatal(err)
	}
	// shared connector (no owner)
	sharedCfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "shared", Config: map[string]any{}, Enabled: true, Shared: true,
	}
	if err := st.CreateConnectorConfig(ctx, sharedCfg); err != nil {
		t.Fatal(err)
	}

	aliceList, err := st.ListUserConnectorConfigs(ctx, alice.ID)
	if err != nil {
		t.Fatalf("alice list: %v", err)
	}
	names := make(map[string]bool)
	for _, c := range aliceList {
		names[c.Name] = true
	}
	if !names["alice-private"] || !names["shared"] {
		t.Errorf("alice should see alice-private + shared, got %v", names)
	}
	if names["bob-private"] {
		t.Errorf("alice should NOT see bob-private, got %v", names)
	}

	bobList, err := st.ListUserConnectorConfigs(ctx, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	names = make(map[string]bool)
	for _, c := range bobList {
		names[c.Name] = true
	}
	if !names["bob-private"] || !names["shared"] {
		t.Errorf("bob should see bob-private + shared, got %v", names)
	}
	if names["alice-private"] {
		t.Errorf("bob should NOT see alice-private, got %v", names)
	}
}

func TestListUserConnectorConfigs_StoreError(t *testing.T) {
	st := newClosedStore(t)
	_, err := st.ListUserConnectorConfigs(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListUserConnectorConfigs_Empty(t *testing.T) {
	st := newTestStore(t)
	configs, err := st.ListUserConnectorConfigs(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if configs == nil || len(configs) != 0 {
		t.Errorf("expected non-nil empty slice, got %v", configs)
	}
}

func TestCreateConnectorConfig_OwnerMissingShared(t *testing.T) {
	// Schema CHECK constraint: must have user_id OR shared=true
	st := newTestStore(t)
	cfg := &model.ConnectorConfig{Type: "filesystem", Name: "noowner", Config: map[string]any{}, Enabled: true}
	err := st.CreateConnectorConfig(context.Background(), cfg)
	if err == nil {
		t.Error("expected CHECK constraint violation when no user_id and shared=false")
	}
}

func TestUpdateConnectorConfig_StoreError(t *testing.T) {
	st := newClosedStore(t)
	cfg := &model.ConnectorConfig{ID: uuid.New(), Type: "filesystem", Name: "x", Config: map[string]any{}, Shared: true}
	err := st.UpdateConnectorConfig(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEncryptExistingConfigs_NoKey(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// No encryption key set — should be a no-op
	n, err := st.EncryptExistingConfigs(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("encrypted %d, want 0 (no key)", n)
	}
}

// TestListConnectorConfigsByOwner pins the owner-only scoping used by user
// deletion: it must return exactly the connectors a user OWNS, excluding both
// other users' connectors and NULL-owner shared connectors (which
// ListUserConnectorConfigs would include via `OR shared = true`).
func TestListConnectorConfigsByOwner(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	alice, err := st.CreateUser(ctx, "alice-owner", "hash", "user")
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := st.CreateUser(ctx, "bob-owner", "hash", "user")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	// Global (type,name) uniqueness forces distinct pairs per connector.
	seed := []*model.ConnectorConfig{
		{Type: "filesystem", Name: "alice-fs", Config: map[string]any{}, Enabled: true, UserID: &alice.ID},
		{Type: "telegram", Name: "alice-tg", Config: map[string]any{}, Enabled: true, Shared: true, UserID: &alice.ID},
		{Type: "imap", Name: "bob-imap", Config: map[string]any{}, Enabled: true, UserID: &bob.ID},
		{Type: "paperless", Name: "shared-noowner", Config: map[string]any{}, Enabled: true, Shared: true},
	}
	for _, c := range seed {
		if err := st.CreateConnectorConfig(ctx, c); err != nil {
			t.Fatalf("create %s: %v", c.Name, err)
		}
	}

	got, err := st.ListConnectorConfigsByOwner(ctx, alice.ID)
	if err != nil {
		t.Fatalf("list by owner: %v", err)
	}
	names := map[string]bool{}
	for _, c := range got {
		names[c.Name] = true
	}
	if len(got) != 2 || !names["alice-fs"] || !names["alice-tg"] {
		t.Errorf("expected exactly alice's two connectors, got %v", names)
	}
	if names["bob-imap"] || names["shared-noowner"] {
		t.Errorf("owner scoping leaked non-owned connectors: %v", names)
	}
}

// TestOrphanSharedConnectorsByOwner: only the user's SHARED connectors get
// user_id NULLed; their private connectors and other users' connectors are
// untouched.
func TestOrphanSharedConnectorsByOwner(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	alice, err := st.CreateUser(ctx, "alice-orphan", "hash", "user")
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := st.CreateUser(ctx, "bob-orphan", "hash", "user")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	aliceShared := &model.ConnectorConfig{Type: "telegram", Name: "a-shared", Config: map[string]any{}, Enabled: true, Shared: true, UserID: &alice.ID}
	alicePrivate := &model.ConnectorConfig{Type: "filesystem", Name: "a-private", Config: map[string]any{}, Enabled: true, UserID: &alice.ID}
	bobShared := &model.ConnectorConfig{Type: "imap", Name: "b-shared", Config: map[string]any{}, Enabled: true, Shared: true, UserID: &bob.ID}
	for _, c := range []*model.ConnectorConfig{aliceShared, alicePrivate, bobShared} {
		if err := st.CreateConnectorConfig(ctx, c); err != nil {
			t.Fatalf("create %s: %v", c.Name, err)
		}
	}

	n, err := st.OrphanSharedConnectorsByOwner(ctx, alice.ID)
	if err != nil {
		t.Fatalf("orphan: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 shared connector orphaned, got %d", n)
	}

	got, _ := st.GetConnectorConfig(ctx, aliceShared.ID)
	if got.UserID != nil {
		t.Errorf("alice's shared connector should be orphaned (NULL owner), got %v", got.UserID)
	}
	priv, _ := st.GetConnectorConfig(ctx, alicePrivate.ID)
	if priv.UserID == nil || *priv.UserID != alice.ID {
		t.Error("alice's private connector must keep its owner")
	}
	other, _ := st.GetConnectorConfig(ctx, bobShared.ID)
	if other.UserID == nil || *other.UserID != bob.ID {
		t.Error("bob's shared connector must be untouched")
	}
}

// TestScanConnectorConfig_DegradesOnWrongKey pins the boot-brick fix: a row
// whose secret can't be decrypted with the active key is returned marked
// unreadable (not errored), with the ciphertext stripped — so one bad row no
// longer takes down the whole list (and thus login/search/chats).
func TestScanConnectorConfig_DegradesOnWrongKey(t *testing.T) {
	keyA := testKey(t, "a")
	keyB := testKey(t, "b")
	stA, stB := newSharedPoolStores(t, keyA, keyB)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type:    "imap",
		Name:    "degrade-me",
		Config:  map[string]any{"host": "mail.example.com", "password": "hunter2"},
		Enabled: true,
		Shared:  true,
	}
	if err := stA.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create under key A: %v", err)
	}

	// List under the WRONG key must degrade, not error.
	got, err := stB.ListConnectorConfigs(ctx)
	if err != nil {
		t.Fatalf("list under wrong key should degrade, got error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 connector, got %d", len(got))
	}
	if !got[0].CredentialsUnreadable {
		t.Error("expected CredentialsUnreadable=true under wrong key")
	}
	if _, ok := got[0].Config["password"]; ok {
		t.Error("expected the unreadable password ciphertext to be stripped")
	}
	if got[0].Config["host"] != "mail.example.com" {
		t.Errorf("expected non-sensitive host to remain, got %v", got[0].Config["host"])
	}

	// The single-row getter degrades identically.
	one, err := stB.GetConnectorConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("get under wrong key should degrade, got error: %v", err)
	}
	if !one.CredentialsUnreadable {
		t.Error("GetConnectorConfig: expected CredentialsUnreadable=true")
	}
}

// TestListConnectorConfigs_MixedReadableAndUnreadable proves one bad row no
// longer fails the whole list: a clean filesystem connector (no secrets) and an
// imap connector under a different key both come back, the latter degraded.
func TestListConnectorConfigs_MixedReadableAndUnreadable(t *testing.T) {
	keyA := testKey(t, "a")
	keyB := testKey(t, "b")
	stA, stB := newSharedPoolStores(t, keyA, keyB)
	ctx := context.Background()

	fs := &model.ConnectorConfig{Type: "filesystem", Name: "clean-fs", Config: map[string]any{"root_path": "/data"}, Enabled: true, Shared: true}
	imap := &model.ConnectorConfig{Type: "imap", Name: "secret-imap", Config: map[string]any{"host": "h", "password": "pw"}, Enabled: true, Shared: true}
	if err := stA.CreateConnectorConfig(ctx, fs); err != nil {
		t.Fatalf("create fs: %v", err)
	}
	if err := stA.CreateConnectorConfig(ctx, imap); err != nil {
		t.Fatalf("create imap: %v", err)
	}

	got, err := stB.ListConnectorConfigs(ctx)
	if err != nil {
		t.Fatalf("list should not error with one unreadable row: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both connectors returned, got %d", len(got))
	}
	byName := map[string]model.ConnectorConfig{}
	for _, c := range got {
		byName[c.Name] = c
	}
	if byName["clean-fs"].CredentialsUnreadable {
		t.Error("filesystem connector has no secrets; should be readable")
	}
	if !byName["secret-imap"].CredentialsUnreadable {
		t.Error("imap connector under wrong key should be marked unreadable")
	}
}
