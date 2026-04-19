//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"go.uber.org/zap"
)

// newAuthTestRouter spins up a router on a fresh DB without any pre-created
// users. Use this when testing the registration / setup_required flow.
func newAuthTestRouter(t *testing.T) (http.Handler, func() *http.Request) {
	t.Helper()
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())
	makeReq := func() *http.Request { return nil }
	return router, makeReq
}

func decodeAPI(t *testing.T, body *bytes.Buffer) APIResponse {
	t.Helper()
	var resp APIResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func doJSON(t *testing.T, router http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

// --- Health (with setup_required) ---

func TestHealth_SetupRequired(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	w := doJSON(t, router, http.MethodGet, "/api/health", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeAPI(t, w.Body)
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatal("expected map data")
	}
	if data["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", data["status"])
	}
	if data["setup_required"] != true {
		t.Errorf("expected setup_required=true on fresh DB, got %v", data["setup_required"])
	}
}

func TestHealth_NoSetupRequiredAfterRegister(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	// Register first user
	w := doJSON(t, router, http.MethodPost, "/api/auth/register", `{"username":"admin","password":"password123"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", w.Code, w.Body.String())
	}

	w = doJSON(t, router, http.MethodGet, "/api/health", "", "")
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	if _, exists := data["setup_required"]; exists {
		t.Errorf("setup_required should be absent after first registration, got %v", data["setup_required"])
	}
}

// --- Register ---

func TestRegister_FirstUserBecomesAdmin(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	w := doJSON(t, router, http.MethodPost, "/api/auth/register", `{"username":"alice","password":"password123"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	if data["token"] == nil || data["token"] == "" {
		t.Error("expected token in response")
	}
	user, _ := data["user"].(map[string]any)
	if user["username"] != "alice" {
		t.Errorf("expected username=alice, got %v", user["username"])
	}
	if user["role"] != "admin" {
		t.Errorf("expected role=admin, got %v", user["role"])
	}
}

func TestRegister_SecondUserForbidden(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	doJSON(t, router, http.MethodPost, "/api/auth/register", `{"username":"first","password":"password123"}`, "")

	w := doJSON(t, router, http.MethodPost, "/api/auth/register", `{"username":"second","password":"password123"}`, "")
	if w.Code != http.StatusForbidden {
		t.Errorf("second registration: expected 403, got %d", w.Code)
	}
}

func TestRegister_ShortPassword(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	w := doJSON(t, router, http.MethodPost, "/api/auth/register", `{"username":"x","password":"short"}`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for short password, got %d", w.Code)
	}
}

func TestRegister_InvalidJSON(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	w := doJSON(t, router, http.MethodPost, "/api/auth/register", `{not valid`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Login ---

func TestLogin_Success(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	doJSON(t, router, http.MethodPost, "/api/auth/register", `{"username":"alice","password":"password123"}`, "")

	w := doJSON(t, router, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"password123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	if data["token"] == nil || data["token"] == "" {
		t.Error("expected token in response")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	doJSON(t, router, http.MethodPost, "/api/auth/register", `{"username":"alice","password":"password123"}`, "")

	w := doJSON(t, router, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"wrongpass1"}`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestLogin_UnknownUser(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	w := doJSON(t, router, http.MethodPost, "/api/auth/login", `{"username":"ghost","password":"password123"}`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestLogin_InvalidJSON(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	w := doJSON(t, router, http.MethodPost, "/api/auth/login", `not json`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Me ---

func TestMe_Authenticated(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	w := doJSON(t, router, http.MethodPost, "/api/auth/register", `{"username":"alice","password":"password123"}`, "")
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	token, _ := data["token"].(string)

	w = doJSON(t, router, http.MethodGet, "/api/auth/me", "", token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp = decodeAPI(t, w.Body)
	user := resp.Data.(map[string]any)
	if user["username"] != "alice" {
		t.Errorf("expected username=alice, got %v", user["username"])
	}
	if user["role"] != "admin" {
		t.Errorf("expected role=admin, got %v", user["role"])
	}
	createdAt, ok := user["created_at"].(string)
	if !ok || createdAt == "" {
		t.Errorf("expected created_at to be a non-empty timestamp, got %v", user["created_at"])
	}
}

func TestMe_Unauthenticated(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	w := doJSON(t, router, http.MethodGet, "/api/auth/me", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestMe_BadToken(t *testing.T) {
	router, _ := newAuthTestRouter(t)

	w := doJSON(t, router, http.MethodGet, "/api/auth/me", "", "this-is-not-a-token")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// TestMe_DirectNoContext exercises the nil-claims branch by calling the
// handler directly, bypassing the middleware.
func TestMe_DirectNoContext(t *testing.T) {
	h := &handler{log: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	w := httptest.NewRecorder()
	h.Me(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- Helper for tests that need an authenticated admin + user setup ---

type authedUser struct {
	id    uuid.UUID
	token string
}

func setupAdminAndUser(t *testing.T, router http.Handler) (admin authedUser, user authedUser) {
	t.Helper()

	// Register admin
	w := doJSON(t, router, http.MethodPost, "/api/auth/register", `{"username":"admin","password":"password123"}`, "")
	if w.Code != http.StatusCreated {
		t.Fatalf("register admin: %d %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	admin.token, _ = data["token"].(string)
	userMap := data["user"].(map[string]any)
	admin.id, _ = uuid.Parse(userMap["id"].(string))

	// Admin creates a regular user
	w = doJSON(t, router, http.MethodPost, "/api/users", `{"username":"alice","password":"password123","role":"user"}`, admin.token)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	resp = decodeAPI(t, w.Body)
	userMap = resp.Data.(map[string]any)
	user.id, _ = uuid.Parse(userMap["id"].(string))

	// Login as that user
	w = doJSON(t, router, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"password123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("login user: %d", w.Code)
	}
	resp = decodeAPI(t, w.Body)
	data = resp.Data.(map[string]any)
	user.token, _ = data["token"].(string)

	return admin, user
}

// --- CreateUser ---

func TestCreateUser_AdminCanCreate(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPost, "/api/users", `{"username":"bob","password":"password123","role":"user"}`, admin.token)
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestCreateUser_NonAdminForbidden(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	_, user := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPost, "/api/users", `{"username":"bob","password":"password123","role":"user"}`, user.token)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestCreateUser_DuplicateUsername(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	// "alice" already exists from setupAdminAndUser
	w := doJSON(t, router, http.MethodPost, "/api/users", `{"username":"alice","password":"password123","role":"user"}`, admin.token)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestCreateUser_InvalidRole(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPost, "/api/users", `{"username":"x","password":"password123","role":"superuser"}`, admin.token)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateUser_DefaultRoleIsUser(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPost, "/api/users", `{"username":"defrole","password":"password123"}`, admin.token)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	resp := decodeAPI(t, w.Body)
	user := resp.Data.(map[string]any)
	if user["role"] != "user" {
		t.Errorf("expected default role=user, got %v", user["role"])
	}
}

// --- ListUsers ---

func TestListUsers_AdminSeesAll(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodGet, "/api/users", "", admin.token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeAPI(t, w.Body)
	users, ok := resp.Data.([]any)
	if !ok {
		t.Fatal("expected array")
	}
	if len(users) != 2 {
		t.Errorf("expected 2 users (admin + alice), got %d", len(users))
	}
}

func TestListUsers_NonAdminForbidden(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	_, user := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodGet, "/api/users", "", user.token)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// --- DeleteUser ---

func TestDeleteUser_AdminCanDelete(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, user := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodDelete, "/api/users/"+user.id.String(), "", admin.token)
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestDeleteUser_CannotDeleteSelf(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodDelete, "/api/users/"+admin.id.String(), "", admin.token)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDeleteUser_NonAdminForbidden(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	_, user := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodDelete, "/api/users/"+user.id.String(), "", user.token)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestDeleteUser_NotFound(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodDelete, "/api/users/"+uuid.New().String(), "", admin.token)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDeleteUser_BadID(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodDelete, "/api/users/not-a-uuid", "", admin.token)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- ChangePassword ---

func TestChangePassword_SelfCanChange(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	_, user := setupAdminAndUser(t, router)

	// Self-rotation returns 200 with a freshly minted token (the
	// version-bump has invalidated the caller's previous token; the API
	// re-issues so the FE can transparently swap localStorage and stay
	// signed in — see the "rotate freely" UX promise).
	w := doJSON(t, router, http.MethodPut, "/api/users/"+user.id.String()+"/password", `{"password":"newpassword456"}`, user.token)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAPI(t, w.Body)
	data := resp.Data.(map[string]any)
	newToken, _ := data["token"].(string)
	if newToken == "" {
		t.Fatal("expected non-empty token in self-rotation response")
	}
	if newToken == user.token {
		t.Error("expected a new token after self-rotation, got the same one")
	}

	// Login with new password should work
	w = doJSON(t, router, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"newpassword456"}`, "")
	if w.Code != http.StatusOK {
		t.Errorf("login with new password: expected 200, got %d", w.Code)
	}
}

func TestChangePassword_AdminCanChangeOther(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, user := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPut, "/api/users/"+user.id.String()+"/password", `{"password":"newpassword456"}`, admin.token)
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestChangePassword_UserCannotChangeOther(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, user := setupAdminAndUser(t, router)

	// user (alice) tries to change admin's password
	w := doJSON(t, router, http.MethodPut, "/api/users/"+admin.id.String()+"/password", `{"password":"newpassword456"}`, user.token)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestChangePassword_ShortPassword(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	_, user := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPut, "/api/users/"+user.id.String()+"/password", `{"password":"short"}`, user.token)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Connector ownership end-to-end (the linchpin test for Phase 7 security) ---

func TestConnectorOwnership_UserCannotAccessOthers(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, alice := setupAdminAndUser(t, router)

	// Admin creates bob
	w := doJSON(t, router, http.MethodPost, "/api/users", `{"username":"bob","password":"password123","role":"user"}`, admin.token)
	if w.Code != http.StatusCreated {
		t.Fatalf("create bob: %d", w.Code)
	}
	w = doJSON(t, router, http.MethodPost, "/api/auth/login", `{"username":"bob","password":"password123"}`, "")
	resp := decodeAPI(t, w.Body)
	bobToken := resp.Data.(map[string]any)["token"].(string)

	// Alice creates a private connector
	dir := t.TempDir()
	body := fmt.Sprintf(`{"type":"filesystem","name":"alice-private","config":{"root_path":"%s","patterns":"*.txt"},"enabled":true}`, dir)
	w = doJSON(t, router, http.MethodPost, "/api/connectors/", body, alice.token)
	if w.Code != http.StatusCreated {
		t.Fatalf("alice create: %d %s", w.Code, w.Body.String())
	}
	resp = decodeAPI(t, w.Body)
	aliceCfg := resp.Data.(map[string]any)
	aliceConnID := aliceCfg["id"].(string)

	// Bob lists his own connectors — should be empty
	w = doJSON(t, router, http.MethodGet, "/api/connectors/", "", bobToken)
	resp = decodeAPI(t, w.Body)
	bobList, _ := resp.Data.([]any)
	for _, c := range bobList {
		cm := c.(map[string]any)
		if cm["name"] == "alice-private" {
			t.Error("bob should not see alice's private connector in list")
		}
	}

	// Bob tries to GET alice's connector by ID → 404
	w = doJSON(t, router, http.MethodGet, "/api/connectors/"+aliceConnID, "", bobToken)
	if w.Code != http.StatusNotFound {
		t.Errorf("bob GET alice: expected 404, got %d", w.Code)
	}

	// Bob tries to UPDATE alice's connector → 404
	updateBody := fmt.Sprintf(`{"type":"filesystem","name":"hacked","config":{"root_path":"%s","patterns":"*"},"enabled":false}`, dir)
	w = doJSON(t, router, http.MethodPut, "/api/connectors/"+aliceConnID, updateBody, bobToken)
	if w.Code != http.StatusNotFound {
		t.Errorf("bob PUT alice: expected 404, got %d", w.Code)
	}

	// Bob tries to DELETE alice's connector → 404
	w = doJSON(t, router, http.MethodDelete, "/api/connectors/"+aliceConnID, "", bobToken)
	if w.Code != http.StatusNotFound {
		t.Errorf("bob DELETE alice: expected 404, got %d", w.Code)
	}

	// Bob tries to TRIGGER SYNC on alice's connector → 404
	w = doJSON(t, router, http.MethodPost, "/api/sync/"+aliceConnID, "", bobToken)
	if w.Code != http.StatusNotFound {
		t.Errorf("bob sync alice: expected 404, got %d", w.Code)
	}

	// Bob tries to DELETE CURSOR for alice's connector → 404
	w = doJSON(t, router, http.MethodDelete, "/api/sync/cursors/"+aliceConnID, "", bobToken)
	if w.Code != http.StatusNotFound {
		t.Errorf("bob delete cursor alice: expected 404, got %d", w.Code)
	}

	// Alice can still access her own connector
	w = doJSON(t, router, http.MethodGet, "/api/connectors/"+aliceConnID, "", alice.token)
	if w.Code != http.StatusOK {
		t.Errorf("alice GET own: expected 200, got %d", w.Code)
	}

	// Admin can access alice's connector
	w = doJSON(t, router, http.MethodGet, "/api/connectors/"+aliceConnID, "", admin.token)
	if w.Code != http.StatusOK {
		t.Errorf("admin GET alice: expected 200, got %d", w.Code)
	}
}

func TestConnectorOwnership_SettingsAreAdminOnly(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	_, user := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodGet, "/api/settings/embedding", "", user.token)
	if w.Code != http.StatusForbidden {
		t.Errorf("user GET embedding settings: expected 403, got %d", w.Code)
	}

	w = doJSON(t, router, http.MethodPost, "/api/reindex", "", user.token)
	if w.Code != http.StatusForbidden {
		t.Errorf("user POST reindex: expected 403, got %d", w.Code)
	}

	w = doJSON(t, router, http.MethodDelete, "/api/sync/cursors", "", user.token)
	if w.Code != http.StatusForbidden {
		t.Errorf("user DELETE all cursors: expected 403, got %d", w.Code)
	}
}

// --- Flipping shared on a connector propagates to indexed chunks ---

func TestUpdateConnector_SharedFlagPropagates(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	ctx := context.Background()

	// Create alice (admin), bob (user), and a private filesystem connector owned by alice.
	alice, err := st.CreateUser(ctx, "alice-prop", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := st.CreateUser(ctx, "bob-prop", "hash", "user")
	if err != nil {
		t.Fatal(err)
	}

	cfg := &model.ConnectorConfig{
		Type: "filesystem", Name: "alice-prop-conn",
		Config:  map[string]any{"root_path": t.TempDir(), "patterns": "*.txt"},
		Enabled: true,
		Shared:  false,
		UserID:  &alice.ID,
	}
	if err := cm.Add(ctx, cfg); err != nil {
		t.Fatal(err)
	}

	// Index a chunk as if a sync had run, with the original ownership baked in.
	chunk := model.Chunk{
		ID: "fs:alice-prop-conn:doc1:0", ParentID: "fs:alice-prop-conn:doc1", ChunkIndex: 0,
		Title: "Alice doc", Content: "propagationterm appears in alice's private doc",
		FullContent: "propagationterm appears in alice's private doc",
		SourceType:  "filesystem", SourceName: "alice-prop-conn", SourceID: "doc1",
		OwnerID:   alice.ID.String(),
		Shared:    false,
		CreatedAt: time.Now(),
	}
	if err := sc.IndexChunks(ctx, []model.Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	sc.Refresh(ctx) //nolint:errcheck // test

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	aliceToken, _ := auth.GenerateToken(testJWTSecret, alice.ID, alice.Username, alice.Role, 1)
	bobToken, _ := auth.GenerateToken(testJWTSecret, bob.ID, bob.Username, bob.Role, 1)

	searchAs := func(token string) int {
		w := doJSON(t, router, http.MethodGet, "/api/search?q=propagationterm", "", token)
		if w.Code != http.StatusOK {
			t.Fatalf("search: %d %s", w.Code, w.Body.String())
		}
		var resp APIResponse
		_ = json.NewDecoder(w.Body).Decode(&resp)
		data, _ := resp.Data.(map[string]any)
		tc, _ := data["total_count"].(float64)
		return int(tc)
	}

	// Baseline: alice sees the doc, bob doesn't.
	if got := searchAs(aliceToken); got != 1 {
		t.Errorf("alice baseline: expected 1, got %d", got)
	}
	if got := searchAs(bobToken); got != 0 {
		t.Errorf("bob baseline: expected 0, got %d", got)
	}

	// Flip the connector to shared via the API.
	updateBody := fmt.Sprintf(`{"type":"filesystem","name":"alice-prop-conn","config":{"root_path":"%s","patterns":"*.txt"},"enabled":true,"shared":true}`, t.TempDir())
	w := doJSON(t, router, http.MethodPut, "/api/connectors/"+cfg.ID.String(), updateBody, aliceToken)
	if w.Code != http.StatusOK {
		t.Fatalf("update connector: %d %s", w.Code, w.Body.String())
	}

	// Bob should now see the chunk immediately, without re-syncing.
	if got := searchAs(bobToken); got != 1 {
		t.Errorf("after flip to shared: bob should see 1, got %d", got)
	}

	// Flip back to private.
	updateBody = fmt.Sprintf(`{"type":"filesystem","name":"alice-prop-conn","config":{"root_path":"%s","patterns":"*.txt"},"enabled":true,"shared":false}`, t.TempDir())
	w = doJSON(t, router, http.MethodPut, "/api/connectors/"+cfg.ID.String(), updateBody, aliceToken)
	if w.Code != http.StatusOK {
		t.Fatalf("update connector back: %d %s", w.Code, w.Body.String())
	}

	// Bob should no longer see it; alice still does.
	if got := searchAs(bobToken); got != 0 {
		t.Errorf("after flip back to private: bob should see 0, got %d", got)
	}
	if got := searchAs(aliceToken); got != 1 {
		t.Errorf("after flip back to private: alice should still see 1, got %d", got)
	}
}

// --- Auth handler store-error paths ---
//
// These tests close the store before invoking each handler so the underlying
// DB operations fail. They cover the 500-status branches that aren't reached
// by the happy-path tests.

func TestRegister_StoreError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	// CountUsers will fail on closed store → 500
	w := doJSON(t, router, http.MethodPost, "/api/auth/register", `{"username":"x","password":"password123"}`, "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestLogin_StoreError(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	st.Close()

	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	// GetUserByUsername fails on closed store → handler returns 400 (invalid credentials)
	// because it cannot distinguish missing user from store error.
	w := doJSON(t, router, http.MethodPost, "/api/auth/login", `{"username":"x","password":"password123"}`, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateUser_StoreError(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)
	// We can't close the store on the existing router since we don't have
	// access. Instead, build a new router with a closed store and inject
	// admin auth via the wrapper.
	st, sc, cm := newTestDeps(t)
	st.Close()
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	closedRouter := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	w := doJSON(t, closedRouter, http.MethodPost, "/api/users", `{"username":"newuser","password":"password123","role":"user"}`, admin.token)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestListUsers_StoreError(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	st, sc, cm := newTestDeps(t)
	st.Close()
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	closedRouter := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	w := doJSON(t, closedRouter, http.MethodGet, "/api/users", "", admin.token)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestDeleteUser_StoreError(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	st, sc, cm := newTestDeps(t)
	st.Close()
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	closedRouter := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	w := doJSON(t, closedRouter, http.MethodDelete, "/api/users/"+uuid.New().String(), "", admin.token)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestMe_StoreError(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	st, sc, cm := newTestDeps(t)
	st.Close()
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	closedRouter := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	// Token is valid, but the closed store fails the GetUserByID lookup → 500.
	w := doJSON(t, closedRouter, http.MethodGet, "/api/auth/me", "", admin.token)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestMe_DeletedUser(t *testing.T) {
	st, sc, cm := newTestDeps(t)
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	router := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	_, user := setupAdminAndUser(t, router)

	// Delete the regular user out from under their valid token.
	if err := st.DeleteUser(context.Background(), user.id); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	// /me should 401 (not 500), so the FE auto-redirects to /login.
	w := doJSON(t, router, http.MethodGet, "/api/auth/me", "", user.token)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for deleted user, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestChangePassword_StoreError(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	st, sc, cm := newTestDeps(t)
	st.Close()
	em := NewEmbeddingManager(st, zap.NewNop())
	p := pipeline.New(st, sc, em, zap.NewNop())
	closedRouter := NewRouter(st, sc, p, cm, em, NewRerankManager(st, zap.NewNop()), NewSyncJobManager(st, zap.NewNop()), nil, nil, nil, testJWTSecret, nil, nil, nil, zap.NewNop())

	w := doJSON(t, closedRouter, http.MethodPut, "/api/users/"+uuid.New().String()+"/password", `{"password":"newpassword456"}`, admin.token)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestChangePassword_BadID(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPut, "/api/users/not-a-uuid/password", `{"password":"newpassword456"}`, admin.token)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestChangePassword_BadJSON(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPut, "/api/users/"+uuid.New().String()+"/password", `not json`, admin.token)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestChangePassword_NotFound(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPut, "/api/users/"+uuid.New().String()+"/password", `{"password":"newpassword456"}`, admin.token)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestCreateUser_InvalidJSON(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPost, "/api/users", `not json`, admin.token)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCreateUser_ShortPassword(t *testing.T) {
	router, _ := newAuthTestRouter(t)
	admin, _ := setupAdminAndUser(t, router)

	w := doJSON(t, router, http.MethodPost, "/api/users", `{"username":"x","password":"short","role":"user"}`, admin.token)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Smoke test the underlying ContextWithClaims helper ---

func TestAuth_ContextWithClaims(t *testing.T) {
	id := uuid.New()
	claims := &auth.Claims{UserID: id, Username: "test", Role: "admin"}
	ctx := auth.ContextWithClaims(context.Background(), claims)
	got := auth.UserFromContext(ctx)
	if got == nil || got.UserID != id {
		t.Errorf("ContextWithClaims round-trip failed: got %+v", got)
	}
}
