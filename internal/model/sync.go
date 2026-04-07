package model

import "time"

type SyncCursor struct {
	ConnectorID string         `json:"connector_id"`
	CursorData  map[string]any `json:"cursor_data"`
	LastSync    time.Time      `json:"last_sync"`
	LastStatus  string         `json:"last_status"`
	ItemsSynced int            `json:"items_synced"`
}

type FetchResult struct {
	Documents []Document  `json:"documents"`
	Cursor    *SyncCursor `json:"cursor"`
}
