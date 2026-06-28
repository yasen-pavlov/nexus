package cliclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/muty/nexus/internal/model"
)

type createChatRequest struct {
	Title        string `json:"title,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
}

// CreateChat creates a new empty chat and returns it. `ask` uses this to make an
// ephemeral conversation it tears down afterwards.
func (c *Client) CreateChat(ctx context.Context) (*model.Chat, error) {
	var chat model.Chat
	if err := c.do(ctx, http.MethodPost, "/api/chats", nil, createChatRequest{}, &chat); err != nil {
		return nil, err
	}
	return &chat, nil
}

// ChatListEntry is one row from GET /api/chats: a Chat plus a preview of its
// first user message (empty until the chat has a first turn).
type ChatListEntry struct {
	model.Chat
	FirstMessagePreview string `json:"first_message_preview,omitempty"`
}

type listChatsResponse struct {
	Chats []ChatListEntry `json:"chats"`
	Total int             `json:"total"`
}

// ListChats returns the caller's chats, most-recently-updated first, plus the
// total count.
func (c *Client) ListChats(ctx context.Context, limit, offset int) ([]ChatListEntry, int, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	var res listChatsResponse
	if err := c.do(ctx, http.MethodGet, "/api/chats", q, nil, &res); err != nil {
		return nil, 0, err
	}
	return res.Chats, res.Total, nil
}

// ChatDetail is a chat together with its message history.
type ChatDetail struct {
	Chat     model.Chat          `json:"chat"`
	Messages []model.ChatMessage `json:"messages"`
}

// GetChat returns a chat and its messages. A 404 means the chat is missing or
// not owned by the caller (the server does not distinguish, to avoid leaking
// existence).
func (c *Client) GetChat(ctx context.Context, id string) (*ChatDetail, error) {
	var d ChatDetail
	if err := c.do(ctx, http.MethodGet, "/api/chats/"+id, nil, nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// DeleteChat deletes a chat and its messages. A 404 means missing or not owned.
func (c *Client) DeleteChat(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/chats/"+id, nil, nil, nil)
}
