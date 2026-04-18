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
//	@Description	Returns Hidden=true per-message documents for a (source_type, conversation_id) pair sorted by created_at ASC. Supports cursor pagination via `before` / `after` RFC3339 timestamps, plus `around=RFC3339` for anchor-seeded opens (returns half the limit older + half newer, centered on the anchor). Chat-like connectors (Telegram today, WhatsApp / Signal / Matrix in the future) plug in by emitting their per-message canonical docs with matching ConversationID.
//	@Tags			conversations
//	@Produce		json
//	@Param			source_type		path	string	true	"Connector source type (e.g. telegram)"
//	@Param			conversation_id	path	string	true	"Conversation identifier (e.g. Telegram chat ID)"
//	@Param			before			query	string	false	"RFC3339 timestamp — return messages strictly before this time"
//	@Param			after			query	string	false	"RFC3339 timestamp — return messages strictly after this time"
//	@Param			around			query	string	false	"RFC3339 timestamp — return limit/2 messages before and limit/2 after, centered on this time. Cannot be combined with before/after."
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

	q := r.URL.Query()
	limit := defaultConversationLimit
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid 'limit' (expected positive integer)")
			return
		}
		if n > maxConversationLimit {
			n = maxConversationLimit
		}
		limit = n
	}

	var beforeTS, afterTS, aroundTS time.Time
	var err error
	if s := q.Get("before"); s != "" {
		beforeTS, err = time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'before' timestamp (expected RFC3339)")
			return
		}
	}
	if s := q.Get("after"); s != "" {
		afterTS, err = time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'after' timestamp (expected RFC3339)")
			return
		}
	}
	if s := q.Get("around"); s != "" {
		aroundTS, err = time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'around' timestamp (expected RFC3339)")
			return
		}
		if !beforeTS.IsZero() || !afterTS.IsZero() {
			writeError(w, http.StatusBadRequest, "'around' cannot be combined with 'before' or 'after'")
			return
		}
	}

	claims := auth.UserFromContext(r.Context())
	if !aroundTS.IsZero() {
		resp := h.buildAroundResponse(r.Context(), sourceType, conversationID, aroundTS, limit, claims)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp := h.buildDirectionalResponse(r.Context(), sourceType, conversationID, beforeTS, afterTS, limit, claims)
	writeJSON(w, http.StatusOK, resp)
}

// buildDirectionalResponse handles the single-direction tail / before /
// after modes. Over-fetches 2x to absorb per-chunk auth filtering.
func (h *handler) buildDirectionalResponse(ctx context.Context, sourceType, conversationID string, beforeTS, afterTS time.Time, limit int, claims *auth.Claims) conversationMessagesResponse {
	opts := search.ConversationMessagesOptions{
		SourceType:   sourceType,
		Conversation: conversationID,
		Before:       beforeTS,
		After:        afterTS,
		Limit:        limit * 2,
	}
	chunks, err := h.search.GetConversationMessages(ctx, opts)
	if err != nil {
		h.log.Error("conversation messages query failed", zap.Error(err))
		return conversationMessagesResponse{Messages: []*model.Document{}}
	}

	resp := conversationMessagesResponse{Messages: []*model.Document{}}
	for i := range chunks {
		if !canReadDocument(claims, chunks[i].OwnerID, chunks[i].Shared) {
			continue
		}
		resp.Messages = append(resp.Messages, chunkToDocument(&chunks[i]))
		if len(resp.Messages) >= limit {
			break
		}
	}

	// Single-direction cursor: scrolling forward (after=X) may have
	// more newer → emit next_after. Scrolling backward or tail-loading
	// may have more older → emit next_before. Never both here; around
	// handles the bidirectional case.
	if len(resp.Messages) >= limit && len(resp.Messages) > 0 {
		if !afterTS.IsZero() {
			last := resp.Messages[len(resp.Messages)-1].CreatedAt
			resp.NextAfter = &last
		} else {
			first := resp.Messages[0].CreatedAt
			resp.NextBefore = &first
		}
	}
	return resp
}

