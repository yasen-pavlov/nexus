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

	// Fetch streams documents, enumeration markers, and cursor
	// checkpoints for incremental sync. A nil cursor indicates a
	// first-time full sync.
	//
	// Both channels are returned non-nil immediately. The implementation
	// spawns a goroutine that emits FetchItems on the items channel and
	// closes it when finished; the errors channel carries at most one
	// terminal error (or nil) and is then closed. The pipeline is
	// responsible for reading both to completion. Context cancellation
	// triggers a graceful drain and a best-effort final checkpoint.
	//
	// Emission order contract: items of type SourceID MUST be emitted
	// in UTF-8 lexicographic order matching OpenSearch's
	// `source_id.keyword` sort. Violating this breaks the streaming
	// merge-diff in the pipeline.
	Fetch(ctx context.Context, cursor *model.SyncCursor) (<-chan model.FetchItem, <-chan error)
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

// BinaryStoreAPI is the subset of internal/storage.BinaryStore methods that
// cache-aware connectors call. Declared here so internal/connector doesn't
// depend on internal/storage (which depends transitively on internal/store)
// and so fakes are trivial to write in tests.
type BinaryStoreAPI interface {
	Put(ctx context.Context, sourceType, sourceName, sourceID string, r io.Reader, size int64) error
	Get(ctx context.Context, sourceType, sourceName, sourceID string) (io.ReadCloser, error)
	Exists(ctx context.Context, sourceType, sourceName, sourceID string) (bool, error)
}

// CacheConfig is the runtime cache policy for a connector instance.
// Mirrors storage.CacheConfig but lives here so connectors don't need
// to import internal/storage. The ConnectorManager translates
// storage.CacheConfig to this struct when injecting.
type CacheConfig struct {
	// Mode is "none", "lazy", or "eager". Connectors consult this to
	// decide whether to populate the cache during Fetch (eager) or on
	// first FetchBinary (lazy), or skip caching entirely (none).
	Mode string
}

// BinaryStoreSetter is an optional capability for connectors that
// participate in the binary cache. Connectors that accept caching (IMAP,
// Telegram) implement this to receive the BinaryStore and their
// resolved policy at wire-up time. Named following Go's -er convention
// for single-method interfaces.
type BinaryStoreSetter interface {
	SetBinaryStore(store BinaryStoreAPI, config CacheConfig)
}
