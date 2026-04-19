package model

import (
	"time"

	"github.com/google/uuid"
)

// User represents an authenticated user.
type User struct {
	ID           uuid.UUID `json:"id"`
	Username     string    `json:"username"`
	Role         string    `json:"role"` // "admin" or "user"
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	TokenVersion int       `json:"-"` // bumped on password change to revoke prior JWTs
}
