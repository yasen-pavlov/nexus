package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// Sync job status values.
//
// The in-memory SyncJob mirrors the sync_runs.status column. Terminal
// states are persisted to the sync_runs row by Complete; live state is
// read from the in-memory map via Get / GetByConnector.
//
// "interrupted" is a boot-time backfill written by
// store.MarkInterruptedStuckRuns when the process restarts mid-sync. The
// manager itself never produces it, but the FE SyncStatus union needs
// to know it so the Activity timeline renders it distinct from "failed".
const (
	SyncStatusRunning     = "running"
	SyncStatusCompleted   = "completed"
	SyncStatusFailed      = "failed"
	SyncStatusCanceled    = "canceled"
	SyncStatusInterrupted = "interrupted"
)

// ErrAlreadyRunning is returned by Start when the connector already has a
// running job. Handlers map this to HTTP 409; the scheduler treats it as a
// silent skip.
var ErrAlreadyRunning = errors.New("sync already running for connector")

// completedJobRetention bounds how long a terminal (completed/failed/canceled)
// job lingers in the in-memory map after Complete. The terminal frame is
// broadcast synchronously in Complete, so live subscribers already have it;
// the lingering copy only serves a late-joining client's poll/hydrate. Without
// this, terminal jobs are never evicted and accumulate in m.jobs for the life
// of the process — an unbounded leak that also makes Active() grow over uptime.
const completedJobRetention = 10 * time.Minute

// SyncJob represents the state of an in-progress or completed sync operation.
// The same UUID is used for the sync_runs.id column so live progress and
// persisted history correlate.
type SyncJob struct {
	ID            string `json:"id"`
	ConnectorID   string `json:"connector_id"`
	ConnectorName string `json:"connector_name"`
	ConnectorType string `json:"connector_type"`
	Status        string `json:"status"` // running | completed | failed | canceled
	DocsTotal     int    `json:"docs_total"`
	DocsProcessed int    `json:"docs_processed"`
	DocsDeleted   int    `json:"docs_deleted"`
	Errors        int    `json:"errors"`
	Error         string `json:"error,omitempty"`
	// Scope is a free-form label from the connector naming its
	// current sub-unit of work — the IMAP folder, the Telegram
	// chat, the Paperless page. Surfaces in the UI alongside the
	// progress bar so "Syncing Archive…" beats a bare "0/327".
	// Empty when the connector hasn't declared a scope yet or
	// the sync has moved past the scoped portion.
	Scope       string    `json:"scope,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitzero"`
}

// runPersister is the subset of *store.Store this manager uses. Declared as
// a local interface so unit tests can pass a nil store without dragging in
// the DB — when nil, persistence is a no-op and the manager behaves like a
// pure in-memory broadcaster. Integration tests and production wire a real
// *store.Store.
type runPersister interface {
	InsertSyncRun(ctx context.Context, run *model.SyncRun) error
	UpdateSyncRunComplete(
		ctx context.Context,
		id uuid.UUID,
		status string,
		docsTotal, docsProcessed, docsDeleted, errCount int,
		errorMessage string,
		completedAt time.Time,
	) error
}

// SyncJobManager tracks live sync job state, broadcasts progress to SSE
// subscribers, owns the cancel-func registry for running jobs, and
// persists start/complete events to the sync_runs table.
type SyncJobManager struct {
	mu                sync.RWMutex
	jobs              map[string]*SyncJob           // job ID → snapshot
	subscribers       map[string][]chan SyncJob     // job ID → SSE listener channels
	globalSubscribers map[int]chan SyncJob          // broadcast subscribers keyed by token
	nextSubToken      int                           // monotonic id for global subs
	cancelFuncs       map[string]context.CancelFunc // job ID → cancel the run ctx
	persister         runPersister
	log               *zap.Logger
	// completedTTL is how long terminal jobs survive in m.jobs before
	// eviction. Defaults to completedJobRetention; tests may shorten it.
	completedTTL time.Duration
}

// NewSyncJobManager creates a new SyncJobManager. Pass a *store.Store for
// production or nil in unit tests that don't need sync_runs persistence.
func NewSyncJobManager(st *store.Store, log *zap.Logger) *SyncJobManager {
	if log == nil {
		log = zap.NewNop()
	}
	var p runPersister
	if st != nil {
		p = st
	}
	return &SyncJobManager{
		jobs:              make(map[string]*SyncJob),
		subscribers:       make(map[string][]chan SyncJob),
		globalSubscribers: make(map[int]chan SyncJob),
		cancelFuncs:       make(map[string]context.CancelFunc),
		persister:         p,
		log:               log,
		completedTTL:      completedJobRetention,
	}
}

