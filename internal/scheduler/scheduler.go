// Package scheduler provides cron-based automatic sync for connectors.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

// ConnectorGetter retrieves active connector instances by UUID.
type ConnectorGetter interface {
	GetByID(id uuid.UUID) (connector.Connector, *model.ConnectorConfig, bool)
}

// PipelineRunner runs the ingestion pipeline for a connector.
type PipelineRunner interface {
	RunWithProgress(ctx context.Context, connectorID uuid.UUID, conn connector.Connector, ownerID string, shared bool, progress pipeline.ProgressFunc) (*pipeline.SyncReport, error)
}

// ConfigLister lists connector configs from the database.
type ConfigLister interface {
	ListConnectorConfigs(ctx context.Context) ([]model.ConnectorConfig, error)
	UpdateLastRun(ctx context.Context, id uuid.UUID, t time.Time) error
}

// JobManager is the subset of api.SyncJobManager the scheduler uses.
// Routing scheduled runs through the same manager as manual triggers means
// they persist to sync_runs (visible in the Activity tab), emit SSE
// progress frames (visible in the top-bar strip), and are cancellable
// through the same endpoint — the frontend has one uniform story.
//
// Can be nil: a nil JobManager falls back to the legacy path where
// scheduler calls pipeline.RunWithProgress directly. Used in tests that
// predate the unification.
type JobManager interface {
	StartForSchedule(connectorID uuid.UUID, name, connectorType string) (jobID string, runCtx context.Context, err error)
	Update(jobID string, total, processed, errors int)
	SetDeleted(jobID string, deleted int)
	Complete(jobID string, err error)
}

// Scheduler manages cron jobs for automatic connector syncs.
//
// Context handling: cron callbacks take no arguments, so the
// process-lifetime context passed into Start is captured into each
// cron job's closure at addJob time rather than stored on the struct.
// OnConnectorChanged / OnConnectorRemoved also take a context so
// late-arriving schedule changes capture the caller's context (typically
// the HTTP request's) into the same closure shape.
type Scheduler struct {
	cron    *cron.Cron
	cm      ConnectorGetter
	pipe    PipelineRunner
	store   ConfigLister
	jobs    JobManager // may be nil
	log     *zap.Logger
	mu      sync.Mutex
	entries map[uuid.UUID]cron.EntryID
}

// New creates a new Scheduler.
func New(cm ConnectorGetter, pipe PipelineRunner, store ConfigLister, log *zap.Logger) *Scheduler {
	return &Scheduler{
		cron:    cron.New(),
		cm:      cm,
		pipe:    pipe,
		store:   store,
		log:     log,
		entries: make(map[uuid.UUID]cron.EntryID),
	}
}

// SetJobManager wires the SyncJobManager so scheduled runs pass through
// the same Start/Complete lifecycle as manual triggers — persisting to
// sync_runs + emitting SSE progress to any connected client. Call before
// Start(); safe to omit in tests.
func (s *Scheduler) SetJobManager(jm JobManager) {
	s.jobs = jm
}

// Start loads scheduled connectors from the database and starts the cron runner.
func (s *Scheduler) Start(ctx context.Context) error {
	configs, err := s.store.ListConnectorConfigs(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: load configs: %w", err)
	}

	for _, cfg := range configs {
		if cfg.Enabled && cfg.Schedule != "" {
			s.addJob(ctx, cfg)
		}
	}

	s.cron.Start()
	s.log.Info("scheduler started", zap.Int("jobs", len(s.entries)))
	return nil
}

// Stop stops the cron runner and waits for running jobs to complete.
func (s *Scheduler) Stop() {
	stopCtx := s.cron.Stop()
	<-stopCtx.Done()
	s.log.Info("scheduler stopped")
}

// OnConnectorChanged is called when a connector is created or updated.
// ctx is captured into the cron job's closure so late-arriving schedule
// changes still have a context for cron-triggered syncs and the
// post-sync UpdateLastRun write — typically the HTTP request's context.
func (s *Scheduler) OnConnectorChanged(ctx context.Context, cfg *model.ConnectorConfig) {
	if cfg.Enabled && cfg.Schedule != "" {
		s.addJob(ctx, *cfg)
	} else {
		s.removeJob(cfg.ID)
	}
}

