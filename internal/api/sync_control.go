package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	_ "github.com/muty/nexus/internal/model" // referenced by swagger annotations (model.SyncRun)
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

// sseHeartbeatInterval keeps reverse-proxy connections alive during idle
// periods. Matches the 30–60s timeout most proxies ship with a comfortable
// margin. Heartbeat frames are comments-only so the client EventSource
// ignores them.
const sseHeartbeatInterval = 15 * time.Second

// CancelSyncJob godoc
//
//	@Summary	Cancel a running sync job
//	@Description	Signals the job's context to cancel. Fire-and-forget: returns 202 immediately. The client learns the terminal state via the SSE progress stream. Idempotent — canceling an already-canceled or completed job returns 202.
//	@Tags		sync
//	@Produce	json
//	@Param		id	path	string	true	"Job UUID"
//	@Success	202	{object}	APIResponse
//	@Failure	400	{object}	APIResponse
//	@Failure	404	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/sync/jobs/{id}/cancel [post]
func (h *handler) CancelSyncJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(jobID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	job := h.syncJobs.Get(jobID)
	if job == nil {
		writeError(w, http.StatusNotFound, "sync job not found")
		return
	}

	connID, err := uuid.Parse(job.ConnectorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid connector id on job")
		return
	}
	_, cfg, ok := h.cm.GetByID(connID)
	if !ok {
		writeError(w, http.StatusNotFound, errConnectorNotFound)
		return
	}
	if !canModifyConnector(auth.UserFromContext(r.Context()), cfg) {
		// Owners of a shared connector can read but no longer mutate; surface
		// 403 so the FE can explain. Strangers (no read access either) get
		// 404 for existence-leak parity with the read endpoints.
		writeMutationDenied(w, auth.UserFromContext(r.Context()), cfg)
		return
	}

	// Fire-and-forget. The goroutine exits on its next ctx.Done() check in
	// the pipeline loop; the client sees the terminal state on SSE.
	h.syncJobs.Cancel(jobID)
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "cancel requested"})
}

// ListSyncRunsForConnector godoc
//
//	@Summary	List sync run history for a connector
//	@Description	Returns the most recent sync runs (newest first) for a connector. Used by the Activity timeline tab. Limit is clamped to 1..200 with default 50.
//	@Tags		sync
//	@Produce	json
//	@Param		id		path	string	true	"Connector UUID"
//	@Param		limit	query	int		false	"Max rows to return (default 50, max 200)"
//	@Success	200	{array}	model.SyncRun
//	@Failure	400	{object}	APIResponse
//	@Failure	404	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/connectors/{id}/runs [get]
func (h *handler) ListSyncRunsForConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, errInvalidConnectorID)
		return
	}

	cfg, err := h.store.GetConnectorConfig(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, errConnectorNotFound)
			return
		}
		h.log.Error("list sync runs: get connector", zap.Error(err))
		writeError(w, http.StatusInternalServerError, errFailedGetConnector)
		return
	}
	if !canReadConnector(auth.UserFromContext(r.Context()), cfg) {
		writeError(w, http.StatusNotFound, errConnectorNotFound)
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	runs, err := h.store.ListSyncRunsByConnector(r.Context(), id, limit)
	if err != nil {
		h.log.Error("list sync runs failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to list sync runs")
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// StreamAllSyncProgress godoc
//
//	@Summary	Stream live sync progress across all visible connectors via SSE
//	@Description	Multiplexed Server-Sent Events stream: one connection pushes SyncJob updates for every job the caller can read. Replaces per-job /sync/{id}/progress. Auth accepts either the standard Authorization header or a sse_token query param (required for browser EventSource, which cannot set headers). Sends a heartbeat comment every 15s to keep reverse proxies from closing the connection.
//	@Tags		sync
//	@Produce	text/event-stream
//	@Success	200	{string}	string	"SSE stream"
//	@Failure	500	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/sync/progress [get]
func (h *handler) StreamAllSyncProgress(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	claims := auth.UserFromContext(r.Context())

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx proxy buffering

	// Send the current snapshot of all visible jobs immediately so a
	// client that connects mid-sync sees the in-flight state without
	// waiting for the next progress tick.
	for _, job := range h.syncJobs.Active() {
		if !h.canReadJob(claims, job) {
			continue
		}
		writeSSEFrame(w, *job)
	}
	flusher.Flush()

	ch, unsubscribe := h.syncJobs.SubscribeAll()
	defer unsubscribe()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case update, open := <-ch:
			if !open {
				return
			}
			if !h.canReadJob(claims, &update) {
				continue
			}
			writeSSEFrame(w, update)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// canReadJob checks whether the caller can see sync events for this job,
// by looking up the underlying connector and applying canReadConnector.
// Unknown/removed connectors fail closed.
func (h *handler) canReadJob(claims *auth.Claims, job *SyncJob) bool {
	connID, err := uuid.Parse(job.ConnectorID)
	if err != nil {
		return false
	}
	_, cfg, ok := h.cm.GetByID(connID)
	if !ok {
		return false
	}
	return canReadConnector(claims, cfg)
}

// writeSSEFrame emits a single SyncJob as a JSON-encoded data frame.
// Errors during marshal/write are ignored — the subsequent write or
// connection close will surface them. The "done" event is emitted only
// by the per-job Subscribe path; the multiplexed stream doesn't terminate
// per-job (clients derive "terminal" from the status field).
func writeSSEFrame(w http.ResponseWriter, job SyncJob) {
	data, err := json.Marshal(job)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}
