package telegram

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
)

// Conversation windowing constants. We group consecutive messages from the
// same chat into "conversation windows" before emitting them as documents,
// which gives the embedder enough context to produce meaningful vectors and
// avoids the noise-hub problem caused by indexing thousands of one-line chat
// messages individually.
const (
	// conversationWindowGap is the time gap between two messages that triggers
	// a new window. Messages within this window are grouped together.
	conversationWindowGap = 30 * time.Minute

	// conversationWindowMaxChars caps the size of a window so an active chat
	// doesn't produce one giant 50KB document. When a window reaches this many
	// characters, the next message starts a new window even if it's within
	// the time gap.
	conversationWindowMaxChars = 2000
)

// messageRecord is a temporary representation of a Telegram message used
// during the windowing pass before producing model.Documents.
type messageRecord struct {
	ID   int
	Text string
	Date time.Time
}

// telegramAPI abstracts the Telegram API calls for testability.
type telegramAPI interface {
	MessagesGetDialogs(ctx context.Context, req *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error)
	MessagesGetHistory(ctx context.Context, req *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
}

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

	err := client.Run(ctx, func(ctx context.Context) error {
		var fetchErr error
		docs, fetchErr = c.fetchWithAPI(ctx, client.API(), cursor)
		return fetchErr
	})

	if err != nil {
		return nil, fmt.Errorf("telegram: client run: %w", err)
	}

	now := time.Now()
	return &model.FetchResult{
		Documents: docs,
		Cursor: &model.SyncCursor{
			CursorData: map[string]any{
				"last_message_date": float64(now.Unix()),
			},
			LastSync:    now,
			LastStatus:  "success",
			ItemsSynced: len(docs),
		},
	}, nil
}

func (c *Connector) fetchWithAPI(ctx context.Context, api telegramAPI, cursor *model.SyncCursor) ([]model.Document, error) {
	var sinceDate int
	if cursor != nil {
		if ts, ok := cursor.CursorData["last_message_date"].(float64); ok {
			sinceDate = int(ts)
		}
	} else if !c.syncSince.IsZero() {
		sinceDate = int(c.syncSince.Unix())
	}

	dialogs, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      100,
	})
	if err != nil {
		return nil, fmt.Errorf("get dialogs: %w", err)
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
		return nil, nil
	}

	return c.processDialogs(ctx, api, chats, users, sinceDate)
}

func (c *Connector) processDialogs(ctx context.Context, api telegramAPI, chats []tg.ChatClass, users []tg.UserClass, sinceDate int) ([]model.Document, error) {
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

func (c *Connector) fetchChatMessages(ctx context.Context, api telegramAPI, inputPeer tg.InputPeerClass, chatName, chatID string, sinceDate int) ([]model.Document, error) {
	// Collect messages into records first (instead of emitting one Document per
	// message), so we can sort by date and group them into conversation windows
	// before producing the final docs. This is the central change that fixes
	// the embedding noise from short chat messages.
	var records []messageRecord

	req := &tg.MessagesGetHistoryRequest{
		Peer:  inputPeer,
		Limit: 100,
	}

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		result, err := api.MessagesGetHistory(ctx, req)
		if err != nil {
			return nil, err
		}

		messages := extractMessages(result)
		if len(messages) == 0 {
			break
		}

		stop := false
		for _, msg := range messages {
			m, ok := msg.(*tg.Message)
			if !ok || m.Message == "" {
				continue
			}

			// Skip messages older than sinceDate
			if sinceDate > 0 && m.Date < sinceDate {
				stop = true
				break
			}

			records = append(records, messageRecord{
				ID:   m.ID,
				Text: m.Message,
				Date: time.Unix(int64(m.Date), 0),
			})
		}

		if stop {
			break
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

	return c.windowMessages(records, chatName, chatID), nil
}

// windowMessages groups consecutive messages from a single chat into
// "conversation windows" and emits one Document per window. A window is closed
// (and a new one started) when the gap between two messages exceeds
// conversationWindowGap, OR when adding the next message would push the window
// past conversationWindowMaxChars.
//
// Input records may be in any order — they are sorted by date ascending here
// before windowing so the resulting windows are chronological regardless of
// how the records arrived from the API.
func (c *Connector) windowMessages(records []messageRecord, chatName, chatID string) []model.Document {
	if len(records) == 0 {
		return nil
	}

	// Sort chronologically (oldest first) so windows reflect actual conversation flow.
	sort.Slice(records, func(i, j int) bool {
		return records[i].Date.Before(records[j].Date)
	})

	var docs []model.Document
	var window []messageRecord
	var windowChars int

	flush := func() {
		if len(window) == 0 {
			return
		}
		docs = append(docs, c.makeWindowDoc(window, chatName, chatID))
		window = nil
		windowChars = 0
	}

	for _, rec := range records {
		newSize := windowChars + len(rec.Text)
		if len(window) > 0 {
			gap := rec.Date.Sub(window[len(window)-1].Date)
			if gap > conversationWindowGap || newSize > conversationWindowMaxChars {
				flush()
				newSize = len(rec.Text)
			}
		}
		window = append(window, rec)
		windowChars = newSize
	}
	flush()

	return docs
}

// makeWindowDoc converts a non-empty slice of message records into a single
// Document. The window's content is the joined message texts (preserving
// message boundaries with newlines). CreatedAt is the latest message in the
// window, so recency decay reflects when the conversation last had activity.
func (c *Connector) makeWindowDoc(window []messageRecord, chatName, chatID string) model.Document {
	first := window[0]
	last := window[len(window)-1]

	texts := make([]string, len(window))
	for i, r := range window {
		texts[i] = r.Text
	}
	content := strings.Join(texts, "\n")

	sourceID := fmt.Sprintf("%s:%d-%d", chatID, first.ID, last.ID)

	return model.Document{
		ID:         model.DocumentID("telegram", c.name, sourceID),
		SourceType: "telegram",
		SourceName: c.name,
		SourceID:   sourceID,
		Title:      chatName,
		Content:    content,
		Metadata: map[string]any{
			"chat_name":        chatName,
			"chat_id":          chatID,
			"first_message_id": first.ID,
			"last_message_id":  last.ID,
			"message_count":    len(window),
			"date_range_start": first.Date.Format(time.RFC3339),
			"date_range_end":   last.Date.Format(time.RFC3339),
		},
		Visibility: "private",
		CreatedAt:  last.Date,
	}
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
