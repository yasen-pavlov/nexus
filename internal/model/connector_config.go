package model

import (
	"time"

	"github.com/google/uuid"
)

type ConnectorConfig struct {
	ID        uuid.UUID      `json:"id"`
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Config    map[string]any `json:"config"`
	Enabled   bool           `json:"enabled"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}
