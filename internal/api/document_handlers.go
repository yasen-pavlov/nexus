package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
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

// relatedResponse is the JSON body returned by GET /documents/{id}/related.
// outgoing = edges declared on this doc; incoming = edges from other docs
// that point at this one.
type relatedResponse struct {
	Outgoing []relatedEdge `json:"outgoing"`
	Incoming []relatedEdge `json:"incoming"`
}

// relatedEdge pairs a relation with the resolved neighbor document. The
// neighbor is nil when the target isn't currently indexed (dangling edge
// — e.g. an IMAP reply_to pointing at a Message-ID we haven't synced yet,
// or a Telegram reply pointing at a message older than our sync window).
// Including dangling edges lets the UI render "reply to unknown sender"
// instead of hiding the signal.
type relatedEdge struct {
	Relation model.Relation  `json:"relation"`
	Document *model.Document `json:"document,omitempty"`
}

// GetRelatedDocuments godoc
//
//	@Summary		Resolve typed relations (attachments, replies, threads) for a document
//	@Description	Returns the neighbors of a document across all relation types. `outgoing` edges are the ones declared on the doc itself (attachment_of, reply_to, member_of_thread, member_of_window); `incoming` edges are the reverse lookup — other docs that point at this one. Dangling edges (target not in index) are returned with `document: null`.
//	@Tags			documents
//	@Produce		json
//	@Param			id	path	string	true	"Document UUID"
//	@Success		200	{object}	relatedResponse
//	@Failure		400	{object}	APIResponse
//	@Failure		401	{object}	APIResponse
//	@Failure		404	{object}	APIResponse
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/documents/{id}/related [get]
func (h *handler) GetRelatedDocuments(w http.ResponseWriter, r *http.Request) {
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

	claims := auth.UserFromContext(r.Context())
	if !canReadDocument(claims, chunk.OwnerID, chunk.Shared) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	out := relatedResponse{Outgoing: []relatedEdge{}, Incoming: []relatedEdge{}}

	// Outgoing: resolve each declared relation. Every edge appears in the
	// response even when the target isn't indexed — the UI needs to know
	// the edge exists so it can show a degraded "unknown neighbor" state.
	for _, rel := range chunk.Relations {
		neighbor := h.resolveRelationTarget(r.Context(), rel, chunk.SourceType, claims)
		out.Outgoing = append(out.Outgoing, relatedEdge{Relation: rel, Document: neighbor})
	}

	// Incoming: nested query for docs whose relations[].target_* matches any
	// identifier this chunk is addressable by. A single chunk can be a
	// target via its doc UUID, its source_id, or (for IMAP emails) its
	// IMAP Message-ID.
	targetSourceIDs := []string{chunk.SourceID}
	if chunk.IMAPMessageID != "" {
		targetSourceIDs = append(targetSourceIDs, chunk.IMAPMessageID)
	}
	incoming, err := h.search.FindChunksReferencing(r.Context(), []string{chunk.DocID}, targetSourceIDs)
	if err != nil {
		h.log.Error("find referencing chunks failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to find related documents")
		return
	}
	for _, ch := range incoming {
		if !canReadDocument(claims, ch.OwnerID, ch.Shared) {
			continue
		}
		// Which relation on the neighbor points at us? We could return all
		// the neighbor's relations, but the UI only cares about the edge
		// that makes it relevant here — find that one.
		rel := findInboundRelation(ch.Relations, chunk.DocID, targetSourceIDs)
		doc := chunkToDocument(&ch)
		out.Incoming = append(out.Incoming, relatedEdge{Relation: rel, Document: doc})
	}

	writeJSON(w, http.StatusOK, out)
}

