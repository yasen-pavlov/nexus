package model

import (
	"time"

	"github.com/google/uuid"
)

type SyncCursor struct {
	ConnectorID uuid.UUID      `json:"connector_id"`
	CursorData  map[string]any `json:"cursor_data"`
	LastSync    time.Time      `json:"last_sync"`
	LastStatus  string         `json:"last_status"`
	ItemsSynced int            `json:"items_synced"`
}

// FetchItem is a single emission on a connector's Fetch stream.
// Exactly one "payload" field is set per item; the pipeline
// dispatches on which one is non-nil.
//
//   - Doc: a document to index. The pipeline buffers docs and flushes
//     them to OpenSearch in bulk at checkpoint/time boundaries.
//   - SourceID: an enumeration marker used for deletion reconciliation.
//     The pipeline merges these (in the connector's emission order)
//     against OpenSearch's own sorted source_id stream and deletes
//     any indexed ID the connector doesn't claim. Emission MUST be in
//     UTF-8 lexicographic order matching the OpenSearch
//     `source_id.keyword` sort — otherwise the two-pointer merge-diff
//     will emit spurious deletions.
//   - EnumerationComplete: signals "the SourceID stream for this run
//     is authoritative; reconcile even if it was empty." Connectors
//     that enumerate emit this exactly once at the end of their
//     enumeration pass. Without it, a run that emitted zero SourceIDs
//     is treated as "opt out of reconciliation" (the Telegram case).
//     Emitting SourceIDs implicitly opts in too; the marker is only
//     strictly required to wipe-all-on-empty-enumeration.
//   - Checkpoint: a safe-resume marker. Pipeline persists the cursor
//     after flushing any buffered docs, so a checkpoint means "every
//     Doc before me is durable; resume from this cursor on crash."
//   - EstimatedTotal: optional hint for progress reporting. Reported
//     totals may grow as the stream progresses (e.g. IMAP reports a
//     new total after each folder's enumeration); the UI clamps
//     displayed totals so they never regress.
//   - Scope: optional free-form label naming the current sub-unit
//     the connector is working on — an IMAP folder name, a
//     Telegram chat title, a Paperless page. The pipeline stamps
//     it onto the SyncJob so the UI can show "Syncing Archive…"
//     instead of a bare N/M counter. Emit once when entering a
//     new scope; an empty-string value clears the label.
//
// Deletion-reconciliation gating: pending deletions computed during
// merge-diff are only flushed when the connector's error channel
// yields nil (i.e. normal close). A non-nil error or context
// cancellation discards the pending-delete queue — missed deletions
// are picked up on the next full run.
type FetchItem struct {
	Doc                 *Document
	SourceID            *string
	EnumerationComplete bool
	Checkpoint          *SyncCursor
	EstimatedTotal      *int64
	Scope               *string
}
