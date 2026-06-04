package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/muty/nexus/internal/auth"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/rag"
	"github.com/muty/nexus/internal/store"
	"go.uber.org/zap"
)

const (
	maxChatListLimit = 200
	chatNotFoundMsg  = "chat not found"
)

// createChatRequest is the body for POST /api/chats. Both fields optional.
type createChatRequest struct {
	Title        string `json:"title"`
	DefaultModel string `json:"default_model"`
}

// updateChatRequest is the body for PATCH /api/chats/:id. Pointers so
// "absent" and "set to empty string" are distinguishable.
type updateChatRequest struct {
	Title        *string `json:"title"`
	DefaultModel *string `json:"default_model"`
}

// messageFeedbackRequest is the body for the message-feedback endpoint.
// Feedback is "up", "down", or null (to clear the rating).
type messageFeedbackRequest struct {
	Feedback *string `json:"feedback"`
}

// listChatsResponse is the body of GET /api/chats.
type listChatsResponse struct {
	Chats []model.ChatListEntry `json:"chats"`
	Total int                   `json:"total"`
}

// chatDetailResponse is the body of GET /api/chats/:id.
type chatDetailResponse struct {
	Chat     model.Chat          `json:"chat"`
	Messages []model.ChatMessage `json:"messages"`
}

// postMessageRequest is the body of POST /api/chats/:id/messages.
type postMessageRequest struct {
	Content string `json:"content"`
	Model   string `json:"model"`
}

// CreateChat godoc
//
//	@Summary	Create a chat
//	@Description	Creates a new empty chat owned by the requesting user. Both title and default_model are optional.
//	@Tags		chats
//	@Accept		json
//	@Produce	json
//	@Param		request	body	createChatRequest	false	"Initial chat fields"
//	@Success	201	{object}	model.Chat
//	@Failure	400	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/chats [post]
func (h *handler) CreateChat(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	var req createChatRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, errInvalidRequestBody)
			return
		}
	}

	chat := &model.Chat{
		UserID:       userID,
		Title:        req.Title,
		DefaultModel: req.DefaultModel,
	}
	if err := h.store.CreateChat(r.Context(), chat); err != nil {
		h.log.Error("create chat", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create chat")
		return
	}
	writeJSON(w, http.StatusCreated, chat)
}

