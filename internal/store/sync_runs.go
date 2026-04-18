package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/muty/nexus/internal/model"
)

// maxSyncRunListLimit caps the per-request page size for ListSyncRunsByConnector.
// Higher values risk returning megabytes of stale history to the UI for no
// practical benefit; the Activity tab only shows the most recent runs.
const maxSyncRunListLimit = 200

// InsertSyncRun writes a new sync run row at the start of a sync. The caller
// (api.SyncJobManager) is expected to generate the UUID and pass status
// "running" plus the started_at timestamp — completed_at stays NULL until
// the matching UpdateSyncRunComplete call.
func (s *Store) InsertSyncRun(ctx context.Context, run *model.SyncRun) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sync_runs
		  (id, connector_id, status, docs_total, docs_processed, docs_deleted, errors, error_message, started_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		run.ID, run.ConnectorID, run.Status,
		run.DocsTotal, run.DocsProcessed, run.DocsDeleted, run.Errors,
		run.ErrorMessage, run.StartedAt, run.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("store: insert sync run: %w", err)
	}
	return nil
}

// UpdateSyncRunComplete finalizes a sync run row with terminal status and
// final counts. Called from api.SyncJobManager.Complete. ErrNotFound is
// returned if no row with the given id exists (should not happen in normal
// operation since the row was inserted at Start).
func (s *Store) UpdateSyncRunComplete(
	ctx context.Context,
	id uuid.UUID,
	status string,
	docsTotal, docsProcessed, docsDeleted, errCount int,
	errorMessage string,
	completedAt time.Time,
) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE sync_runs
		 SET status = $2,
		     docs_total = $3,
		     docs_processed = $4,
		     docs_deleted = $5,
		     errors = $6,
		     error_message = $7,
		     completed_at = $8
		 WHERE id = $1`,
		id, status, docsTotal, docsProcessed, docsDeleted, errCount, errorMessage, completedAt,
	)
	if err != nil {
		return fmt.Errorf("store: update sync run complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetSyncRun returns a single sync run by ID or ErrNotFound.
func (s *Store) GetSyncRun(ctx context.Context, id uuid.UUID) (*model.SyncRun, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, connector_id, status, docs_total, docs_processed, docs_deleted, errors, error_message, started_at, completed_at
		 FROM sync_runs
		 WHERE id = $1`,
		id,
	)
	run, err := scanSyncRun(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get sync run: %w", err)
	}
	return run, nil
}

// ListSyncRunsByConnector returns the most recent sync runs for a connector,
// newest first. limit is clamped to [1, maxSyncRunListLimit]. Returns an
// empty slice if no runs exist.
func (s *Store) ListSyncRunsByConnector(ctx context.Context, connectorID uuid.UUID, limit int) ([]model.SyncRun, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxSyncRunListLimit {
		limit = maxSyncRunListLimit
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, connector_id, status, docs_total, docs_processed, docs_deleted, errors, error_message, started_at, completed_at
		 FROM sync_runs
		 WHERE connector_id = $1
		 ORDER BY started_at DESC
		 LIMIT $2`,
		connectorID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list sync runs by connector: %w", err)
	}
	defer rows.Close()

	out := make([]model.SyncRun, 0)
	for rows.Next() {
		run, err := scanSyncRun(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan sync run: %w", err)
		}
		out = append(out, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate sync runs: %w", err)
	}
	return out, nil
}

// MarkInterruptedStuckRuns rewrites every sync_runs row still sitting at
// status='running' (which means the process crashed or was force-killed
// before Complete could update it) to status='interrupted' with a
// timestamp and a canned error message. Called once at boot, before the
// scheduler starts — so the UI's Activity timeline never shows a run
// that can't finish.
//
// Returns the number of rows rewritten.
func (s *Store) MarkInterruptedStuckRuns(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE sync_runs
		 SET status = 'interrupted',
		     completed_at = COALESCE(completed_at, NOW()),
		     error_message = CASE
		         WHEN error_message = '' THEN 'Nexus was restarted before this sync finished'
		         ELSE error_message
		     END
		 WHERE status = 'running'`,
	)
	if err != nil {
		return 0, fmt.Errorf("store: mark interrupted stuck runs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteSyncRunsOlderThan removes terminal sync_runs rows started before
// cutoff. Running rows are never deleted — MarkInterruptedStuckRuns
// handles that class separately. Returns rows deleted.
func (s *Store) DeleteSyncRunsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM sync_runs
		 WHERE started_at < $1
		   AND status <> 'running'`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("store: delete sync runs older than: %w", err)
	}
	return tag.RowsAffected(), nil
}

// TrimSyncRunsPerConnector keeps only the most recent `keep` terminal
// runs per connector. Useful when max-age alone leaves pathological
// connectors (which sync every 5 minutes) with tens of thousands of rows
// inside the retention window. Running rows are preserved. Returns rows
// deleted.
func (s *Store) TrimSyncRunsPerConnector(ctx context.Context, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM sync_runs
		 WHERE id IN (
		     SELECT id FROM (
		         SELECT id,
		                ROW_NUMBER() OVER (PARTITION BY connector_id ORDER BY started_at DESC) AS rn,
		                status
		         FROM sync_runs
		     ) ranked
		     WHERE ranked.rn > $1
		       AND ranked.status <> 'running'
		 )`,
		keep,
	)
	if err != nil {
		return 0, fmt.Errorf("store: trim sync runs per connector: %w", err)
	}
	return tag.RowsAffected(), nil
}

// syncRunScanner accepts either *pgx.Row (from QueryRow) or pgx.Rows
// (from Query) — both implement a compatible Scan method.
type syncRunScanner interface {
	Scan(dest ...any) error
}

func scanSyncRun(sc syncRunScanner) (*model.SyncRun, error) {
	var run model.SyncRun
	var completedAt *time.Time
	if err := sc.Scan(
		&run.ID,
		&run.ConnectorID,
		&run.Status,
		&run.DocsTotal,
		&run.DocsProcessed,
		&run.DocsDeleted,
		&run.Errors,
		&run.ErrorMessage,
		&run.StartedAt,
		&completedAt,
	); err != nil {
		return nil, err
	}
	run.CompletedAt = completedAt
	return &run, nil
}
