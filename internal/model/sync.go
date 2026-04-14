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

// FetchResult is what a connector hands back to the pipeline after a sync
// pass. The three fields carry different signals and use different
// conventions for "unset":
//
//   - Documents: new or updated docs to index this sync.
//   - Cursor: new sync-cursor state to persist for the next incremental run.
//   - CurrentSourceIDs: the full set of source_ids the connector believes
//     still exist upstream, used by the pipeline to delete stale docs. Three
//     states:
//   - nil — connector did not enumerate (Telegram's case) or its
//     enumeration errored partway. Pipeline skips the deletion pass.
//   - empty slice — connector enumerated successfully and nothing
//     exists upstream. Pipeline deletes every indexed chunk for this
//     (source_type, source_name).
//   - non-empty slice — authoritative list. Pipeline deletes any
//     indexed source_id not in this slice.
//
// Enumeration must be all-or-nothing. A half-filled slice from a partial
// enumeration would trigger false-positive deletions — connectors that
// can't guarantee completeness must return nil.
//
// Colon-suffix children are preserved implicitly. A source_id `X` in
// CurrentSourceIDs also preserves any indexed source_id that starts
// with `X:` — e.g. IMAP emits email UIDs `INBOX:42` as parents and the
// pipeline keeps their attachments `INBOX:42:attachment:0`, `...:1`
// without the connector having to enumerate attachment indices. The
// colon delimiter avoids prefix collisions (`INBOX:42` doesn't
// preserve `INBOX:420:whatever`).
type FetchResult struct {
	Documents        []Document  `json:"documents"`
	Cursor           *SyncCursor `json:"cursor"`
	CurrentSourceIDs []string    `json:"current_source_ids,omitempty"`
}