// resolveRelationTarget expands a single outgoing Relation into the
// neighbor Document, respecting the caller's visibility. Returns nil
// when the target isn't indexed yet or isn't visible to this user (the
// edge is still echoed in the response; the UI shows a placeholder).
func (h *handler) resolveRelationTarget(ctx context.Context, rel model.Relation, sourceType string, claims *auth.Claims) *model.Document {
	// Prefer TargetID (UUID) when the connector resolved it at emit.
	if rel.TargetID != "" {
		ch, err := h.search.GetChunkByDocID(ctx, rel.TargetID)
		if err == nil && canReadDocument(claims, ch.OwnerID, ch.Shared) {
			return chunkToDocument(ch)
		}
	}
	if rel.TargetSourceID == "" {
		return nil
	}
	// IMAP reply_to and member_of_thread point at Message-IDs (not the
	// source_id we use internally). Try imap_message_id first for IMAP
	// documents; any hit resolves the edge.
	if sourceType == "imap" && (rel.Type == model.RelationReplyTo || rel.Type == model.RelationMemberOfThread) {
		hits, err := h.search.FindChunksByTerm(ctx, "imap_message_id", rel.TargetSourceID)
		if err == nil {
			for i := range hits {
				if canReadDocument(claims, hits[i].OwnerID, hits[i].Shared) {
					return chunkToDocument(&hits[i])
				}
			}
		}
	}
	// Fallback: same-connector source_id lookup (Telegram member_of_window,
	// reply_to, IMAP attachment_of via source_id).
	hits, err := h.search.FindChunksByTerm(ctx, "source_id", rel.TargetSourceID)
	if err == nil {
		for i := range hits {
			if hits[i].SourceType == sourceType && canReadDocument(claims, hits[i].OwnerID, hits[i].Shared) {
				return chunkToDocument(&hits[i])
			}
		}
	}
	return nil
}

// findInboundRelation picks, from a neighbor chunk's relations array, the
// one edge that makes it relevant as an incoming link to the target. When
// multiple relations point at the same target (unusual) we return the
// first match — callers treat the edge as "neighbor has some edge to us"
// regardless of which type in that case.
func findInboundRelation(rels []model.Relation, targetDocID string, targetSourceIDs []string) model.Relation {
	sidSet := map[string]struct{}{}
	for _, s := range targetSourceIDs {
		sidSet[s] = struct{}{}
	}
	for _, r := range rels {
		if r.TargetID != "" && r.TargetID == targetDocID {
			return r
		}
		if _, ok := sidSet[r.TargetSourceID]; ok {
			return r
		}
	}
	return model.Relation{}
}

// conversationMessagesResponse is the paginated payload returned by
// GET /conversations/{source_type}/{conversation_id}/messages. NextBefore
// is non-nil when earlier messages (scroll back) exist; NextAfter is
// non-nil when later messages (scroll forward) exist. Cursors are the
// edge timestamps of the returned page, to be passed back as
// ?before=/?after= on the next request.
type conversationMessagesResponse struct {
	Messages   []*model.Document `json:"messages"`
	NextBefore *time.Time        `json:"next_before,omitempty"`
	NextAfter  *time.Time        `json:"next_after,omitempty"`
}

// defaultConversationLimit / maxConversationLimit cap the page size.
// The max prevents a hostile client from dragging the whole chat
// history in one request.
const (
	defaultConversationLimit = 50
	maxConversationLimit     = 200
)

