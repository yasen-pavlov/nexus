package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/muty/nexus/internal/model"
)

func (s *Store) GetSyncCursor(ctx context.Context, connectorID string) (*model.SyncCursor, error) {
	query := `SELECT connector_id, cursor_data, last_sync, last_status, items_synced FROM sync_cursors WHERE connector_id = $1`
	row := s.pool.QueryRow(ctx, query, connectorID)

	var cursor model.SyncCursor
	var cursorData []byte
	err := row.Scan(&cursor.ConnectorID, &cursorData, &cursor.LastSync, &cursor.LastStatus, &cursor.ItemsSynced)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // no cursor yet, first sync
		}
		return nil, fmt.Errorf("store: get sync cursor: %w", err)
	}

	if err := json.Unmarshal(cursorData, &cursor.CursorData); err != nil {
		return nil, fmt.Errorf("store: unmarshal cursor data: %w", err)
	}
	return &cursor, nil
}

func (s *Store) UpsertSyncCursor(ctx context.Context, cursor *model.SyncCursor) error {
	cursorData, err := json.Marshal(cursor.CursorData)
	if err != nil {
		return fmt.Errorf("store: marshal cursor data: %w", err)
	}

	query := `
		INSERT INTO sync_cursors (connector_id, cursor_data, last_sync, last_status, items_synced)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (connector_id)
		DO UPDATE SET
			cursor_data = EXCLUDED.cursor_data,
			last_sync = EXCLUDED.last_sync,
			last_status = EXCLUDED.last_status,
			items_synced = EXCLUDED.items_synced
	`
	_, err = s.pool.Exec(ctx, query,
		cursor.ConnectorID, cursorData, cursor.LastSync, cursor.LastStatus, cursor.ItemsSynced,
	)
	if err != nil {
		return fmt.Errorf("store: upsert sync cursor: %w", err)
	}
	return nil
}
