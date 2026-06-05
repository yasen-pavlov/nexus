//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAPIToken_CRUDRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "tok-owner", "hash", "admin")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Create with no expiry.
	tok, err := st.CreateAPIToken(ctx, owner.ID, "agent", "hash-1", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if tok.ID == uuid.Nil {
		t.Error("expected non-nil token id")
	}
	if tok.CreatedAt.IsZero() {
		t.Error("expected created_at to be set")
	}
	if tok.ExpiresAt != nil {
		t.Errorf("expected nil expiry, got %v", tok.ExpiresAt)
	}

	// Resolve by hash → joins users for live role/username.
	idn, err := st.GetAPITokenByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("get by hash: %v", err)
	}
	if idn.UserID != owner.ID || idn.Username != "tok-owner" || idn.Role != "admin" {
		t.Errorf("identity mismatch: %+v", idn)
	}
	if idn.TokenID != tok.ID {
		t.Errorf("token id mismatch: got %s want %s", idn.TokenID, tok.ID)
	}

	// List returns it (newest first), metadata only.
	list, err := st.ListAPITokensByUser(ctx, owner.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "agent" {
		t.Fatalf("expected 1 token named agent, got %+v", list)
	}

	// Touch last_used_at, then confirm it surfaces in the list.
	if err := st.TouchAPITokenLastUsed(ctx, tok.ID); err != nil {
		t.Fatalf("touch: %v", err)
	}
	list, err = st.ListAPITokensByUser(ctx, owner.ID)
	if err != nil {
		t.Fatalf("list after touch: %v", err)
	}
	if list[0].LastUsedAt == nil {
		t.Error("expected last_used_at to be populated after touch")
	}

	// Delete (owner-scoped).
	if err := st.DeleteAPIToken(ctx, tok.ID, owner.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetAPITokenByHash(ctx, "hash-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestAPIToken_CreateWithExpiry(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "exp-owner", "hash", "user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	exp := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	tok, err := st.CreateAPIToken(ctx, owner.ID, "expiring", "hash-exp", &exp)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tok.ExpiresAt == nil {
		t.Fatal("expected expiry to be set")
	}
	idn, err := st.GetAPITokenByHash(ctx, "hash-exp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if idn.ExpiresAt == nil || !idn.ExpiresAt.Equal(exp) {
		t.Errorf("expiry round-trip mismatch: got %v want %v", idn.ExpiresAt, exp)
	}
}

func TestAPIToken_GetByHash_NotFound(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.GetAPITokenByHash(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestAPIToken_Delete_WrongOwner(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	alice, err := st.CreateUser(ctx, "alice-del", "hash", "user")
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := st.CreateUser(ctx, "bob-del", "hash", "user")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	tok, err := st.CreateAPIToken(ctx, alice.ID, "alice-token", "hash-aw", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// Bob can't delete Alice's token → ErrNotFound (ownership-scoped).
	if err := st.DeleteAPIToken(ctx, tok.ID, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for wrong owner, got %v", err)
	}
	// Alice's token still resolves.
	if _, err := st.GetAPITokenByHash(ctx, "hash-aw"); err != nil {
		t.Errorf("alice token should still exist: %v", err)
	}
}

func TestAPIToken_ListEmpty(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	owner, err := st.CreateUser(ctx, "empty-owner", "hash", "user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	list, err := st.ListAPITokensByUser(ctx, owner.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list == nil {
		t.Error("expected non-nil empty slice")
	}
	if len(list) != 0 {
		t.Errorf("expected 0 tokens, got %d", len(list))
	}
}

func TestAPIToken_StoreClosedErrors(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	owner, err := st.CreateUser(ctx, "closed-owner", "hash", "user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	tok, err := st.CreateAPIToken(ctx, owner.ID, "t", "hash-closed", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	st.Close()

	if _, err := st.CreateAPIToken(ctx, owner.ID, "x", "hash-x", nil); err == nil {
		t.Error("CreateAPIToken: expected error on closed store")
	}
	if _, err := st.GetAPITokenByHash(ctx, "hash-closed"); err == nil {
		t.Error("GetAPITokenByHash: expected error on closed store")
	}
	if _, err := st.ListAPITokensByUser(ctx, owner.ID); err == nil {
		t.Error("ListAPITokensByUser: expected error on closed store")
	}
	if err := st.DeleteAPIToken(ctx, tok.ID, owner.ID); err == nil {
		t.Error("DeleteAPIToken: expected error on closed store")
	}
	if err := st.TouchAPITokenLastUsed(ctx, tok.ID); err == nil {
		t.Error("TouchAPITokenLastUsed: expected error on closed store")
	}
}

func TestAPIToken_CascadeOnUserDelete(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	owner, err := st.CreateUser(ctx, "cascade-owner", "hash", "user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := st.CreateAPIToken(ctx, owner.ID, "doomed", "hash-cascade", nil); err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := st.DeleteUser(ctx, owner.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	// ON DELETE CASCADE should have removed the token.
	if _, err := st.GetAPITokenByHash(ctx, "hash-cascade"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected token gone after owner delete, got %v", err)
	}
}
