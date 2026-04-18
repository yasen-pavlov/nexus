package model

import (
	"time"

	"github.com/google/uuid"
)

// SyncRun is one persisted execution of a connector's sync pipeline. The
// row is inserted at the start of a sync and updated on completion with
// terminal status and final counts. Keyed by the same UUID as the
// in-memory api.SyncJob so live progress (SSE) and persisted history
// correlate cleanly.
//
// Status values: "running", "completed", "failed", "canceled".
type SyncRun struct {
	ID            uuid.UUID  `json:"id"`
	ConnectorID   uuid.UUID  `json:"connector_id"`
	Status        string     `json:"status"`
	DocsTotal     int        `json:"docs_total"`
	DocsProcessed int        `json:"docs_processed"`
	DocsDeleted   int        `json:"docs_deleted"`
	Errors        int        `json:"errors"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}
