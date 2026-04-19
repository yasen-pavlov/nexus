//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestCreateUser(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	u, err := st.CreateUser(ctx, "alice", "hash-alice", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == uuid.Nil {
		t.Error("expected non-nil ID")
	}
	if u.Username != "alice" {
		t.Errorf("Username = %q, want alice", u.Username)
	}
	if u.Role != "admin" {
		t.Errorf("Role = %q, want admin", u.Role)
	}
	if u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Error("expected timestamps to be set")
	}
}

func TestCreateUser_DuplicateUsername(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if _, err := st.CreateUser(ctx, "dupe", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	_, err := st.CreateUser(ctx, "dupe", "hash2", "user")
	if err != ErrDuplicateUsername {
		t.Errorf("expected ErrDuplicateUsername, got %v", err)
	}
}

func TestCreateUser_StoreError(t *testing.T) {
	st := newClosedStore(t)
	_, err := st.CreateUser(context.Background(), "x", "h", "user")
	if err == nil {
		t.Fatal("expected error from closed store")
	}
	if err == ErrDuplicateUsername {
		t.Errorf("should not be ErrDuplicateUsername")
	}
}

func TestGetUserByUsername(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	created, _ := st.CreateUser(ctx, "lookup", "hash-lookup", "user")

	got, hash, err := st.GetUserByUsername(ctx, "lookup")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch")
	}
	if hash != "hash-lookup" {
		t.Errorf("hash = %q, want hash-lookup", hash)
	}
	if got.Role != "user" {
		t.Errorf("Role = %q", got.Role)
	}
}

func TestGetUserByUsername_NotFound(t *testing.T) {
	st := newTestStore(t)
	_, _, err := st.GetUserByUsername(context.Background(), "ghost")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetUserByUsername_StoreError(t *testing.T) {
	st := newClosedStore(t)
	_, _, err := st.GetUserByUsername(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
	if err == ErrNotFound {
		t.Errorf("should not be ErrNotFound on closed store")
	}
}

func TestGetUserByID(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	created, _ := st.CreateUser(ctx, "byid", "hash", "admin")

	got, err := st.GetUserByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.Username != "byid" {
		t.Errorf("Username = %q", got.Username)
	}
}

func TestGetUserByID_NotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetUserByID(context.Background(), uuid.New())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetUserByID_StoreError(t *testing.T) {
	st := newClosedStore(t)
	_, err := st.GetUserByID(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListUsers_Empty(t *testing.T) {
	st := newTestStore(t)
	users, err := st.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if users == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(users) != 0 {
		t.Errorf("expected 0 users, got %d", len(users))
	}
}

func TestListUsers(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"u1", "u2", "u3"} {
		if _, err := st.CreateUser(ctx, name, "h", "user"); err != nil {
			t.Fatal(err)
		}
	}

	users, err := st.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 {
		t.Errorf("expected 3 users, got %d", len(users))
	}
	// Ordered by created_at
	if users[0].Username != "u1" {
		t.Errorf("expected u1 first, got %q", users[0].Username)
	}
}

func TestListUsers_StoreError(t *testing.T) {
	st := newClosedStore(t)
	_, err := st.ListUsers(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteUser(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	u, _ := st.CreateUser(ctx, "todelete", "h", "user")

	if err := st.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := st.GetUserByID(ctx, u.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteUser_NotFound(t *testing.T) {
	st := newTestStore(t)
	err := st.DeleteUser(context.Background(), uuid.New())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteUser_StoreError(t *testing.T) {
	st := newClosedStore(t)
	err := st.DeleteUser(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error")
	}
	if err == ErrNotFound {
		t.Errorf("should not be ErrNotFound on closed store")
	}
}

func TestUpdateUserPassword(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	u, _ := st.CreateUser(ctx, "pw-user", "old-hash", "user")

	if err := st.UpdateUserPassword(ctx, u.ID, "new-hash"); err != nil {
		t.Fatalf("update password: %v", err)
	}

	_, hash, err := st.GetUserByUsername(ctx, "pw-user")
	if err != nil {
		t.Fatal(err)
	}
	if hash != "new-hash" {
		t.Errorf("hash = %q, want new-hash", hash)
	}
}

func TestUpdateUserPassword_NotFound(t *testing.T) {
	st := newTestStore(t)
	err := st.UpdateUserPassword(context.Background(), uuid.New(), "h")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateUserPassword_StoreError(t *testing.T) {
	st := newClosedStore(t)
	err := st.UpdateUserPassword(context.Background(), uuid.New(), "h")
	if err == nil {
		t.Fatal("expected error")
	}
	if err == ErrNotFound {
		t.Errorf("should not be ErrNotFound on closed store")
	}
}

func TestCountUsers(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if n, err := st.CountUsers(ctx); err != nil || n != 0 {
		t.Errorf("CountUsers empty: n=%d err=%v", n, err)
	}

	for _, name := range []string{"a", "b", "c"} {
		if _, err := st.CreateUser(ctx, name, "h", "user"); err != nil {
			t.Fatal(err)
		}
	}

	n, err := st.CountUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("CountUsers = %d, want 3", n)
	}
}

func TestCountUsers_StoreError(t *testing.T) {
	st := newClosedStore(t)
	_, err := st.CountUsers(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateFirstAdmin_Empty(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	u, err := st.CreateFirstAdmin(ctx, "boot", "hash")
	if err != nil {
		t.Fatalf("first admin: %v", err)
	}
	if u.Role != "admin" {
		t.Errorf("role = %q, want admin", u.Role)
	}
	if u.TokenVersion != 1 {
		t.Errorf("token_version = %d, want 1", u.TokenVersion)
	}
}

func TestCreateFirstAdmin_AlreadyExists(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if _, err := st.CreateFirstAdmin(ctx, "boot", "hash"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := st.CreateFirstAdmin(ctx, "second", "hash")
	if err != ErrFirstAdminExists {
		t.Errorf("second call: expected ErrFirstAdminExists, got %v", err)
	}
}

func TestCreateFirstAdmin_DuplicateUsername(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if _, err := st.CreateUser(ctx, "boot", "h", "user"); err != nil {
		t.Fatal(err)
	}
	// Different username path: ErrFirstAdminExists.
	_, err := st.CreateFirstAdmin(ctx, "different", "hash")
	if err != ErrFirstAdminExists {
		t.Errorf("expected ErrFirstAdminExists when table is non-empty, got %v", err)
	}
}

func TestCreateFirstAdmin_StoreError(t *testing.T) {
	st := newClosedStore(t)
	_, err := st.CreateFirstAdmin(context.Background(), "x", "h")
	if err == nil {
		t.Fatal("expected error on closed store")
	}
	if err == ErrFirstAdminExists {
		t.Errorf("closed store should not return ErrFirstAdminExists, got %v", err)
	}
}

func TestUpdateUserPassword_BumpsTokenVersion(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	u, err := st.CreateUser(ctx, "alice", "old-hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	before := u.TokenVersion
	if before != 1 {
		t.Fatalf("initial token_version = %d, want 1", before)
	}

	if err := st.UpdateUserPassword(ctx, u.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenVersion != before+1 {
		t.Errorf("token_version = %d, want %d", got.TokenVersion, before+1)
	}
}
