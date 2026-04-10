// Package connector defines the interface for data source connectors and their registry.
package connector

import (
	"context"
	"io"

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

// BinaryContent carries the bytes of a previewable/downloadable document along
// with the metadata the HTTP layer needs to set response headers.
type BinaryContent struct {
	Reader   io.ReadCloser
	MimeType string
	Size     int64
	Filename string
}

// BinaryFetcher is an optional capability for connectors whose source documents
// have downloadable bytes (files, attachments, media). The download endpoint
// type-asserts a Connector to this interface to dispatch preview/download
// requests. Connectors that don't implement it cannot be previewed.
type BinaryFetcher interface {
	// FetchBinary returns the raw bytes for the document identified by sourceID.
	// The caller is responsible for closing the returned Reader.
	FetchBinary(ctx context.Context, sourceID string) (*BinaryContent, error)
}
