// Package connector defines the interface for data source connectors and their registry.
package connector

import (
	"context"

	"github.com/muty/nexus/internal/model"
)

type Config map[string]any

type Connector interface {
	// Type returns the connector type (e.g., "filesystem", "imap").
	Type() string

	// Name returns the configured instance name (e.g., "my-notes").
	Name() string

	// Configure sets up the connector with the given configuration.
	Configure(cfg Config) error

	// Validate checks that the connector is properly configured.
	Validate() error

	// Fetch retrieves documents from the source, using the cursor for incremental sync.
	// A nil cursor indicates a first-time full sync.
	Fetch(ctx context.Context, cursor *model.SyncCursor) (*model.FetchResult, error)
}
