//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/muty/nexus/internal/model"
)

func TestGetMyIdentities_ReturnsOwnedConnectorsWithExternalID(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	userID, token := createTestUser(t, st)
	ctx := context.Background()

	// Owned by the test user, with self-identity populated.
	owned := &model.ConnectorConfig{
		Type: "telegram", Name: "tg-personal",
		Config:  map[string]any{"api_id": 1, "api_hash": "h", "phone": "+1"},
		Enabled: true, UserID: &userID,
		ExternalID: "9001", ExternalName: "Me",
	}
	if err := st.CreateConnectorConfig(ctx, owned); err != nil {
		t.Fatalf("create owned: %v", err)
	}

	// Shared connector — never surfaces as a self-identity even when
	// populated (shared connectors have no "me").
	shared := &model.ConnectorConfig{
		Type: "filesystem", Name: "shared-fs",
		Config:  map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"},
		Enabled: true, Shared: true,
		ExternalID: "ignored",
	}
	if err := st.CreateConnectorConfig(ctx, shared); err != nil {
		t.Fatalf("create shared: %v", err)
	}

	// Another user's connector — must not leak.
	otherID, _ := createTestUser(t, st)
	other := &model.ConnectorConfig{
		Type: "telegram", Name: "tg-other",
		Config:  map[string]any{"api_id": 2, "api_hash": "h", "phone": "+2"},
		Enabled: true, UserID: &otherID,
		ExternalID: "12345", ExternalName: "Other",
	}
	if err := st.CreateConnectorConfig(ctx, other); err != nil {
		t.Fatalf("create other: %v", err)
	}

	w := doJSON(t, router, http.MethodGet, "/api/me/identities", "", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	var out identitiesResponse
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal identities: %v", err)
	}

	if len(out.Identities) != 1 {
		t.Fatalf("expected 1 identity, got %d: %+v", len(out.Identities), out.Identities)
	}
	got := out.Identities[0]
	if got.ExternalID != "9001" || got.ExternalName != "Me" {
		t.Errorf("unexpected identity: %+v", got)
	}
	if got.SourceType != "telegram" || got.SourceName != "tg-personal" {
		t.Errorf("wrong connector: %+v", got)
	}
}

func TestGetMyIdentities_EmptyWhenNoneOwned(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	_, token := createTestUser(t, st)

	w := doJSON(t, router, http.MethodGet, "/api/me/identities", "", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data, _ := json.Marshal(resp.Data)
	var out identitiesResponse
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Identities) != 0 {
		t.Errorf("expected no identities, got %d", len(out.Identities))
	}
}

func TestGetMyIdentities_SetsHasAvatarWhenBlobIsCached(t *testing.T) {
	st, bs, router := newRouterWithBinaryStore(t)
	userID, token := createTestUser(t, st)
	ctx := context.Background()

	cfg := &model.ConnectorConfig{
		Type: "telegram", Name: "tg-with-avatar",
		Config:  map[string]any{"api_id": 1, "api_hash": "h", "phone": "+1"},
		Enabled: true, UserID: &userID,
		ExternalID: "9001", ExternalName: "Me",
	}
	if err := st.CreateConnectorConfig(ctx, cfg); err != nil {
		t.Fatalf("create cfg: %v", err)
	}

	if err := bs.Put(ctx, "telegram", "tg-with-avatar", "avatars:9001",
		bytes.NewBufferString("jpeg"), 4); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w := doJSON(t, router, http.MethodGet, "/api/me/identities", "", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeAPI(t, w.Body)
	data, _ := json.Marshal(resp.Data)
	var out identitiesResponse
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Identities) != 1 {
		t.Fatalf("expected 1 identity, got %d", len(out.Identities))
	}
	if !out.Identities[0].HasAvatar {
		t.Errorf("expected has_avatar=true, got false: %+v", out.Identities[0])
	}
}

func TestGetMyIdentities_SkipsConnectorsWithoutExternalID(t *testing.T) {
	st, _, _, router := newTestRouter(t)
	userID, token := createTestUser(t, st)

	// Connector owned by the user but auth hasn't completed yet, so no
	// external_id. The endpoint should skip it rather than return an
	// empty identity row.
	cfg := &model.ConnectorConfig{
		Type: "telegram", Name: "tg-unauthed",
		Config:  map[string]any{"api_id": 1, "api_hash": "h", "phone": "+1"},
		Enabled: true, UserID: &userID,
	}
	if err := st.CreateConnectorConfig(context.Background(), cfg); err != nil {
		t.Fatalf("create: %v", err)
	}

	w := doJSON(t, router, http.MethodGet, "/api/me/identities", "", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeAPI(t, w.Body)
	data, _ := json.Marshal(resp.Data)
	var out identitiesResponse
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Identities) != 0 {
		t.Errorf("connector without external_id should be skipped, got %+v", out.Identities)
	}
}
