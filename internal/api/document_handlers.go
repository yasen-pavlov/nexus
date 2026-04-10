package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/search"
	"go.uber.org/zap"
)

// DownloadDocument godoc
//
//	@Summary		Download or preview a document's binary content
//	@Description	Streams the original bytes of a document. Dispatches to the source connector via the BinaryFetcher capability. Returns 404 if the connector does not support previews or if the user lacks read permission. Use ?download=1 to force an attachment disposition instead of inline rendering.
//	@Tags			documents
//	@Produce		application/octet-stream
//	@Param			id			path	string	true	"Document UUID"
//	@Param			download	query	string	false	"Set to '1' to force attachment disposition"
//	@Success		200
//	@Failure		400	{object}	APIResponse
//	@Failure		401	{object}	APIResponse
//	@Failure		404	{object}	APIResponse
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/documents/{id}/content [get]
func (h *handler) DownloadDocument(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid document id")
		return
	}

	chunk, err := h.search.GetChunkByDocID(r.Context(), id.String())
	if err != nil {
		if errors.Is(err, search.ErrNotFound) {
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		h.log.Error("get chunk by doc id failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to look up document")
		return
	}

	// Auth check: 404 (not 403) on failure to avoid leaking existence.
	if !canReadDocument(auth.UserFromContext(r.Context()), chunk.OwnerID, chunk.Shared) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	conn, _, ok := h.cm.GetByTypeAndName(chunk.SourceType, chunk.SourceName)
	if !ok {
		// Connector is gone (deleted/disabled). Treat as not found.
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	fetcher, ok := conn.(connector.BinaryFetcher)
	if !ok {
		writeError(w, http.StatusNotFound, "preview not supported for this source")
		return
	}

	bc, err := fetcher.FetchBinary(r.Context(), chunk.SourceID)
	if err != nil {
		h.log.Error("fetch binary failed",
			zap.String("source_type", chunk.SourceType),
			zap.String("source_name", chunk.SourceName),
			zap.String("source_id", chunk.SourceID),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "failed to fetch document")
		return
	}
	defer func() { _ = bc.Reader.Close() }()

	mimeType := bc.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)

	if bc.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(bc.Size, 10))
	}

	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	filename := bc.Filename
	if filename == "" {
		filename = chunk.Title
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename=%q`, disposition, filename))

	if _, err := io.Copy(w, bc.Reader); err != nil {
		// Connection likely closed by client mid-stream — log at debug, not error.
		h.log.Debug("download stream interrupted", zap.Error(err))
	}
}