// Start registers a new running sync job for the given connector. If a
// sync is already running for the connector, returns (nil, nil, ErrAlreadyRunning).
//
// The returned context is cancellable via Cancel(jobID); handlers /
// schedulers pass it into pipeline.RunWithProgress so a mid-flight sync
// can be aborted.
//
// Persists a row to sync_runs with status=running before returning, so the
// run survives process restart. If the insert fails, the job is not
// registered and the error is propagated.
func (m *SyncJobManager) Start(connectorID uuid.UUID, connectorName, connectorType string) (*SyncJob, context.Context, error) {
	// Pre-check + allocation under lock so two concurrent Starts for the
	// same connector can't both succeed. The cancel map is populated in
	// the same critical section as the jobs map to close the window where
	// Cancel could arrive before the func is stored.
	m.mu.Lock()

	// Is there already a running job for this connector?
	connIDStr := connectorID.String()
	for _, j := range m.jobs {
		if j.ConnectorID == connIDStr && j.Status == SyncStatusRunning {
			m.mu.Unlock()
			return nil, nil, ErrAlreadyRunning
		}
	}

	// Detach from any request context — syncs outlive the HTTP handler
	// that starts them. Cancellation is driven explicitly via Cancel(id).
	runCtx, cancel := context.WithCancel(context.Background())

	jobID := uuid.New()
	now := time.Now()
	job := &SyncJob{
		ID:            jobID.String(),
		ConnectorID:   connIDStr,
		ConnectorName: connectorName,
		ConnectorType: connectorType,
		Status:        SyncStatusRunning,
		StartedAt:     now,
	}
	m.jobs[job.ID] = job
	m.cancelFuncs[job.ID] = cancel
	m.mu.Unlock()

	if m.persister != nil {
		run := &model.SyncRun{
			ID:          jobID,
			ConnectorID: connectorID,
			Status:      SyncStatusRunning,
			StartedAt:   now,
		}
		if err := m.persister.InsertSyncRun(context.Background(), run); err != nil {
			// Roll back the in-memory registration so the world stays
			// consistent with the DB.
			m.mu.Lock()
			delete(m.jobs, job.ID)
			delete(m.cancelFuncs, job.ID)
			m.mu.Unlock()
			cancel()
			return nil, nil, err
		}
	}

	return job, runCtx, nil
}

// StartForSchedule is a thin adapter over Start that returns just the
// generated job ID + run context. Lets consumers outside this package
// (notably the cron scheduler) route scheduled runs through the same
// lifecycle as manual triggers without having to import the SyncJob type.
func (m *SyncJobManager) StartForSchedule(connectorID uuid.UUID, name, connectorType string) (string, context.Context, error) {
	job, ctx, err := m.Start(connectorID, name, connectorType)
	if err != nil {
		return "", nil, err
	}
	return job.ID, ctx, nil
}

// Cancel signals the running job to stop. Fire-and-forget: returns
// immediately without waiting for the goroutine to exit. The goroutine's
// next ctx.Done() check in the pipeline (or the next network round-trip,
// whichever comes first) will unwind. Idempotent — calling twice is safe.
// Returns false if the job doesn't exist or has already completed.
func (m *SyncJobManager) Cancel(id string) bool {
	m.mu.RLock()
	cancel, ok := m.cancelFuncs[id]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// Get returns a snapshot of a job by ID, or nil if not found.
func (m *SyncJobManager) Get(id string) *SyncJob {
	m.mu.RLock()
	defer m.mu.RUnlock()

	job, ok := m.jobs[id]
	if !ok {
		return nil
	}
	cp := *job
	return &cp
}

// GetByConnector returns the most recent active (running) job for a connector, or nil.
func (m *SyncJobManager) GetByConnector(connectorID uuid.UUID) *SyncJob {
	m.mu.RLock()
	defer m.mu.RUnlock()

	idStr := connectorID.String()
	for _, job := range m.jobs {
		if job.ConnectorID == idStr && job.Status == SyncStatusRunning {
			cp := *job
			return &cp
		}
	}
	return nil
}

// Active returns snapshots of all running and recently completed jobs.
// Terminal jobs are evicted completedTTL after Complete (see evict), so
// "recently completed" is bounded — the map never grows without limit.
func (m *SyncJobManager) Active() []*SyncJob {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*SyncJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		cp := *job
		result = append(result, &cp)
	}
	return result
}

// Update sets the total, processed, error counts, and scope label
// for a job and notifies subscribers.
func (m *SyncJobManager) Update(id string, total, processed, errors int, scope string) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if ok {
		job.DocsTotal = total
		job.DocsProcessed = processed
		job.Errors = errors
		job.Scope = scope
	}
	m.mu.Unlock()

	if ok {
		m.notify(id)
	}
}

// SetDeleted records the count of documents removed by deletion sync
// for this job, then notifies subscribers.
func (m *SyncJobManager) SetDeleted(id string, deleted int) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if ok {
		job.DocsDeleted = deleted
	}
	m.mu.Unlock()
	if ok {
		m.notify(id)
	}
}