// ListChats godoc
//
//	@Summary	List chats
//	@Description	Returns the calling user's chats, ordered by updated_at desc.
//	@Tags		chats
//	@Produce	json
//	@Param		limit	query	int	false	"Max chats to return (default 50, max 200)"
//	@Param		offset	query	int	false	"Number of chats to skip"
//	@Success	200	{object}	listChatsResponse
//	@Security	BearerAuth
//	@Router		/chats [get]
func (h *handler) ListChats(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	if limit > maxChatListLimit {
		limit = maxChatListLimit
	}
	if offset < 0 {
		offset = 0
	}

	chats, total, err := h.store.ListChats(r.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("list chats", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to list chats")
		return
	}
	writeJSON(w, http.StatusOK, listChatsResponse{Chats: chats, Total: total})
}

// GetChat godoc
//
//	@Summary	Get a chat
//	@Description	Returns the chat plus its full message history. Owner-only; non-owners (including admins) get 404 to avoid leaking chat existence.
//	@Tags		chats
//	@Produce	json
//	@Param		id	path	string	true	"Chat ID"
//	@Success	200	{object}	chatDetailResponse
//	@Failure	400	{object}	APIResponse
//	@Failure	404	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/chats/{id} [get]
func (h *handler) GetChat(w http.ResponseWriter, r *http.Request) {
	chat, ok := h.loadOwnedChat(w, r)
	if !ok {
		return
	}
	msgs, err := h.store.ListMessages(r.Context(), chat.ID)
	if err != nil {
		h.log.Error("list messages", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to list messages")
		return
	}
	writeJSON(w, http.StatusOK, chatDetailResponse{Chat: *chat, Messages: msgs})
}

// UpdateChat godoc
//
//	@Summary	Update a chat
//	@Description	Applies a partial update (title and/or default_model). Owner-only; non-owners get 404 to avoid leaking chat existence.
//	@Tags		chats
//	@Accept		json
//	@Produce	json
//	@Param		id		path	string	true	"Chat ID"
//	@Param		request	body	updateChatRequest	true	"Fields to update"
//	@Success	200	{object}	model.Chat
//	@Failure	400	{object}	APIResponse
//	@Failure	404	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/chats/{id} [patch]
func (h *handler) UpdateChat(w http.ResponseWriter, r *http.Request) {
	chat, ok := h.loadOwnedChat(w, r)
	if !ok {
		return
	}

	var req updateChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}

	fields := store.ChatUpdate{Title: req.Title, DefaultModel: req.DefaultModel}
	if err := h.store.UpdateChat(r.Context(), chat.ID, fields); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, chatNotFoundMsg)
			return
		}
		h.log.Error("update chat", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to update chat")
		return
	}
	updated, err := h.store.GetChat(r.Context(), chat.ID)
	if err != nil {
		h.log.Error("reload chat after update", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to load updated chat")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// SetMessageFeedback godoc
//
//	@Summary		Rate an assistant message
//	@Description	Records a thumbs rating ("up"/"down", or null to clear) on a message. Owner-only; non-owners get 404 to avoid leaking chat existence.
//	@Tags			chats
//	@Accept			json
//	@Produce		json
//	@Param			id			path	string	true	"Chat ID"
//	@Param			messageId	path	string	true	"Message ID"
//	@Param			request		body	messageFeedbackRequest	true	"Feedback"
//	@Success		204
//	@Failure		400	{object}	APIResponse
//	@Failure		404	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/chats/{id}/messages/{messageId}/feedback [put]
func (h *handler) SetMessageFeedback(w http.ResponseWriter, r *http.Request) {
	chat, ok := h.loadOwnedChat(w, r)
	if !ok {
		return
	}
	messageID, err := uuid.Parse(chi.URLParam(r, "messageId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid message id")
		return
	}
	var req messageFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	if req.Feedback != nil && *req.Feedback != "up" && *req.Feedback != "down" {
		writeError(w, http.StatusBadRequest, "feedback must be 'up', 'down', or null")
		return
	}
	if err := h.store.SetMessageFeedback(r.Context(), chat.ID, messageID, req.Feedback); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "message not found")
			return
		}
		h.log.Error("set message feedback", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to set feedback")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteChat godoc
//
//	@Summary	Delete a chat
//	@Description	Removes the chat and (via ON DELETE CASCADE) all its messages. Owner-only; non-owners get 404 to avoid leaking chat existence.
//	@Tags		chats
//	@Produce	json
//	@Param		id	path	string	true	"Chat ID"
//	@Success	204
//	@Failure	400	{object}	APIResponse
//	@Failure	404	{object}	APIResponse
//	@Security	BearerAuth
//	@Router		/chats/{id} [delete]
func (h *handler) DeleteChat(w http.ResponseWriter, r *http.Request) {
	chat, ok := h.loadOwnedChat(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteChat(r.Context(), chat.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, chatNotFoundMsg)
			return
		}
		h.log.Error("delete chat", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to delete chat")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PostChatMessage godoc
//
//	@Summary	Send a chat message (SSE stream)
//	@Description	Submits a user turn and streams the RAG orchestrator's events back as Server-Sent Events (retrieving, evidence, text, citation, tool_start/result, usage, title, done, error). Owner-only; non-owners get 404 to avoid leaking chat existence. The response is a long-lived text/event-stream, not a single JSON body.
//	@Tags		chats
//	@Accept		json
//	@Produce	text/event-stream
//	@Param		id		path	string	true	"Chat ID"
//	@Param		request	body	postMessageRequest	true	"User message"
//	@Success	200	{string}	string	"SSE event stream"
//	@Failure	400	{object}	APIResponse
//	@Failure	404	{object}	APIResponse
//	@Failure	503	{object}	APIResponse	"RAG orchestrator not configured"
//	@Security	BearerAuth
//	@Router		/chats/{id}/messages [post]
func (h *handler) PostChatMessage(w http.ResponseWriter, r *http.Request) {
	chat, ok := h.loadOwnedChat(w, r)
	if !ok {
		return
	}

	var req postMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if h.rag == nil {
		writeError(w, http.StatusServiceUnavailable, "rag orchestrator not configured")
		return
	}

	events, err := h.rag.Run(r.Context(), rag.RunInput{
		ChatID:  chat.ID,
		UserID:  chat.UserID,
		Content: req.Content,
		Model:   req.Model,
	})
	if err != nil {
		if errors.Is(err, rag.ErrChatNotFound) {
			writeError(w, http.StatusNotFound, chatNotFoundMsg)
			return
		}
		h.log.Warn("rag start failed", zap.Error(err))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	for ev := range events {
		writeRagEvent(w, ev)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// loadOwnedChat fetches the chat from the URL path param and verifies
// the caller owns it. Writes the appropriate response and returns
// (nil, false) on any failure. Non-owners (including admins) get 404
// to avoid existence leaks.
func (h *handler) loadOwnedChat(w http.ResponseWriter, r *http.Request) (*model.Chat, bool) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chat id")
		return nil, false
	}
	chat, err := h.store.GetChat(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, chatNotFoundMsg)
			return nil, false
		}
		h.log.Error("get chat", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to load chat")
		return nil, false
	}
	if chat.UserID != userID {
		// Admins are NOT exempt — return 404, not 403, so we don't
		// leak chat existence to non-owners.
		writeError(w, http.StatusNotFound, chatNotFoundMsg)
		return nil, false
	}
	return chat, true
}

// writeRagEvent serializes one rag.Event as an SSE frame. Phase 4 added
// the skipped_retrieval and title kinds; the FE state machine treats
// the first as a peer of retrieving (mutually exclusive) and the second
// as an out-of-band metadata frame that piggybacks on the same stream.
func writeRagEvent(w http.ResponseWriter, ev rag.Event) {
	switch ev.Kind {
	case rag.EvRetrieving:
		writeNamedSSEFrame(w, "retrieving", map[string]string{"query": ev.Query})
	case rag.EvSkippedRetrieval:
		writeNamedSSEFrame(w, "skipped_retrieval", map[string]string{"query": ev.Query})
	case rag.EvEvidence:
		writeNamedSSEFrame(w, "evidence", map[string]any{"chunks": ev.Evidence})
	case rag.EvText:
		writeNamedSSEFrame(w, "text", map[string]string{"delta": ev.TextDelta})
	case rag.EvCitation:
		if ev.Citation != nil {
			writeNamedSSEFrame(w, "citation", map[string]any{
				"doc_id":     ev.Citation.DocID,
				"cited_text": ev.Citation.CitedText,
				"span":       []int{ev.Citation.SpanStart, ev.Citation.SpanEnd},
			})
		}
	case rag.EvUsage:
		if ev.Usage != nil {
			writeNamedSSEFrame(w, "usage", ev.Usage)
		}
	case rag.EvTitle:
		writeNamedSSEFrame(w, "title", map[string]string{"title": ev.Title})
	case rag.EvRewriterStatus:
		writeNamedSSEFrame(w, "rewriter_status", map[string]string{"reason": ev.StatusReason})
	case rag.EvTitleStatus:
		writeNamedSSEFrame(w, "title_status", map[string]string{"reason": ev.StatusReason})
	case rag.EvToolStart:
		writeNamedSSEFrame(w, "tool_start", map[string]string{
			"name": ev.ToolName,
			"args": ev.ToolArgs,
		})
	case rag.EvToolResult:
		writeNamedSSEFrame(w, "tool_result", map[string]any{
			"name":    ev.ToolName,
			"summary": ev.ToolSummary,
			"chunks":  ev.ToolChunks,
		})
	case rag.EvDone:
		writeNamedSSEFrame(w, "done", map[string]any{
			"stop_reason": ev.StopReason,
			"message_id":  ev.MessageID,
			"duration_ms": ev.DurationMs,
		})
	case rag.EvError:
		writeNamedSSEFrame(w, "error", map[string]string{"message": ev.Err})
	}
}

// writeNamedSSEFrame emits one `event: <name>\ndata: <json>\n\n` frame.
// (The unnamed-event variant in sync_control.go writes a `data:`-only
// frame, so we keep both.)
func writeNamedSSEFrame(w http.ResponseWriter, event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte("{}")
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

// Compile-time assertion that *store.Store satisfies the rag.ChatStore
// interface.
var _ rag.ChatStore = (*store.Store)(nil)
