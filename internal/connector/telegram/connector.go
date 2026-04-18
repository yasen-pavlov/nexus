package telegram

import (
	"bytes"
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
	"github.com/muty/nexus/internal/pipeline/extractor"
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
// during the windowing pass before producing model.Documents. Carries
// the fields needed to attribute each message to a sender in both
// group chats (via FromID) and DMs (via Out + dmPeerID context), so
// window docs can emit per-line sender metadata matching what the
// per-message docs emit.
type messageRecord struct {
	ID     int
	Text   string
	Date   time.Time
	FromID tg.PeerClass
	Out    bool
}

// AvatarSourceID returns the binary-cache source ID used to store a
// Telegram user's profile photo. Kept as a package-level helper so the
// HTTP avatar endpoint can read the same key without cross-package
// coupling on the connector.
func AvatarSourceID(userID int64) string {
	return fmt.Sprintf("avatars:%d", userID)
}

// telegramAPI abstracts the Telegram API calls for testability.
type telegramAPI interface {
	MessagesGetDialogs(ctx context.Context, req *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error)
	MessagesGetHistory(ctx context.Context, req *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
}

// mediaDownloader downloads the bytes for a Telegram file location.
// Abstracted so tests can bypass the live MTProto client.
type mediaDownloader interface {
	Download(ctx context.Context, loc tg.InputFileLocationClass) ([]byte, error)
}

// liveMediaDownloader is the production mediaDownloader backed by a
// live *telegram.Client (via its Download/Stream helpers).
type liveMediaDownloader struct {
	client *telegram.Client
}

func (l liveMediaDownloader) Download(ctx context.Context, loc tg.InputFileLocationClass) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := l.client.Download(loc).Stream(ctx, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func init() {
	connector.Register("telegram", func() connector.Connector {
		return &Connector{}
	})
}

// Connector fetches messages from Telegram chats via the MTProto User API.
//
// Deletion sync: this connector permanently opts out by leaving
// FetchResult.CurrentSourceIDs as nil. MTProto doesn't expose a
// reliable "list all message IDs in a chat" signal — message
// deletions aren't surfaced via incremental updates and full history
// re-enumeration would defeat the purpose of incremental sync.
// Telegram docs are only removed from the index by a full reindex.
type Connector struct {
	name        string
	apiID       int
	apiHash     string
	phone       string
	chatFilter  []string
	syncSince   time.Time
	session     *DBSessionStorage
	extractor   *extractor.Registry
	binaryStore connector.BinaryStoreAPI
	cacheConfig connector.CacheConfig
}

func (c *Connector) Type() string { return "telegram" }
func (c *Connector) Name() string { return c.name }

// SetExtractor sets the content extractor used for downloaded media
// (e.g. PDF documents, text files attached to chat messages).
func (c *Connector) SetExtractor(ext *extractor.Registry) {
	c.extractor = ext
}

// SetBinaryStore wires the binary content cache plus the resolved
// per-connector cache policy. Implements connector.CacheAware.
// Telegram runs in eager mode by default: media bytes are written
// during Fetch because Telegram file references expire and lazy
// refetch isn't viable.
func (c *Connector) SetBinaryStore(store connector.BinaryStoreAPI, cfg connector.CacheConfig) {
	c.binaryStore = store
	c.cacheConfig = cfg
}

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
		// Resolve the authenticated user's own ID once per sync. Used
		// to attribute DM messages where m.Out=true but m.FromID is
		// nil (Telegram commonly omits FromID in private chats since
		// the sender is implicit). Failure is non-fatal — falls back
		// to FromID-only resolution.
		var selfID int64
		if self, err := client.Self(ctx); err == nil && self != nil {
			selfID = self.ID
		}
		var fetchErr error
		docs, fetchErr = c.fetchWithAPI(ctx, client.API(), liveMediaDownloader{client: client}, cursor, selfID)
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

func (c *Connector) fetchWithAPI(ctx context.Context, api telegramAPI, dl mediaDownloader, cursor *model.SyncCursor, selfID int64) ([]model.Document, error) {
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

	userMap := buildUserMap(users)
	return c.processDialogs(ctx, api, dl, chats, users, userMap, selfID, sinceDate)
}

// buildUserMap indexes a slice of Telegram UserClass values by their
// numeric user ID so downstream message emission can look up sender
// display metadata without a second API round-trip per message.
func buildUserMap(users []tg.UserClass) map[int64]*tg.User {
	m := make(map[int64]*tg.User, len(users))
	for _, u := range users {
		if tu, ok := u.(*tg.User); ok {
			m[tu.ID] = tu
		}
	}
	return m
}

func (c *Connector) processDialogs(ctx context.Context, api telegramAPI, dl mediaDownloader, chats []tg.ChatClass, users []tg.UserClass, userMap map[int64]*tg.User, selfID int64, sinceDate int) ([]model.Document, error) {
	var allDocs []model.Document

	// Group chats & channels — dmPeerID=0 because the sender is
	// always explicit via m.FromID in multi-user rooms.
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

		docs, err := c.fetchChatMessages(ctx, api, dl, inputPeer, chatName, chatID, userMap, selfID, 0, sinceDate)
		if err != nil {
			continue
		}
		allDocs = append(allDocs, docs...)
	}

	// Private chats — every message is between two knowable users, so
	// we pass u.ID as dmPeerID. makeMessageDoc uses that to attribute
	// m.Out=false messages to the peer when FromID is nil (the common
	// case in Telegram DMs).
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

		// Cache the DM peer's avatar — it'll be used as the sender avatar
		// for all of their messages in the DM.
		c.ensureAvatarCached(ctx, dl, u)

		inputPeer := &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
		docs, err := c.fetchChatMessages(ctx, api, dl, inputPeer, chatName, chatID, userMap, selfID, u.ID, sinceDate)
		if err != nil {
			continue
		}
		allDocs = append(allDocs, docs...)
	}

	return allDocs, nil
}

func (c *Connector) fetchChatMessages(ctx context.Context, api telegramAPI, dl mediaDownloader, inputPeer tg.InputPeerClass, chatName, chatID string, userMap map[int64]*tg.User, selfID, dmPeerID int64, sinceDate int) ([]model.Document, error) {
	// Dual emission: each Telegram message produces up to three documents:
	//
	//   1. a conversation *window* doc — the retrieval unit, embedded and
	//      searchable, whose content is several messages joined together.
	//   2. a canonical per-*message* doc — Hidden so it doesn't surface in
	//      default search, used for reply_to targets and chat-browser
	//      pagination.
	//   3. a media doc — when m.Media is downloadable (see mediaToDocument).
	//
	// The retrieval unit (window) != product unit (message) is deliberate:
	// windows keep embeddings honest on short chat text, messages keep the
	// reply graph and UI navigation clean. See plans/scalable-beaming-tower.md.
	var records []messageRecord
	var allMessages []*tg.Message

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

		// GetHistory responses carry users for forwarded messages, reply
		// targets, and participants. Merge them into the shared map so
		// sender lookup works even for users not in the top-level dialog
		// list.
		for id, u := range buildUserMap(extractHistoryUsers(result)) {
			if _, exists := userMap[id]; !exists {
				userMap[id] = u
			}
		}

		messages := extractMessages(result)
		if len(messages) == 0 {
			break
		}

		stop := false
		for _, msg := range messages {
			m, ok := msg.(*tg.Message)
			if !ok {
				continue
			}

			// Skip messages older than sinceDate. Applies before both
			// text and media handling so we don't re-download media
			// already cached from previous syncs.
			if sinceDate > 0 && m.Date < sinceDate {
				stop = true
				break
			}

			allMessages = append(allMessages, m)
			if m.Message != "" {
				records = append(records, messageRecord{
					ID:     m.ID,
					Text:   m.Message,
					Date:   time.Unix(int64(m.Date), 0),
					FromID: m.FromID,
					Out:    m.Out,
				})
			}
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

	windowDocs, msgIDToWindow := c.windowMessages(records, chatName, chatID, userMap, selfID, dmPeerID)

	docs := make([]model.Document, 0, len(windowDocs)+len(allMessages))
	docs = append(docs, windowDocs...)

	for _, m := range allMessages {
		// Skip messages that carry neither text nor media — typically
		// edit-cleared shells or unsupported event types that leaked
		// past the *tg.Message type filter. A canonical record for them
		// would just pollute the chat browser.
		if m.Message == "" && m.Media == nil {
			continue
		}
		// Eagerly cache the sender's avatar before emitting the doc so
		// the frontend's first render after sync can show the image
		// without another sync round-trip. No-op when already cached.
		if senderID := resolveSenderID(m, selfID, dmPeerID); senderID != 0 {
			if user, known := userMap[senderID]; known {
				c.ensureAvatarCached(ctx, dl, user)
			}
		}
		msgDoc := c.makeMessageDoc(m, chatName, chatID, msgIDToWindow[m.ID], userMap, selfID, dmPeerID)
		if m.Media != nil {
			if mediaDoc, ok := c.mediaToDocument(ctx, dl, m, chatName, chatID); ok {
				// Annotate the canonical message doc with attachment
				// metadata so the chat UI can render attachment chips
				// without a separate /related round-trip per message.
				if msgDoc.Metadata == nil {
					msgDoc.Metadata = map[string]any{}
				}
				att := map[string]any{
					"id":        mediaDoc.ID.String(),
					"source_id": mediaDoc.SourceID,
					"mime_type": mediaDoc.MimeType,
					"size":      mediaDoc.Size,
				}
				if filename, _ := mediaDoc.Metadata["filename"].(string); filename != "" {
					att["filename"] = filename
				} else {
					att["filename"] = mediaDoc.Title
				}
				existing, _ := msgDoc.Metadata["attachments"].([]map[string]any)
				msgDoc.Metadata["attachments"] = append(existing, att)
				docs = append(docs, mediaDoc)
			}
		}
		docs = append(docs, msgDoc)
	}

	return docs, nil
}

// makeMessageDoc emits the canonical per-message record. It's Hidden
// (excluded from default search so it doesn't duplicate the window doc's
// hit) but carries everything the chat browser and relation resolver need:
// the text, the reply edge, the member-of-window edge (when the message is
// part of a text window), and sender metadata.
// resolveSenderID picks the numeric sender ID for a Telegram message
// across group chats and DMs. Group messages carry m.FromID explicitly.
// DMs commonly leave FromID nil because the sender is implicit: if
// m.Out=true the caller sent it (selfID), otherwise the DM peer sent
// it (dmPeerID). Returns 0 when nothing attributable is available
// (channel posts, service messages, or group DMs where the sender
// wasn't in the dialog batch).
func resolveSenderID(m *tg.Message, selfID, dmPeerID int64) int64 {
	if u, ok := m.FromID.(*tg.PeerUser); ok && u.UserID != 0 {
		return u.UserID
	}
	if dmPeerID == 0 {
		return 0
	}
	if m.Out {
		return selfID
	}
	return dmPeerID
}

func (c *Connector) makeMessageDoc(m *tg.Message, chatName, chatID, windowSourceID string, userMap map[int64]*tg.User, selfID, dmPeerID int64) model.Document {
	sourceID := fmt.Sprintf("%s:%d:msg", chatID, m.ID)

	relations := make([]model.Relation, 0, 2)
	if windowSourceID != "" {
		relations = append(relations, model.Relation{
			Type:           model.RelationMemberOfWindow,
			TargetSourceID: windowSourceID,
			TargetID:       model.DocumentID("telegram", c.name, windowSourceID).String(),
		})
	}
	if h, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok && h.ReplyToMsgID > 0 {
		targetChatID := chatID
		if h.ReplyToPeerID != nil {
			resolved, ok := peerChatID(h.ReplyToPeerID)
			if !ok {
				resolved = ""
			}
			targetChatID = resolved
		}
		if targetChatID != "" {
			replyTargetSourceID := fmt.Sprintf("%s:%d:msg", targetChatID, h.ReplyToMsgID)
			relations = append(relations, model.Relation{
				Type:           model.RelationReplyTo,
				TargetSourceID: replyTargetSourceID,
				TargetID:       model.DocumentID("telegram", c.name, replyTargetSourceID).String(),
			})
		}
	}

	metadata := map[string]any{
		"chat_id":    chatID,
		"chat_name":  chatName,
		"message_id": m.ID,
	}
	if senderID := resolveSenderID(m, selfID, dmPeerID); senderID != 0 {
		metadata["sender_id"] = senderID
		if user, known := userMap[senderID]; known {
			metadata["sender_name"] = userDisplayName(user)
			if user.Username != "" {
				metadata["sender_username"] = user.Username
			}
			if hasDownloadableAvatar(user) {
				metadata["sender_avatar_key"] = AvatarSourceID(senderID)
			}
		}
	}

	return model.Document{
		ID:             model.DocumentID("telegram", c.name, sourceID),
		SourceType:     "telegram",
		SourceName:     c.name,
		SourceID:       sourceID,
		Title:          chatName,
		Content:        m.Message,
		Metadata:       metadata,
		Relations:      relations,
		ConversationID: chatID,
		Hidden:         true,
		Visibility:     "private",
		CreatedAt:      time.Unix(int64(m.Date), 0),
	}
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
func (c *Connector) windowMessages(records []messageRecord, chatName, chatID string, userMap map[int64]*tg.User, selfID, dmPeerID int64) ([]model.Document, map[int]string) {
	msgIDToWindow := map[int]string{}
	if len(records) == 0 {
		return nil, msgIDToWindow
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
		doc := c.makeWindowDoc(window, chatName, chatID, userMap, selfID, dmPeerID)
		docs = append(docs, doc)
		for _, r := range window {
			msgIDToWindow[r.ID] = doc.SourceID
		}
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

	return docs, msgIDToWindow
}

// messageLine is the per-entry shape inside a window doc's
// message_lines metadata array. Carries exactly the identity bits the
// search card needs to render a message row without a secondary fetch,
// plus the id/created_at the chat view needs for anchor navigation.
// Kept as a plain map[string]any when emitted to avoid any encoding
// surprises across the index → response boundary.
func buildMessageLine(r messageRecord, userMap map[int64]*tg.User, selfID, dmPeerID int64) map[string]any {
	line := map[string]any{
		"id":         r.ID,
		"text":       r.Text,
		"created_at": r.Date.Format(time.RFC3339),
	}
	// Resolve the sender using the same rules per-message docs apply,
	// so group chats and DMs attribute consistently across both doc
	// types. We build a lightweight *tg.Message for reuse of
	// resolveSenderID — avoids a parallel helper that could drift.
	probe := &tg.Message{FromID: r.FromID, Out: r.Out}
	if senderID := resolveSenderID(probe, selfID, dmPeerID); senderID != 0 {
		line["sender_id"] = senderID
		if user, known := userMap[senderID]; known {
			line["sender_name"] = userDisplayName(user)
			if user.Username != "" {
				line["sender_username"] = user.Username
			}
			if hasDownloadableAvatar(user) {
				line["sender_avatar_key"] = AvatarSourceID(senderID)
			}
		}
	}
	return line
}

// makeWindowDoc converts a non-empty slice of message records into a single
// Document. The window's content is the joined message texts (preserving
// message boundaries with newlines). CreatedAt is the latest message in the
// window, so recency decay reflects when the conversation last had activity.
//
// The per-line message_lines metadata array is what drives the search
// card's match-mode rendering (via highlight→line mapping) and the
// semantic-fallback bookended preview. One array, two consumers.
func (c *Connector) makeWindowDoc(window []messageRecord, chatName, chatID string, userMap map[int64]*tg.User, selfID, dmPeerID int64) model.Document {
	first := window[0]
	last := window[len(window)-1]

	texts := make([]string, len(window))
	messageIDs := make([]int, len(window))
	messageLines := make([]map[string]any, len(window))
	for i, r := range window {
		texts[i] = r.Text
		messageIDs[i] = r.ID
		messageLines[i] = buildMessageLine(r, userMap, selfID, dmPeerID)
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
			"chat_name":            chatName,
			"chat_id":              chatID,
			"first_message_id":     first.ID,
			"last_message_id":      last.ID,
			"message_count":        len(window),
			"date_range_start":     first.Date.Format(time.RFC3339),
			"date_range_end":       last.Date.Format(time.RFC3339),
			"anchor_message_id":    first.ID,
			"anchor_created_at":    first.Date.Format(time.RFC3339),
			"included_message_ids": messageIDs,
			"message_lines":        messageLines,
		},
		ConversationID: chatID,
		Visibility:     "private",
		CreatedAt:      last.Date,
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

// peerChatID extracts the bare numeric identifier from a Telegram peer,
// matching the format produced by chatIdentifier and the DM branch in
// fetchChatsAndDMs. Returns false for unknown peer types (e.g. secret
// chats) so callers can skip emitting relations they can't target.
func peerChatID(p tg.PeerClass) (string, bool) {
	switch v := p.(type) {
	case *tg.PeerUser:
		return strconv.FormatInt(v.UserID, 10), true
	case *tg.PeerChat:
		return strconv.FormatInt(v.ChatID, 10), true
	case *tg.PeerChannel:
		return strconv.FormatInt(v.ChannelID, 10), true
	default:
		return "", false
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
	return DisplayName(u)
}

// DisplayName returns a human-readable label for a Telegram user,
// preferring the concatenated first + last name, falling back through
// username to a "User <id>" placeholder. Exposed so the auth handler
// can use the same rule when persisting self-identity.
func DisplayName(u *tg.User) string {
	if u == nil {
		return ""
	}
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

// mediaToDocument turns a single Telegram message carrying downloadable
// media into a sibling Document. The text caption (m.Message) is preserved
// as the doc's Content so it's searchable independently of the windowed
// text document that already holds the same caption.
//
// Returns ok=false (and emits no doc) when:
//   - the media kind isn't downloadable (webpage/geo/poll/etc.)
//   - the download itself fails (expired file reference, network, etc.)
//
// Eager cache population runs inline — the bytes are already in memory
// from the download, so skipping the Put would mean the first preview
// request has no way to recover (Telegram file references expire).
func (c *Connector) mediaToDocument(ctx context.Context, dl mediaDownloader, m *tg.Message, chatName, chatID string) (model.Document, bool) {
	loc, mimeType, filename, _, ok := mediaLocation(m.Media)
	if !ok {
		return model.Document{}, false
	}

	data, err := dl.Download(ctx, loc)
	if err != nil {
		// Best-effort: a single media failure shouldn't derail the sync.
		// The message's text (if any) still produces a window doc.
		return model.Document{}, false
	}

	// Use the actual downloaded byte count — the advertised size in
	// the message header is a prediction; len(data) is truth.
	size := int64(len(data))

	// Photos have no filename attribute; synthesize one so the download
	// endpoint can serve a sensible Content-Disposition without
	// falling back to the chat name as a filename.
	if filename == "" && mimeType == "image/jpeg" {
		filename = fmt.Sprintf("photo-%d.jpg", m.ID)
	}

	sourceID := fmt.Sprintf("%s:%d:media", chatID, m.ID)

	if c.binaryStore != nil && c.cacheConfig.Mode == "eager" {
		// Best-effort — a cache write failure shouldn't abort the doc
		// emit. If this Put fails the preview endpoint will surface a
		// clear "media not cached" error later, which is the correct
		// degraded-state behavior.
		_ = c.binaryStore.Put(ctx, "telegram", c.name, sourceID, bytes.NewReader(data), int64(len(data)))
	}

	var extracted string
	if c.extractor != nil && c.extractor.CanExtract(mimeType) {
		if out, err := c.extractor.Extract(ctx, mimeType, data); err == nil {
			extracted = out
		}
	}

	// Caption flows through both the windowed text doc and the media
	// doc. Having it in both is deliberate: the text window is for
	// conversation-context ranking, while the media doc is the
	// standalone searchable representation of the asset itself.
	content := extracted
	if content == "" {
		content = m.Message
	}

	title := filename
	if title == "" {
		title = chatName
	}

	metadata := map[string]any{
		"chat_name":    chatName,
		"chat_id":      chatID,
		"content_type": mimeType,
	}
	if filename != "" {
		metadata["filename"] = filename
	}
	if m.Message != "" {
		metadata["caption"] = m.Message
	}

	// attachment_of points at the per-message doc (the canonical record
	// for this Telegram message), not the window. The window can be
	// reached by walking member_of_window from the message doc.
	parentMsgSourceID := fmt.Sprintf("%s:%d:msg", chatID, m.ID)

	return model.Document{
		ID:         model.DocumentID("telegram", c.name, sourceID),
		SourceType: "telegram",
		SourceName: c.name,
		SourceID:   sourceID,
		Title:      title,
		Content:    content,
		MimeType:   mimeType,
		Size:       size,
		Metadata:   metadata,
		Relations: []model.Relation{{
			Type:           model.RelationAttachmentOf,
			TargetSourceID: parentMsgSourceID,
			TargetID:       model.DocumentID("telegram", c.name, parentMsgSourceID).String(),
		}},
		ConversationID: chatID,
		Visibility:     "private",
		CreatedAt:      time.Unix(int64(m.Date), 0),
	}, true
}

// mediaLocation inspects a message's media and returns the
// InputFileLocation needed to download it, plus the sidecar metadata
// (mime type, filename, size) the indexer needs.
//
// Returns ok=false for non-downloadable media: webpages, geo points,
// polls, contacts, games, invoices, venues, unsupported types, and the
// empty placeholder. Photos always report size=0 here because the
// advertised size lives on the individual PhotoSize entry, not the
// parent media object — callers compensate from the downloaded bytes.
func mediaLocation(media tg.MessageMediaClass) (loc tg.InputFileLocationClass, mimeType, filename string, size int64, ok bool) {
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		photo, pok := m.Photo.AsNotEmpty()
		if !pok {
			return nil, "", "", 0, false
		}
		thumbType, photoSize, sok := largestPhotoSize(photo.Sizes)
		if !sok {
			return nil, "", "", 0, false
		}
		return &tg.InputPhotoFileLocation{
			ID:            photo.ID,
			AccessHash:    photo.AccessHash,
			FileReference: photo.FileReference,
			ThumbSize:     thumbType,
		}, "image/jpeg", "", photoSize, true

	case *tg.MessageMediaDocument:
		doc, dok := m.Document.AsNotEmpty()
		if !dok {
			return nil, "", "", 0, false
		}
		// Stickers are emoji-equivalent decorations — no search value,
		// and their Lottie JSON (for animated stickers) actively
		// pollutes ranking. Skip them. GIFs (DocumentAttributeAnimated)
		// and round video messages still flow through since they can
		// carry meaningful content.
		if isSticker(doc) {
			return nil, "", "", 0, false
		}
		return &tg.InputDocumentFileLocation{
			ID:            doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
			ThumbSize:     "",
		}, doc.MimeType, documentFilename(doc), doc.Size, true
	}
	return nil, "", "", 0, false
}

// isSticker reports whether a Telegram document is a sticker (animated
// .tgs, static .webp, or video .webm sticker). Sticker documents always
// carry a *tg.DocumentAttributeSticker in their Attributes slice.
func isSticker(doc *tg.Document) bool {
	for _, attr := range doc.Attributes {
		if _, ok := attr.(*tg.DocumentAttributeSticker); ok {
			return true
		}
	}
	return false
}

// largestPhotoSize picks the largest downloadable PhotoSize from a
// Telegram photo's sizes array and returns its type (used as ThumbSize
// in InputPhotoFileLocation) and advertised byte size.
//
// Stripped/Cached/Path sizes are inline previews, not downloadable from
// the file servers — skip them. Progressive sizes carry multiple byte
// offsets; we use the final (largest) size from its Sizes slice.
func largestPhotoSize(sizes []tg.PhotoSizeClass) (string, int64, bool) {
	var bestType string
	var bestSize int64
	found := false
	for _, s := range sizes {
		switch ps := s.(type) {
		case *tg.PhotoSize:
			if int64(ps.Size) > bestSize {
				bestType = ps.Type
				bestSize = int64(ps.Size)
				found = true
			}
		case *tg.PhotoSizeProgressive:
			var last int
			for _, sz := range ps.Sizes {
				if sz > last {
					last = sz
				}
			}
			if int64(last) > bestSize {
				bestType = ps.Type
				bestSize = int64(last)
				found = true
			}
		}
	}
	return bestType, bestSize, found
}

// documentFilename extracts the filename attribute from a Telegram
// document. Returns "" when no filename attribute is present (voice
// notes, round videos, stickers — all rely on auto-generated names).
func documentFilename(doc *tg.Document) string {
	for _, attr := range doc.Attributes {
		if fn, ok := attr.(*tg.DocumentAttributeFilename); ok {
			return fn.FileName
		}
	}
	return ""
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

// extractHistoryUsers returns the Users slice from a GetHistory response.
// Telegram populates this with senders of forwarded messages, reply
// targets, and participants referenced by the returned messages — useful
// for resolving sender display info without a second API call.
func extractHistoryUsers(result tg.MessagesMessagesClass) []tg.UserClass {
	switch r := result.(type) {
	case *tg.MessagesMessages:
		return r.Users
	case *tg.MessagesMessagesSlice:
		return r.Users
	case *tg.MessagesChannelMessages:
		return r.Users
	default:
		return nil
	}
}

// hasDownloadableAvatar reports whether a Telegram user has a profile
// photo the connector can fetch. Returns false for accounts that never
// set a photo or whose photo is the empty placeholder.
func hasDownloadableAvatar(u *tg.User) bool {
	if u == nil {
		return false
	}
	photo, ok := u.Photo.(*tg.UserProfilePhoto)
	return ok && photo.PhotoID != 0
}

// ensureAvatarCached fetches and caches a Telegram user's profile photo
// if one exists and isn't already in the binary store. No-op when the
// user has no photo, when the store is unwired, or when the connector
// isn't in eager cache mode. Failures are silent — avatar pipeline
// failures must never block message indexing, and the frontend has an
// initials fallback.
func (c *Connector) ensureAvatarCached(ctx context.Context, dl mediaDownloader, user *tg.User) {
	if c.binaryStore == nil || c.cacheConfig.Mode != "eager" {
		return
	}
	if !hasDownloadableAvatar(user) {
		return
	}

	sourceID := AvatarSourceID(user.ID)
	if exists, _ := c.binaryStore.Exists(ctx, "telegram", c.name, sourceID); exists {
		return
	}

	photo, ok := user.Photo.(*tg.UserProfilePhoto)
	if !ok {
		return
	}

	loc := &tg.InputPeerPhotoFileLocation{
		Big:     false,
		Peer:    &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash},
		PhotoID: photo.PhotoID,
	}
	data, err := dl.Download(ctx, loc)
	if err != nil || len(data) == 0 {
		return
	}

	_ = c.binaryStore.Put(ctx, "telegram", c.name, sourceID, bytes.NewReader(data), int64(len(data)))
}
