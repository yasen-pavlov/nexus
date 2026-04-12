package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestContextWithClaims_RoundTrip(t *testing.T) {
	userID := uuid.New()
	claims := &Claims{UserID: userID, Username: "alice", Role: "admin"}
	ctx := ContextWithClaims(context.Background(), claims)

	got := UserFromContext(ctx)
	if got == nil || got.UserID != userID || got.Role != "admin" {
		t.Errorf("round-trip failed: got %+v, want userID=%s role=admin", got, userID)
	}

	// UserIDFromContext convenience helper exercises the same path.
	if id := UserIDFromContext(ctx); id != userID {
		t.Errorf("UserIDFromContext = %s, want %s", id, userID)
	}

	// Empty context returns nil claims and nil UUID — exercises the
	// not-present branch in UserIDFromContext.
	if got := UserFromContext(context.Background()); got != nil {
		t.Errorf("empty context should return nil claims, got %+v", got)
	}
	if got := UserIDFromContext(context.Background()); got != uuid.Nil {
		t.Errorf("empty context should return uuid.Nil, got %s", got)
	}
}

var testSecret = []byte("test-secret-key-for-jwt-testing!")

func TestGenerateAndParseToken(t *testing.T) {
	userID := uuid.New()
	token, err := GenerateToken(testSecret, userID, "alice", "admin")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	claims, err := ParseToken(testSecret, token)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("UserID = %v, want %v", claims.UserID, userID)
	}
	if claims.Username != "alice" {
		t.Errorf("Username = %q, want alice", claims.Username)
	}
	if claims.Role != "admin" {
		t.Errorf("Role = %q, want admin", claims.Role)
	}
}

func TestParseToken_InvalidSecret(t *testing.T) {
	token, _ := GenerateToken(testSecret, uuid.New(), "alice", "admin")
	_, err := ParseToken([]byte("wrong-secret"), token)
	if err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestParseToken_InvalidToken(t *testing.T) {
	_, err := ParseToken(testSecret, "not.a.valid.token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestMiddleware_ValidToken(t *testing.T) {
	userID := uuid.New()
	token, _ := GenerateToken(testSecret, userID, "alice", "user")

	handler := Middleware(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := UserFromContext(r.Context())
		if claims == nil {
			t.Fatal("expected claims in context")
		}
		if claims.Username != "alice" {
			t.Errorf("Username = %q, want alice", claims.Username)
		}
		if UserIDFromContext(r.Context()) != userID {
			t.Errorf("UserID mismatch")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestMiddleware_NoHeader(t *testing.T) {
	handler := Middleware(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestMiddleware_InvalidHeader(t *testing.T) {
	handler := Middleware(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestMiddleware_ExpiredToken(t *testing.T) {
	handler := Middleware(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJleHAiOjB9.invalid")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireRole_Admin(t *testing.T) {
	token, _ := GenerateToken(testSecret, uuid.New(), "alice", "admin")

	handler := Middleware(testSecret)(RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for admin, got %d", w.Code)
	}
}

func TestRequireRole_Forbidden(t *testing.T) {
	token, _ := GenerateToken(testSecret, uuid.New(), "bob", "user")

	handler := Middleware(testSecret)(RequireRole("admin")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-admin, got %d", w.Code)
	}
}

func TestMiddleware_QueryParamToken(t *testing.T) {
	// EventSource cannot set headers, so the middleware accepts ?token= as fallback.
	userID := uuid.New()
	token, _ := GenerateToken(testSecret, userID, "alice", "user")

	handler := Middleware(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := UserFromContext(r.Context())
		if claims == nil || claims.UserID != userID {
			t.Errorf("expected user from query param token")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/?token="+token, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestMiddleware_HeaderTakesPrecedenceOverQuery(t *testing.T) {
	// If both Authorization header and ?token= are present, header wins.
	headerID := uuid.New()
	queryID := uuid.New()
	headerToken, _ := GenerateToken(testSecret, headerID, "alice", "user")
	queryToken, _ := GenerateToken(testSecret, queryID, "bob", "user")

	handler := Middleware(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := UserFromContext(r.Context())
		if claims.UserID != headerID {
			t.Errorf("expected header token to win, got %v", claims.UserID)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/?token="+queryToken, nil)
	req.Header.Set("Authorization", "Bearer "+headerToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestMiddleware_QueryParamInvalidToken(t *testing.T) {
	handler := Middleware(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/?token=garbage", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestUserFromContext_Nil(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if UserFromContext(req.Context()) != nil {
		t.Error("expected nil claims from empty context")
	}
	if UserIDFromContext(req.Context()) != uuid.Nil {
		t.Error("expected nil UUID from empty context")
	}
}