// GetConversationMessages godoc
//
//	@Summary		Paginated chronological browse of a conversation
//	@Description	Returns Hidden=true per-message documents for a (source_type, conversation_id) pair sorted by created_at ASC. Supports cursor pagination via `before` and `after` RFC3339 timestamps. Chat-like connectors (Telegram today, WhatsApp / Signal / Matrix in the future) plug in by emitting their per-message canonical docs with matching ConversationID.
//	@Tags			conversations
//	@Produce		json
//	@Param			source_type		path	string	true	"Connector source type (e.g. telegram)"
//	@Param			conversation_id	path	string	true	"Conversation identifier (e.g. Telegram chat ID)"
//	@Param			before			query	string	false	"RFC3339 timestamp — return messages strictly before this time"
//	@Param			after			query	string	false	"RFC3339 timestamp — return messages strictly after this time"
//	@Param			limit			query	int		false	"Page size (default 50, max 200)"
//	@Success		200	{object}	conversationMessagesResponse
//	@Failure		400	{object}	APIResponse
//	@Failure		401	{object}	APIResponse
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/conversations/{source_type}/{conversation_id}/messages [get]
func (h *handler) GetConversationMessages(w http.ResponseWriter, r *http.Request) {
	sourceType := chi.URLParam(r, "source_type")
	conversationID := chi.URLParam(r, "conversation_id")
	if sourceType == "" || conversationID == "" {
		writeError(w, http.StatusBadRequest, "source_type and conversation_id are required")
		return
	}

	opts := search.ConversationMessagesOptions{
		SourceType:   sourceType,
		Conversation: conversationID,
		Limit:        defaultConversationLimit,
	}
	q := r.URL.Query()
	if s := q.Get("before"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'before' timestamp (expected RFC3339)")
			return
		}
		opts.Before = t
	}
	if s := q.Get("after"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'after' timestamp (expected RFC3339)")
			return
		}
		opts.After = t
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid 'limit' (expected positive integer)")
			return
		}
		if n > maxConversationLimit {
			n = maxConversationLimit
		}
		opts.Limit = n
	}

	claims := auth.UserFromContext(r.Context())
	// Over-fetch so we can still fill a full page after per-chunk auth
	// filtering. Simpler than pushing the ownership filter into the
	// OpenSearch query — the auth boundary stays centralized in
	// canReadDocument. Worst case: doubling the fetch is cheap compared
	// to the ergonomic cost of duplicating auth logic into the query.
	opts.Limit *= 2

	chunks, err := h.search.GetConversationMessages(r.Context(), opts)
	if err != nil {
		h.log.Error("conversation messages query failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to load conversation")
		return
	}

	resp := conversationMessagesResponse{Messages: []*model.Document{}}
	requested := opts.Limit / 2
	for i := range chunks {
		if !canReadDocument(claims, chunks[i].OwnerID, chunks[i].Shared) {
			continue
		}
		resp.Messages = append(resp.Messages, chunkToDocument(&chunks[i]))
		if len(resp.Messages) >= requested {
			break
		}
	}

	// Cursors reflect the direction the caller moved. When the caller
	// paged forward (after=X) a full page means more newer messages
	// may exist → emit next_after. When the caller paged backward (or
	// did the initial tail load) a full page means more older messages
	// may exist → emit next_before. Emitting both would be misleading:
	// scrolling up from the initial load should not advertise a
	// "newer" cursor (the caller already has the latest).
	if len(resp.Messages) >= requested && len(resp.Messages) > 0 {
		if !opts.After.IsZero() {
			last := resp.Messages[len(resp.Messages)-1].CreatedAt
			resp.NextAfter = &last
		} else {
			first := resp.Messages[0].CreatedAt
			resp.NextBefore = &first
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// chunkToDocument projects a chunk into the Document shape returned by
// search responses — same fields `hitsToResult` copies, so the UI can
// render any neighbor with the same logic it uses for search hits.
func chunkToDocument(ch *model.Chunk) *model.Document {
	return &model.Document{
		ID:             model.DocumentID(ch.SourceType, ch.SourceName, ch.SourceID),
		SourceType:     ch.SourceType,
		SourceName:     ch.SourceName,
		SourceID:       ch.SourceID,
		Title:          ch.Title,
		Content:        ch.Content,
		MimeType:       ch.MimeType,
		Size:           ch.Size,
		Metadata:       ch.Metadata,
		Relations:      ch.Relations,
		ConversationID: ch.ConversationID,
		URL:            ch.URL,
		Visibility:     ch.Visibility,
		CreatedAt:      ch.CreatedAt,
		IndexedAt:      ch.IndexedAt,
	}
}