// buildAroundResponse handles anchor-seeded opens. Fetches ~limit/2
// messages on each side of the anchor in a single response, so the
// chat view can open centered on the anchor with proper bi-directional
// cursors from the first render.
//
// A message exactly at the anchor timestamp falls on the "before" side
// (the before half includes messages at-or-before anchor; after side
// is strictly later). Dedup is unnecessary because the halves don't
// overlap by construction.
func (h *handler) buildAroundResponse(ctx context.Context, sourceType, conversationID string, aroundTS time.Time, limit int, claims *auth.Claims) conversationMessagesResponse {
	half := max(limit/2, 1)

	// Before half: messages up to and including the anchor. Use a
	// 1-second inclusive bump since the search uses strict `<`.
	beforeOpts := search.ConversationMessagesOptions{
		SourceType:   sourceType,
		Conversation: conversationID,
		Before:       aroundTS.Add(time.Second),
		Limit:        half * 2,
	}
	beforeChunks, err := h.search.GetConversationMessages(ctx, beforeOpts)
	if err != nil {
		h.log.Error("conversation around (before) query failed", zap.Error(err))
		return conversationMessagesResponse{Messages: []*model.Document{}}
	}

	// After half: strictly newer than the anchor.
	afterOpts := search.ConversationMessagesOptions{
		SourceType:   sourceType,
		Conversation: conversationID,
		After:        aroundTS,
		Limit:        half * 2,
	}
	afterChunks, err := h.search.GetConversationMessages(ctx, afterOpts)
	if err != nil {
		h.log.Error("conversation around (after) query failed", zap.Error(err))
		return conversationMessagesResponse{Messages: []*model.Document{}}
	}

	resp := conversationMessagesResponse{Messages: []*model.Document{}}

	// beforeChunks is ASC ending at the anchor — take the tail (most
	// recent half entries that pass auth) so we get messages closest
	// to the anchor, not the oldest.
	beforeFiltered := filterReadable(beforeChunks, claims)
	if len(beforeFiltered) > half {
		beforeFiltered = beforeFiltered[len(beforeFiltered)-half:]
	}
	for _, ch := range beforeFiltered {
		resp.Messages = append(resp.Messages, chunkToDocument(&ch))
	}

	// afterChunks is ASC starting strictly after anchor — take the
	// head half entries (closest to anchor, same direction we'd
	// paginate on scroll-down).
	afterFiltered := filterReadable(afterChunks, claims)
	if len(afterFiltered) > half {
		afterFiltered = afterFiltered[:half]
	}
	for _, ch := range afterFiltered {
		resp.Messages = append(resp.Messages, chunkToDocument(&ch))
	}

	// Bidirectional cursors: emit next_before when the before half
	// was full (older messages might exist), next_after when the
	// after half was full (newer messages might exist). Edges are
	// the oldest/newest created_at in the merged response.
	if len(resp.Messages) == 0 {
		return resp
	}
	if len(beforeFiltered) >= half {
		first := resp.Messages[0].CreatedAt
		resp.NextBefore = &first
	}
	if len(afterFiltered) >= half {
		last := resp.Messages[len(resp.Messages)-1].CreatedAt
		resp.NextAfter = &last
	}
	return resp
}

// filterReadable copies only the chunks the given claims can read.
// Centralizes the auth gate so both halves of the around query apply
// it consistently.
func filterReadable(chunks []model.Chunk, claims *auth.Claims) []model.Chunk {
	out := make([]model.Chunk, 0, len(chunks))
	for i := range chunks {
		if canReadDocument(claims, chunks[i].OwnerID, chunks[i].Shared) {
			out = append(out, chunks[i])
		}
	}
	return out
}

// GetDocumentBySource godoc
//
//	@Summary		Look up a document by (source_type, source_id)
//	@Description	Resolves a chunk identified by its canonical per-connector identifiers into a Document. Used by the conversation view to lazy-fetch reply targets whose message falls outside the loaded window — when walking a reply_to relation the frontend only has the target's source identifiers and needs to fetch the full doc. Auth-scoped: returns 404 (not 403) when the doc isn't readable, to avoid leaking existence.
//	@Tags			documents
//	@Produce		json
//	@Param			source_type	query	string	true	"Source type (e.g. telegram, imap)"
//	@Param			source_id	query	string	true	"Source-assigned identifier of the chunk"
//	@Success		200	{object}	model.Document
//	@Failure		400	{object}	APIResponse
//	@Failure		401	{object}	APIResponse
//	@Failure		404	{object}	APIResponse
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/documents/by-source [get]
func (h *handler) GetDocumentBySource(w http.ResponseWriter, r *http.Request) {
	sourceType := r.URL.Query().Get("source_type")
	sourceID := r.URL.Query().Get("source_id")
	if sourceType == "" || sourceID == "" {
		writeError(w, http.StatusBadRequest, "source_type and source_id are required")
		return
	}

	hits, err := h.search.FindChunksByTerm(r.Context(), "source_id", sourceID)
	if err != nil {
		h.log.Error("find chunk by source_id failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to look up document")
		return
	}

	claims := auth.UserFromContext(r.Context())
	for i := range hits {
		if hits[i].SourceType != sourceType {
			continue
		}
		if !canReadDocument(claims, hits[i].OwnerID, hits[i].Shared) {
			continue
		}
		writeJSON(w, http.StatusOK, chunkToDocument(&hits[i]))
		return
	}

	writeError(w, http.StatusNotFound, "document not found")
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
