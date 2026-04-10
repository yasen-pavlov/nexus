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

type FetchResult struct {
	Documents []Document  `json:"documents"`
	Cursor    *SyncCursor `json:"cursor"`
}
