package api

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// SyncJob represents the state of an in-progress or completed sync operation.
type SyncJob struct {
	ID            string    `json:"id"`
	ConnectorID   string    `json:"connector_id"`
	ConnectorName string    `json:"connector_name"`
	ConnectorType string    `json:"connector_type"`
	Status        string    `json:"status"` // "running", "completed", "failed"
	DocsTotal     int       `json:"docs_total"`
	DocsProcessed int       `json:"docs_processed"`
	DocsDeleted   int       `json:"docs_deleted"`
	Errors        int       `json:"errors"`
	Error         string    `json:"error,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	CompletedAt   time.Time `json:"completed_at,omitzero"`
}

// SyncJobManager tracks in-memory sync job state and notifies subscribers via channels.
type SyncJobManager struct {
	mu          sync.RWMutex
	jobs        map[string]*SyncJob       // keyed by job ID
	subscribers map[string][]chan SyncJob // keyed by job ID
}

// NewSyncJobManager creates a new SyncJobManager.
func NewSyncJobManager() *SyncJobManager {
	return &SyncJobManager{
		jobs:        make(map[string]*SyncJob),
		subscribers: make(map[string][]chan SyncJob),
	}
}

// Start creates a new running sync job and returns it.
func (m *SyncJobManager) Start(connectorID uuid.UUID, connectorName, connectorType string) *SyncJob {
	job := &SyncJob{
		ID:            uuid.New().String(),
		ConnectorID:   connectorID.String(),
		ConnectorName: connectorName,
		ConnectorType: connectorType,
		Status:        "running",
		StartedAt:     time.Now(),
	}

	m.mu.Lock()
	m.jobs[job.ID] = job
	m.mu.Unlock()

	return job
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
		if job.ConnectorID == idStr && job.Status == "running" {
			cp := *job
			return &cp
		}
	}
	return nil
}

// Active returns snapshots of all running and recently completed jobs.
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

// Update sets the total, processed, and error counts for a job and notifies subscribers.
func (m *SyncJobManager) Update(id string, total, processed, errors int) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if ok {
		job.DocsTotal = total
		job.DocsProcessed = processed
		job.Errors = errors
	}
	m.mu.Unlock()

	if ok {
		m.notify(id)
	}
}

// SetDeleted records the count of documents removed by deletion sync
// for this job, then notifies subscribers. Called by the sync handler
// once RunWithProgress returns its SyncReport — deletion happens at
// the end of a sync, after the regular progress callbacks, so it
// gets a separate update path rather than threading through the
// per-doc ProgressFunc.
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

// Complete marks a job as completed or failed.
func (m *SyncJobManager) Complete(id string, err error) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if ok {
		job.CompletedAt = time.Now()
		if err != nil {
			job.Status = "failed"
			job.Error = err.Error()
		} else {
			job.Status = "completed"
		}
	}
	m.mu.Unlock()

	if ok {
		m.notify(id)
		m.closeSubscribers(id)
	}
}

// Subscribe returns a channel that receives job snapshots on every state change.
// The channel is closed when the job completes.
func (m *SyncJobManager) Subscribe(id string) <-chan SyncJob {
	ch := make(chan SyncJob, 16)

	m.mu.Lock()
	defer m.mu.Unlock()

	// If job is already done, send final state and close immediately
	job, ok := m.jobs[id]
	if ok && job.Status != "running" {
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

// notify sends the current job state to all subscribers.
func (m *SyncJobManager) notify(id string) {
	m.mu.RLock()
	job, ok := m.jobs[id]
	if !ok {
		m.mu.RUnlock()
		return
	}
	snapshot := *job
	subs := m.subscribers[id]
	m.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- snapshot:
		default:
			// subscriber is slow, skip this update
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
