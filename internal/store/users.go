package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/muty/nexus/internal/model"
)

// ErrDuplicateUsername is returned when a username is already taken.
var ErrDuplicateUsername = errors.New("username already exists")

// CreateUser creates a new user with the given password hash and role.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string) (*model.User, error) {
	id := uuid.New()
	now := time.Now()

	query := `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := s.pool.Exec(ctx, query, id, username, passwordHash, role, now, now)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateUsername
		}
		return nil, fmt.Errorf("store: create user: %w", err)
	}

	return &model.User{
		ID:        id,
		Username:  username,
		Role:      role,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// GetUserByUsername returns a user and their password hash by username.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, string, error) {
	query := `SELECT id, username, password_hash, role, created_at, updated_at FROM users WHERE username = $1`
	var user model.User
	var passwordHash string
	err := s.pool.QueryRow(ctx, query, username).Scan(
		&user.ID, &user.Username, &passwordHash, &user.Role, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("store: get user by username: %w", err)
	}
	return &user, passwordHash, nil
}

// GetUserByID returns a user by ID.
func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (*model.User, error) {
	query := `SELECT id, username, role, created_at, updated_at FROM users WHERE id = $1`
	var user model.User
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&user.ID, &user.Username, &user.Role, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get user by id: %w", err)
	}
	return &user, nil
}

// ListUsers returns all users.
func (s *Store) ListUsers(ctx context.Context) ([]model.User, error) {
	query := `SELECT id, username, role, created_at, updated_at FROM users ORDER BY created_at`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list users: %w", err)
	}
	defer rows.Close()

	var users []model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan user: %w", err)
		}
		users = append(users, u)
	}
	if users == nil {
		users = []model.User{}
	}
	return users, nil
}

// DeleteUser deletes a user by ID.
func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: delete user: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateUserPassword updates a user's password hash.
func (s *Store) UpdateUserPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	result, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`, passwordHash, id)
	if err != nil {
		return fmt.Errorf("store: update password: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountUsers returns the total number of users.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count users: %w", err)
	}
	return count, nil
}