// OnConnectorRemoved is called when a connector is deleted.
func (s *Scheduler) OnConnectorRemoved(_ context.Context, id uuid.UUID, _ string) {
	s.removeJob(id)
}

func (s *Scheduler) addJob(ctx context.Context, cfg model.ConnectorConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove existing entry if any
	if eid, ok := s.entries[cfg.ID]; ok {
		s.cron.Remove(eid)
		delete(s.entries, cfg.ID)
	}

	connName := cfg.Name
	connID := cfg.ID

	eid, err := s.cron.AddFunc(cfg.Schedule, func() {
		s.runSync(ctx, connID)
	})
	if err != nil {
		s.log.Error("failed to add cron job",
			zap.String("connector", connName),
			zap.String("schedule", cfg.Schedule),
			zap.Error(err),
		)
		return
	}

	s.entries[cfg.ID] = eid
	s.log.Info("scheduled connector", zap.String("connector", connName), zap.String("schedule", cfg.Schedule))
}

func (s *Scheduler) removeJob(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if eid, ok := s.entries[id]; ok {
		s.cron.Remove(eid)
		delete(s.entries, id)
		s.log.Info("unscheduled connector", zap.String("id", id.String()))
	}
}

// runSync executes one cron-triggered sync. ctx is the process-lifetime
// context captured into the cron closure at addJob time; it's used as
// the fallback for RunWithProgress (legacy path) and for the
// post-sync UpdateLastRun write.
func (s *Scheduler) runSync(ctx context.Context, id uuid.UUID) {
	conn, cfg, ok := s.cm.GetByID(id)
	if !ok {
		s.log.Warn("scheduled sync skipped: connector not found", zap.String("id", id.String()))
		return
	}

	name := cfg.Name
	ownerID := ""
	if cfg.UserID != nil {
		ownerID = cfg.UserID.String()
	}

	s.log.Info("scheduled sync starting", zap.String("connector", name))

	// Prefer routing through the SyncJobManager when wired, so the run
	// is tracked in sync_runs and streams SSE progress just like manual
	// triggers. The legacy direct-pipeline path remains for tests /
	// deployments that wire the scheduler without a JobManager.
	if s.jobs != nil {
		jobID, runCtx, err := s.jobs.StartForSchedule(id, name, conn.Type())
		if err != nil {
			// ErrAlreadyRunning is a silent skip — a manual trigger is
			// already syncing this connector. Other errors are logged.
			s.log.Info("scheduled sync skipped",
				zap.String("connector", name),
				zap.Error(err))
			return
		}
		progress := func(total, processed, errors int) {
			s.jobs.Update(jobID, total, processed, errors)
		}
		report, runErr := s.pipe.RunWithProgress(runCtx, id, conn, ownerID, cfg.Shared, progress)
		if report != nil {
			s.jobs.SetDeleted(jobID, report.DocsDeleted)
		}
		s.jobs.Complete(jobID, runErr)

		if runErr != nil {
			s.log.Error("scheduled sync failed", zap.String("connector", name), zap.Error(runErr))
		} else if report != nil {
			s.log.Info("scheduled sync completed",
				zap.String("connector", name),
				zap.Int("docs", report.DocsProcessed),
				zap.Duration("duration", report.Duration),
			)
		}
		// connector_configs.last_run stays for now — see Phase 4 in the
		// plan for retiring it once the Activity timeline is the single
		// source of truth for recency.
		if runErr == nil {
			if err := s.store.UpdateLastRun(ctx, id, time.Now()); err != nil {
				s.log.Error("failed to update last_run", zap.String("connector", name), zap.Error(err))
			}
		}
		return
	}

	// Legacy path — scheduler not wired to a JobManager.
	report, err := s.pipe.RunWithProgress(ctx, id, conn, ownerID, cfg.Shared, nil)
	if err != nil {
		s.log.Error("scheduled sync failed", zap.String("connector", name), zap.Error(err))
		return
	}

	if err := s.store.UpdateLastRun(ctx, id, time.Now()); err != nil {
		s.log.Error("failed to update last_run", zap.String("connector", name), zap.Error(err))
	}

	s.log.Info("scheduled sync completed",
		zap.String("connector", name),
		zap.Int("docs", report.DocsProcessed),
		zap.Duration("duration", report.Duration),
	)
}
