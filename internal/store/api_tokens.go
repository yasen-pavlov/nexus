package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/muty/nexus/internal/model"
)

// APITokenIdentity is the result of resolving an API token by its hash. It
// carries everything the auth layer needs to act as the token's owning user
// (id, username, role resolved live from the users table) plus the token's
// own id and expiry for the last-used touch and expiry check.
type APITokenIdentity struct {
	TokenID   uuid.UUID
	UserID    uuid.UUID
	Username  string
	Role      string
	ExpiresAt *time.Time
}

// CreateAPIToken inserts a new token row. tokenHash is the SHA-256 hex of the
// plaintext (the plaintext is never stored). expiresAt nil means never.
func (s *Store) CreateAPIToken(ctx context.Context, userID uuid.UUID, name, tokenHash string, expiresAt *time.Time) (*model.APIToken, error) {
	id := uuid.New()
	now := time.Now()

	query := `INSERT INTO api_tokens (id, user_id, name, token_hash, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := s.pool.Exec(ctx, query, id, userID, name, tokenHash, now, expiresAt); err != nil {
		return nil, fmt.Errorf("store: create api token: %w", err)
	}

	return &model.APIToken{
		ID:        id,
		UserID:    userID,
		Name:      name,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}, nil
}

// GetAPITokenByHash resolves a token by its SHA-256 hash, joining users so the
// owner's current role is read live (a demoted owner's tokens immediately lose
// privileges; a deleted owner's rows are gone via ON DELETE CASCADE, yielding
// ErrNotFound here). Expiry is NOT enforced here — the caller checks ExpiresAt
// so it can distinguish "unknown token" from "expired token" if needed.
func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string) (*APITokenIdentity, error) {
	query := `SELECT t.id, t.user_id, u.username, u.role, t.expires_at
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1`
	var idn APITokenIdentity
	err := s.pool.QueryRow(ctx, query, tokenHash).Scan(
		&idn.TokenID, &idn.UserID, &idn.Username, &idn.Role, &idn.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get api token by hash: %w", err)
	}
	return &idn, nil
}

// ListAPITokensByUser returns a user's tokens (metadata only — never the
// secret), newest first.
func (s *Store) ListAPITokensByUser(ctx context.Context, userID uuid.UUID) ([]model.APIToken, error) {
	query := `SELECT id, user_id, name, created_at, last_used_at, expires_at
		FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list api tokens: %w", err)
	}
	defer rows.Close()

	tokens := []model.APIToken{}
	for rows.Next() {
		var t model.APIToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt); err != nil {
			return nil, fmt.Errorf("store: scan api token: %w", err)
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate api tokens: %w", err)
	}
	return tokens, nil
}

// DeleteAPIToken removes a token, scoped to its owner so one user can't revoke
// another's token. Returns ErrNotFound when no row matches (unknown id, or the
// token belongs to someone else — the handler maps both to 404 so token
// existence doesn't leak across users).
func (s *Store) DeleteAPIToken(ctx context.Context, id, userID uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("store: delete api token: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchAPITokenLastUsed best-effort updates last_used_at. Callers throttle how
// often this runs (it's telemetry, not correctness) so the write isn't issued
// on every single request.
func (s *Store) TouchAPITokenLastUsed(ctx context.Context, id uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, `UPDATE api_tokens SET last_used_at = now() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("store: touch api token: %w", err)
	}
	return nil
}
