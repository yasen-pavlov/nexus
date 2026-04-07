package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
)

func init() {
	connector.Register("telegram", func() connector.Connector {
		return &Connector{}
	})
}

// Connector fetches messages from Telegram chats via the MTProto User API.
type Connector struct {
	name       string
	apiID      int
	apiHash    string
	phone      string
	chatFilter []string
	syncSince  time.Time
	session    *DBSessionStorage
}

func (c *Connector) Type() string { return "telegram" }
func (c *Connector) Name() string { return c.name }

func (c *Connector) Configure(cfg connector.Config) error {
	name, _ := cfg["name"].(string)
	if name == "" {
		name = "telegram"
	}
	c.name = name

	// api_id can come as string or number from JSON
	switch v := cfg["api_id"].(type) {
	case string:
		id, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("telegram: invalid api_id: %w", err)
		}
		c.apiID = id
	case float64:
		c.apiID = int(v)
	default:
		return fmt.Errorf("telegram: api_id is required")
	}

	apiHash, _ := cfg["api_hash"].(string)
	if apiHash == "" {
		return fmt.Errorf("telegram: api_hash is required")
	}
	c.apiHash = apiHash

	phone, _ := cfg["phone"].(string)
	if phone == "" {
		return fmt.Errorf("telegram: phone is required")
	}
	c.phone = phone

	if filter, _ := cfg["chat_filter"].(string); filter != "" {
		for _, f := range strings.Split(filter, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				c.chatFilter = append(c.chatFilter, f)
			}
		}
	}

	c.syncSince = connector.ComputeSyncSince(cfg)

	return nil
}

// SetSession sets the session storage (called by the auth flow handler).
func (c *Connector) SetSession(s *DBSessionStorage) {
	c.session = s
}

// Session returns the current session storage.
func (c *Connector) Session() *DBSessionStorage {
	return c.session
}

func (c *Connector) Validate() error {
	if c.apiID == 0 || c.apiHash == "" || c.phone == "" {
		return fmt.Errorf("telegram: api_id, api_hash, and phone are required")
	}
	return nil
}

func (c *Connector) Fetch(ctx context.Context, cursor *model.SyncCursor) (*model.FetchResult, error) {
	if c.session == nil || !c.session.HasSession(ctx) {
		return nil, fmt.Errorf("telegram: not authenticated, please connect via the UI first")
	}

	client := telegram.NewClient(c.apiID, c.apiHash, telegram.Options{
		SessionStorage: c.session,
	})

	var docs []model.Document
	var fetchErr error

	err := client.Run(ctx, func(ctx context.Context) error {
		api := client.API()

		// Determine the earliest message date
		var sinceDate int
		if cursor != nil {
			if ts, ok := cursor.CursorData["last_message_date"].(float64); ok {
				sinceDate = int(ts)
			}
		} else if !c.syncSince.IsZero() {
			sinceDate = int(c.syncSince.Unix())
		}

		// Get dialogs
		dialogs, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetPeer: &tg.InputPeerEmpty{},
			Limit:      100,
		})
		if err != nil {
			return fmt.Errorf("get dialogs: %w", err)
		}

		var chats []tg.ChatClass
		var users []tg.UserClass

		switch d := dialogs.(type) {
		case *tg.MessagesDialogs:
			chats = d.Chats
			users = d.Users
		case *tg.MessagesDialogsSlice:
			chats = d.Chats
			users = d.Users
		default:
			return nil
		}

		docs, fetchErr = c.processDialogs(ctx, api, chats, users, sinceDate)
		return fetchErr
	})

	if err != nil {
		return nil, fmt.Errorf("telegram: client run: %w", err)
	}
	if fetchErr != nil {
		return nil, fetchErr
	}

	now := time.Now()
	return &model.FetchResult{
		Documents: docs,
		Cursor: &model.SyncCursor{
			ConnectorID: c.Name(),
			CursorData: map[string]any{
				"last_message_date": float64(now.Unix()),
			},
			LastSync:    now,
			LastStatus:  "success",
			ItemsSynced: len(docs),
		},
	}, nil
}

