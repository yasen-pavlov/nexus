package model

import (
	"time"

	"github.com/google/uuid"
)

type ConnectorConfig struct {
	ID       uuid.UUID      `json:"id"`
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	Config   map[string]any `json:"config"`
	Enabled  bool           `json:"enabled"`
	Schedule string         `json:"schedule"`
	Shared   bool           `json:"shared"`
	UserID   *uuid.UUID     `json:"user_id,omitempty"`
	// ExternalID and ExternalName identify who the Nexus user IS on the
	// external system this connector represents (e.g. own Telegram user
	// ID + display name after OAuth). Empty when the connector has no
	// notion of "self" (filesystem) or hasn't completed auth yet. Used
	// by the /api/me/identities endpoint to drive self-aware UI.
	ExternalID   string     `json:"external_id,omitempty"`
	ExternalName string     `json:"external_name,omitempty"`
	LastRun      *time.Time `json:"last_run"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	// CredentialsUnreadable is a transient, read-time-only flag (never
	// persisted): it is set true when this row's encrypted secret fields
	// could not be decrypted with the active NEXUS_ENCRYPTION_KEY. When true,
	// the sensitive fields are stripped from Config so no ciphertext leaks,
	// and the connector loads inactive — the owner can re-enter the secret to
	// restore it. A single such row no longer bricks boot for every connector.
	CredentialsUnreadable bool `json:"credentials_unreadable,omitempty"`
}
