package cliclient

import (
	"context"
	"net/http"
	"time"

	"github.com/muty/nexus/internal/model"
)

// Connector is one row from GET /api/connectors: a ConnectorConfig plus the
// in-memory Status ("active" = loaded/enabled at boot, "inactive" otherwise —
// NOT a live sync state; use SyncJob for that). Config is server-masked.
type Connector struct {
	model.ConnectorConfig
	Status string `json:"status"`
}

// ListConnectors returns the caller's own + shared connectors.
func (c *Client) ListConnectors(ctx context.Context) ([]Connector, error) {
	var res []Connector
	if err := c.do(ctx, http.MethodGet, "/api/connectors", nil, nil, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// SyncJob is the start-snapshot / live status of a sync run. CompletedAt is the
// zero value until the job reaches a terminal Status.
type SyncJob struct {
	ID            string    `json:"id"`
	ConnectorID   string    `json:"connector_id"`
	ConnectorName string    `json:"connector_name"`
	ConnectorType string    `json:"connector_type"`
	Status        string    `json:"status"` // running | completed | failed | canceled | interrupted
	DocsTotal     int       `json:"docs_total"`
	DocsProcessed int       `json:"docs_processed"`
	DocsDeleted   int       `json:"docs_deleted"`
	Errors        int       `json:"errors"`
	Error         string    `json:"error,omitempty"`
	Scope         string    `json:"scope,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   time.Time `json:"completed_at,omitzero"`
}

// TriggerSync starts a sync for one connector and returns the start-snapshot
// job. A 409 *APIError means a sync is already running for that connector; a 404
// means not found, disabled, or not accessible.
func (c *Client) TriggerSync(ctx context.Context, connectorID string) (*SyncJob, error) {
	var job SyncJob
	if err := c.do(ctx, http.MethodPost, "/api/sync/"+connectorID, nil, nil, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// SyncAll starts a sync for every eligible connector; already-running or
// non-modifiable ones are silently skipped. Returns the jobs actually started
// (may be empty).
func (c *Client) SyncAll(ctx context.Context) ([]SyncJob, error) {
	var jobs []SyncJob
	if err := c.do(ctx, http.MethodPost, "/api/sync", nil, nil, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// ListSyncJobs returns running + recently-completed jobs for readable connectors.
func (c *Client) ListSyncJobs(ctx context.Context) ([]SyncJob, error) {
	var jobs []SyncJob
	if err := c.do(ctx, http.MethodGet, "/api/sync", nil, nil, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}