// Complete marks a job as completed, failed, or canceled (depending on the
// error) and updates the corresponding sync_runs row with final counts.
// A nil err means "completed"; a context.Canceled error (anywhere in the
// chain) means "canceled" with no user-visible error message; any other
// non-nil err means "failed" with the error string.
func (m *SyncJobManager) Complete(id string, err error) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	job.CompletedAt = time.Now()
	switch {
	case err == nil:
		job.Status = SyncStatusCompleted
		job.Error = ""
	case errors.Is(err, context.Canceled):
		job.Status = SyncStatusCanceled
		job.Error = ""
	default:
		job.Status = SyncStatusFailed
		job.Error = err.Error()
	}
	// Snapshot final values before releasing the lock so the subsequent
	// DB update doesn't need to hold it.
	snapshot := *job
	delete(m.cancelFuncs, id)
	m.mu.Unlock()

	m.notify(id)
	m.closeSubscribers(id)

	// Evict the terminal job after a grace period so it doesn't linger in
	// m.jobs (and thus Active() / the SSE snapshot) forever. The terminal
	// frame was just broadcast, so connected clients already have it.
	if m.completedTTL > 0 {
		time.AfterFunc(m.completedTTL, func() { m.evict(id) })
	}

	if m.persister != nil {
		runID, parseErr := uuid.Parse(snapshot.ID)
		if parseErr != nil {
			m.log.Warn("sync job: parse job id for persistence",
				zap.String("id", snapshot.ID), zap.Error(parseErr))
			return
		}
		if err := m.persister.UpdateSyncRunComplete(
			context.Background(),
			runID,
			snapshot.Status,
			snapshot.DocsTotal,
			snapshot.DocsProcessed,
			snapshot.DocsDeleted,
			snapshot.Errors,
			snapshot.Error,
			snapshot.CompletedAt,
		); err != nil {
			m.log.Warn("sync job: persist complete",
				zap.String("id", snapshot.ID), zap.Error(err))
		}
	}
}

// evict removes a terminal job from the in-memory map. Scheduled by Complete
// on a completedTTL timer. A no-op if the job is already gone. Safe to call
// for a job that never existed (delete on a missing key is a no-op).
func (m *SyncJobManager) evict(id string) {
	m.mu.Lock()
	delete(m.jobs, id)
	m.mu.Unlock()
}

// SubscribeAll returns a channel that receives snapshots for every job state
// change across all connectors. Used by the multiplexed SSE endpoint so the
// browser opens only one EventSource regardless of how many jobs are running.
// The returned unsubscribe function MUST be called when the listener goes
// away (e.g. client disconnects) to avoid leaking the channel.
//
// Buffer is 64 — a slow subscriber skips updates rather than blocking the
// notifier, matching the per-job Subscribe behavior.
func (m *SyncJobManager) SubscribeAll() (<-chan SyncJob, func()) {
	ch := make(chan SyncJob, 64)

	m.mu.Lock()
	token := m.nextSubToken
	m.nextSubToken++
	m.globalSubscribers[token] = ch
	m.mu.Unlock()

	unsubscribe := func() {
		m.mu.Lock()
		if existing, ok := m.globalSubscribers[token]; ok {
			delete(m.globalSubscribers, token)
			close(existing)
		}
		m.mu.Unlock()
	}
	return ch, unsubscribe
}

// Subscribe returns a channel that receives job snapshots on every state change.
// The channel is closed when the job completes.
func (m *SyncJobManager) Subscribe(id string) <-chan SyncJob {
	ch := make(chan SyncJob, 16)

	m.mu.Lock()
	defer m.mu.Unlock()

	// If job is already done, send final state and close immediately
	job, ok := m.jobs[id]
	if ok && job.Status != SyncStatusRunning {
		ch <- *job
		close(ch)
		return ch
	}

	// Send current state
	if ok {
		ch <- *job
	}

	m.subscribers[id] = append(m.subscribers[id], ch)
	return ch
}

// notify sends the current job state to all per-job and global subscribers.
//
// The sends run while the RLock is still held. This is deliberate: a
// subscriber channel is only ever closed under the write Lock (by
// unsubscribe / closeSubscribers), so holding the read lock across the sends
// guarantees no channel can be closed mid-send — a send on a closed channel
// panics even inside a select/default, which would crash the whole server on
// the routine event of an SSE client disconnecting during a sync. The sends
// are non-blocking (select/default), so keeping the read lock over them adds
// no meaningful contention.
func (m *SyncJobManager) notify(id string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	job, ok := m.jobs[id]
	if !ok {
		return
	}
	snapshot := *job

	for _, ch := range m.subscribers[id] {
		select {
		case ch <- snapshot:
		default:
			// subscriber is slow, skip this update
		}
	}
	for _, ch := range m.globalSubscribers {
		select {
		case ch <- snapshot:
		default:
			// slow global subscriber — best-effort
		}
	}
}

// closeSubscribers closes and removes all subscriber channels for a job.
func (m *SyncJobManager) closeSubscribers(id string) {
	m.mu.Lock()
	subs := m.subscribers[id]
	delete(m.subscribers, id)
	m.mu.Unlock()

	for _, ch := range subs {
		close(ch)
	}
}
