package api

import (
	"context"
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

// CreateChat creates a new empty chat owned by the requesting user.
func (h *handler) CreateChat(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == uuid.Nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	var req createChatRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
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

// ListChats returns the calling user's chats, ordered by updated_at desc.
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

// GetChat returns the chat plus its full message history. Owner-only;
// non-owners (including admins) get 404 to avoid existence leaks.
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

// UpdateChat applies a partial update (title and/or default_model). Owner-only.
func (h *handler) UpdateChat(w http.ResponseWriter, r *http.Request) {
	chat, ok := h.loadOwnedChat(w, r)
	if !ok {
		return
	}

	var req updateChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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

// DeleteChat removes the chat and (via ON DELETE CASCADE) all its messages.
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

// PostChatMessage submits a user turn and streams the orchestrator's
// events back as SSE frames. Owner-only.
func (h *handler) PostChatMessage(w http.ResponseWriter, r *http.Request) {
	chat, ok := h.loadOwnedChat(w, r)
	if !ok {
		return
	}

	var req postMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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

// writeRagEvent serializes one rag.Event as an SSE frame.
func writeRagEvent(w http.ResponseWriter, ev rag.Event) {
	switch ev.Kind {
	case rag.EvRetrieving:
		writeNamedSSEFrame(w, "retrieving", map[string]string{"query": ev.Query})
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
	case rag.EvDone:
		writeNamedSSEFrame(w, "done", map[string]any{
			"stop_reason": ev.StopReason,
			"message_id":  ev.MessageID,
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

// guard against unused imports if a future refactor drops references
var _ = context.Background
