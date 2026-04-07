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

// ConnectorGetter retrieves active connector instances by name.
type ConnectorGetter interface {
	Get(name string) (connector.Connector, bool)
}

// PipelineRunner runs the ingestion pipeline for a connector.
type PipelineRunner interface {
	Run(ctx context.Context, conn connector.Connector) (*pipeline.SyncReport, error)
}

// ConfigLister lists connector configs from the database.
type ConfigLister interface {
	ListConnectorConfigs(ctx context.Context) ([]model.ConnectorConfig, error)
	UpdateLastRun(ctx context.Context, id uuid.UUID, t time.Time) error
}

// Scheduler manages cron jobs for automatic connector syncs.
type Scheduler struct {
	cron    *cron.Cron
	cm      ConnectorGetter
	pipe    PipelineRunner
	store   ConfigLister
	log     *zap.Logger
	ctx     context.Context
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

// Start loads scheduled connectors from the database and starts the cron runner.
func (s *Scheduler) Start(ctx context.Context) error {
	s.ctx = ctx

	configs, err := s.store.ListConnectorConfigs(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: load configs: %w", err)
	}

	for _, cfg := range configs {
		if cfg.Enabled && cfg.Schedule != "" {
			s.addJob(cfg)
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
func (s *Scheduler) OnConnectorChanged(cfg *model.ConnectorConfig) {
	if cfg.Enabled && cfg.Schedule != "" {
		s.addJob(*cfg)
	} else {
		s.removeJob(cfg.ID)
	}
}

// OnConnectorRemoved is called when a connector is deleted.
func (s *Scheduler) OnConnectorRemoved(id uuid.UUID, _ string) {
	s.removeJob(id)
}

func (s *Scheduler) addJob(cfg model.ConnectorConfig) {
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
		s.runSync(connName, connID)
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

func (s *Scheduler) runSync(name string, id uuid.UUID) {
	conn, ok := s.cm.Get(name)
	if !ok {
		s.log.Warn("scheduled sync skipped: connector not found", zap.String("connector", name))
		return
	}

	s.log.Info("scheduled sync starting", zap.String("connector", name))
	report, err := s.pipe.Run(s.ctx, conn)
	if err != nil {
		s.log.Error("scheduled sync failed", zap.String("connector", name), zap.Error(err))
		return
	}

	now := time.Now()
	if err := s.store.UpdateLastRun(s.ctx, id, now); err != nil {
		s.log.Error("failed to update last_run", zap.String("connector", name), zap.Error(err))
	}

	s.log.Info("scheduled sync completed",
		zap.String("connector", name),
		zap.Int("docs", report.DocsProcessed),
		zap.Duration("duration", report.Duration),
	)
}