func (c *Connector) processDialogs(ctx context.Context, api *tg.Client, chats []tg.ChatClass, users []tg.UserClass, sinceDate int) ([]model.Document, error) {
	var allDocs []model.Document

	for _, chat := range chats {
		chatName := chatTitle(chat)
		chatID := chatIdentifier(chat)

		if !c.matchesChatFilter(chatName, chatID) {
			continue
		}

		inputPeer := chatToInputPeer(chat)
		if inputPeer == nil {
			continue
		}

		docs, err := c.fetchChatMessages(ctx, api, inputPeer, chatName, chatID, sinceDate)
		if err != nil {
			continue
		}
		allDocs = append(allDocs, docs...)
	}

	// Also process user DMs
	for _, user := range users {
		u, ok := user.(*tg.User)
		if !ok || u.Bot || u.Self {
			continue
		}

		chatName := userDisplayName(u)
		chatID := strconv.FormatInt(u.ID, 10)

		if !c.matchesChatFilter(chatName, chatID) {
			continue
		}

		inputPeer := &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
		docs, err := c.fetchChatMessages(ctx, api, inputPeer, chatName, chatID, sinceDate)
		if err != nil {
			continue
		}
		allDocs = append(allDocs, docs...)
	}

	return allDocs, nil
}

func (c *Connector) fetchChatMessages(ctx context.Context, api *tg.Client, inputPeer tg.InputPeerClass, chatName, chatID string, sinceDate int) ([]model.Document, error) {
	var docs []model.Document

	req := &tg.MessagesGetHistoryRequest{
		Peer:  inputPeer,
		Limit: 100,
	}

	for {
		if ctx.Err() != nil {
			return docs, ctx.Err()
		}

		result, err := api.MessagesGetHistory(ctx, req)
		if err != nil {
			return docs, err
		}

		messages := extractMessages(result)
		if len(messages) == 0 {
			break
		}

		for _, msg := range messages {
			m, ok := msg.(*tg.Message)
			if !ok || m.Message == "" {
				continue
			}

			// Skip messages older than sinceDate
			if sinceDate > 0 && m.Date < sinceDate {
				return docs, nil // done with this chat
			}

			msgTime := time.Unix(int64(m.Date), 0)
			docs = append(docs, model.Document{
				ID:         uuid.New(),
				SourceType: "telegram",
				SourceName: c.name,
				SourceID:   fmt.Sprintf("%s:%d", chatID, m.ID),
				Title:      chatName,
				Content:    m.Message,
				Metadata: map[string]any{
					"chat_name":  chatName,
					"chat_id":    chatID,
					"message_id": m.ID,
					"date":       msgTime.Format(time.RFC3339),
				},
				Visibility: "private",
				CreatedAt:  msgTime,
			})
		}

		// Pagination: use the last message's ID as offset
		lastMsg := messages[len(messages)-1]
		if m, ok := lastMsg.(*tg.Message); ok {
			req.OffsetID = m.ID
			req.AddOffset = 0
		} else {
			break
		}

		if len(messages) < 100 {
			break // no more messages
		}
	}

	return docs, nil
}

func (c *Connector) matchesChatFilter(name, id string) bool {
	if len(c.chatFilter) == 0 {
		return true
	}
	nameLower := strings.ToLower(name)
	for _, f := range c.chatFilter {
		if strings.ToLower(f) == nameLower || f == id {
			return true
		}
	}
	return false
}

func chatTitle(chat tg.ChatClass) string {
	switch c := chat.(type) {
	case *tg.Chat:
		return c.Title
	case *tg.Channel:
		return c.Title
	default:
		return "Unknown"
	}
}

func chatIdentifier(chat tg.ChatClass) string {
	switch c := chat.(type) {
	case *tg.Chat:
		return strconv.FormatInt(c.ID, 10)
	case *tg.Channel:
		return strconv.FormatInt(c.ID, 10)
	default:
		return "0"
	}
}

func chatToInputPeer(chat tg.ChatClass) tg.InputPeerClass {
	switch c := chat.(type) {
	case *tg.Chat:
		return &tg.InputPeerChat{ChatID: c.ID}
	case *tg.Channel:
		return &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}
	default:
		return nil
	}
}

func userDisplayName(u *tg.User) string {
	if u.FirstName != "" && u.LastName != "" {
		return u.FirstName + " " + u.LastName
	}
	if u.FirstName != "" {
		return u.FirstName
	}
	if u.Username != "" {
		return u.Username
	}
	return "User " + strconv.FormatInt(u.ID, 10)
}

func extractMessages(result tg.MessagesMessagesClass) []tg.MessageClass {
	switch r := result.(type) {
	case *tg.MessagesMessages:
		return r.Messages
	case *tg.MessagesMessagesSlice:
		return r.Messages
	case *tg.MessagesChannelMessages:
		return r.Messages
	default:
		return nil
	}
}
