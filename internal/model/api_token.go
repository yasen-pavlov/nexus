package model

import (
	"time"

	"github.com/google/uuid"
)

// APIToken is a long-lived personal access token an external agent/CLI uses
// to authenticate against the API as its owning user. It carries metadata
// only — the secret itself is never stored (only a SHA-256 hash) and is
// surfaced to the caller exactly once, at creation.
type APIToken struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}
