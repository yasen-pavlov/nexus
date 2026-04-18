package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline/extractor"
)

// mockTelegramAPI implements telegramAPI for testing.
type mockTelegramAPI struct {
	dialogs tg.MessagesDialogsClass
	msgList []tg.MessagesMessagesClass // returned in order
	msgIdx  int
}

// stubDownloader is a mediaDownloader that returns a fixed payload
// (or a fixed error) for every location. Tests that don't exercise
// media can pass a zero-value stubDownloader.
type stubDownloader struct {
	payload []byte
	err     error
	calls   atomic.Int32
}

func (s *stubDownloader) Download(_ context.Context, _ tg.InputFileLocationClass) ([]byte, error) {
	s.calls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return s.payload, nil
}

// fakeBinaryStore is a minimal in-memory BinaryStoreAPI stub for tests.
// Mirrors the IMAP test file's shape so the Telegram tests stay
// self-contained.
type fakeBinaryStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
	puts  atomic.Int32
	gets  atomic.Int32
}

func newFakeBinaryStore() *fakeBinaryStore {
	return &fakeBinaryStore{blobs: map[string][]byte{}}
}

func (f *fakeBinaryStore) key(st, sn, sid string) string { return st + "/" + sn + "/" + sid }

func (f *fakeBinaryStore) Put(_ context.Context, st, sn, sid string, r io.Reader, _ int64) error {
	f.puts.Add(1)
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blobs[f.key(st, sn, sid)] = b
	return nil
}

func (f *fakeBinaryStore) Get(_ context.Context, st, sn, sid string) (io.ReadCloser, error) {
	f.gets.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.blobs[f.key(st, sn, sid)]
	if !ok {
		return nil, errNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeBinaryStore) Exists(_ context.Context, st, sn, sid string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.blobs[f.key(st, sn, sid)]
	return ok, nil
}

// errNotExist matches os.ErrNotExist for the fake store so tests don't
// have to import os just for this.
var errNotExist = &notExistErr{}

type notExistErr struct{}

func (*notExistErr) Error() string { return "not exist" }

// Is satisfies errors.Is against os.ErrNotExist without importing os.
func (*notExistErr) Is(target error) bool {
	return target.Error() == "file does not exist"
}

func (m *mockTelegramAPI) MessagesGetDialogs(_ context.Context, _ *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
	return m.dialogs, nil
}

func (m *mockTelegramAPI) MessagesGetHistory(_ context.Context, _ *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error) {
	if m.msgIdx < len(m.msgList) {
		result := m.msgList[m.msgIdx]
		m.msgIdx++
		return result, nil
	}
	return &tg.MessagesMessages{Messages: []tg.MessageClass{}}, nil
}

func TestConfigure(t *testing.T) {
	c := &Connector{}

	t.Run("valid config", func(t *testing.T) {
		err := c.Configure(connector.Config{
			"name":     "my-tg",
			"api_id":   "12345",
			"api_hash": "abcdef",
			"phone":    "+1234567890",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Name() != "my-tg" {
			t.Errorf("expected name 'my-tg', got %q", c.Name())
		}
		if c.Type() != "telegram" {
			t.Errorf("expected type 'telegram', got %q", c.Type())
		}
		if c.apiID != 12345 {
			t.Errorf("expected api_id 12345, got %d", c.apiID)
		}
	})

	t.Run("api_id as float64", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{
			"api_id": float64(99999), "api_hash": "x", "phone": "+1",
		})
		if err != nil {
			t.Fatal(err)
		}
		if c2.apiID != 99999 {
			t.Errorf("expected 99999, got %d", c2.apiID)
		}
	})

	t.Run("missing api_id", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{"api_hash": "x", "phone": "+1"})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("missing api_hash", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{"api_id": "123", "phone": "+1"})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("missing phone", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{"api_id": "123", "api_hash": "x"})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid api_id", func(t *testing.T) {
		c2 := &Connector{}
		err := c2.Configure(connector.Config{"api_id": "notanumber", "api_hash": "x", "phone": "+1"})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("chat filter", func(t *testing.T) {
		c2 := &Connector{}
		c2.Configure(connector.Config{ //nolint:errcheck // test
			"api_id": "1", "api_hash": "x", "phone": "+1",
			"chat_filter": "Family, Work",
		})
		if len(c2.chatFilter) != 2 {
			t.Errorf("expected 2 filters, got %d", len(c2.chatFilter))
		}
	})

	t.Run("default name", func(t *testing.T) {
		c2 := &Connector{}
		c2.Configure(connector.Config{"api_id": "1", "api_hash": "x", "phone": "+1"}) //nolint:errcheck // test
		if c2.Name() != "telegram" {
			t.Errorf("expected default name 'telegram', got %q", c2.Name())
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		c := &Connector{apiID: 123, apiHash: "abc", phone: "+1"}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("missing fields", func(t *testing.T) {
		c := &Connector{}
		if err := c.Validate(); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestMatchesChatFilter(t *testing.T) {
	t.Run("no filter", func(t *testing.T) {
		c := &Connector{}
		if !c.matchesChatFilter("any", "123") {
			t.Error("expected match with no filter")
		}
	})

	t.Run("name match", func(t *testing.T) {
		c := &Connector{chatFilter: []string{"Family"}}
		if !c.matchesChatFilter("Family", "123") {
			t.Error("expected match on name")
		}
		if !c.matchesChatFilter("family", "123") {
			t.Error("expected case-insensitive match")
		}
	})

	t.Run("id match", func(t *testing.T) {
		c := &Connector{chatFilter: []string{"456"}}
		if !c.matchesChatFilter("Other", "456") {
			t.Error("expected match on id")
		}
	})

	t.Run("no match", func(t *testing.T) {
		c := &Connector{chatFilter: []string{"Family"}}
		if c.matchesChatFilter("Work", "789") {
			t.Error("expected no match")
		}
	})
}

func TestSetSession(t *testing.T) {
	c := &Connector{}
	if c.Session() != nil {
		t.Error("expected nil session")
	}
	s := NewDBSessionStorage("test", nil, nil)
	c.SetSession(s)
	if c.Session() == nil {
		t.Error("expected non-nil session")
	}
}

func TestDBSessionStorage(t *testing.T) {
	store := make(map[string]string)
	getSetting := func(_ context.Context, key string) (string, error) {
		return store[key], nil
	}
	setSetting := func(_ context.Context, key, value string) error {
		store[key] = value
		return nil
	}

	s := NewDBSessionStorage("test_key", getSetting, setSetting)

	if s.HasSession(context.Background()) {
		t.Error("expected no session initially")
	}

	data := []byte("session data")
	if err := s.StoreSession(context.Background(), data); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.LoadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded) != "session data" {
		t.Errorf("expected 'session data', got %q", string(loaded))
	}

	if !s.HasSession(context.Background()) {
		t.Error("expected session to exist")
	}
}

func TestProcessDialogs_GroupChat(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{ID: 1, Message: "Hello group!", Date: now},
					&tg.Message{ID: 2, Message: "Second message", Date: now},
				},
			},
		},
	}

	c := &Connector{name: "test"}
	chats := []tg.ChatClass{
		&tg.Chat{ID: 123, Title: "Test Group"},
	}

	docs, err := c.processDialogs(context.Background(), api, &stubDownloader{}, chats, nil, nil, 0, 0)
	if err != nil {
		t.Fatalf("processDialogs failed: %v", err)
	}
	// Dual emission: one window doc (retrieval) + two per-message docs.
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs (window + 2 messages), got %d", len(docs))
	}
	window := docs[0]
	if !strings.Contains(window.Content, "Hello group!") || !strings.Contains(window.Content, "Second message") {
		t.Errorf("expected window to contain both messages, got %q", window.Content)
	}
	if window.SourceType != "telegram" {
		t.Errorf("expected source_type 'telegram', got %q", window.SourceType)
	}
	if window.Metadata["message_count"] != 2 {
		t.Errorf("expected message_count=2, got %v", window.Metadata["message_count"])
	}
	if window.Hidden {
		t.Error("window doc should not be Hidden")
	}
	// Per-message docs are Hidden=true and carry ConversationID=chatID.
	for _, m := range docs[1:] {
		if !m.Hidden {
			t.Errorf("per-message doc %q should be Hidden=true", m.SourceID)
		}
		if m.ConversationID != "123" {
			t.Errorf("per-message doc %q conversation_id = %q, want '123'", m.SourceID, m.ConversationID)
		}
	}
}

func TestProcessDialogs_WithFilter(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{ID: 1, Message: "Filtered out", Date: now},
				},
			},
		},
	}

	c := &Connector{name: "test", chatFilter: []string{"Other Group"}}
	chats := []tg.ChatClass{
		&tg.Chat{ID: 123, Title: "Test Group"},
	}

	docs, err := c.processDialogs(context.Background(), api, &stubDownloader{}, chats, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Errorf("expected 0 docs (filtered), got %d", len(docs))
	}
}

func TestProcessDialogs_UserDMs(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{ID: 10, Message: "DM message", Date: now},
				},
			},
		},
	}

	c := &Connector{name: "test"}
	users := []tg.UserClass{
		&tg.User{ID: 456, FirstName: "John", LastName: "Doe"},
	}

	docs, err := c.processDialogs(context.Background(), api, &stubDownloader{}, nil, users, buildUserMap(users), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Window + per-message doc for one DM message.
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs (window + message), got %d", len(docs))
	}
	if docs[0].Title != "John Doe" {
		t.Errorf("expected title 'John Doe', got %q", docs[0].Title)
	}
}

func TestProcessDialogs_SkipsBots(t *testing.T) {
	api := &mockTelegramAPI{}
	c := &Connector{name: "test"}
	users := []tg.UserClass{
		&tg.User{ID: 1, FirstName: "Bot", Bot: true},
		&tg.User{ID: 2, FirstName: "Self", Self: true},
	}

	docs, err := c.processDialogs(context.Background(), api, &stubDownloader{}, nil, users, buildUserMap(users), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Errorf("expected 0 docs (bots/self skipped), got %d", len(docs))
	}
}

func TestFetchChatMessages_SinceDateFilter(t *testing.T) {
	old := int(time.Now().Add(-24 * time.Hour).Unix())
	recent := int(time.Now().Unix())
	sinceDate := int(time.Now().Add(-1 * time.Hour).Unix())

	api := &mockTelegramAPI{
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{ID: 2, Message: "Recent", Date: recent},
					&tg.Message{ID: 1, Message: "Old message", Date: old},
				},
			},
		},
	}

	c := &Connector{name: "test"}
	inputPeer := &tg.InputPeerChat{ChatID: 123}

	docs, err := c.fetchChatMessages(context.Background(), api, &stubDownloader{}, inputPeer, "Test", "123", map[int64]*tg.User{}, 0, 0, sinceDate)
	if err != nil {
		t.Fatal(err)
	}
	// Only the recent message passes the sinceDate filter → window + message.
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs (window + message) for filtered-by-date, got %d", len(docs))
	}
	if docs[0].Content != "Recent" {
		t.Errorf("expected window content 'Recent', got %q", docs[0].Content)
	}
}

func TestFetchChatMessages_SkipsEmptyMessages(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{ID: 1, Message: "Text message", Date: now},
					&tg.Message{ID: 2, Message: "", Date: now}, // empty
					&tg.MessageService{ID: 3},                  // service message
				},
			},
		},
	}

	c := &Connector{name: "test"}
	docs, err := c.fetchChatMessages(context.Background(), api, &stubDownloader{}, &tg.InputPeerChat{ChatID: 1}, "Chat", "1", map[int64]*tg.User{}, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Text message yields window + per-message doc; empty-no-media message
	// and MessageService are both skipped by the canonical-record gate.
	if len(docs) != 2 {
		t.Errorf("expected 2 docs (window + message for the text one), got %d", len(docs))
	}
}

func TestFetchWithAPI(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		dialogs: &tg.MessagesDialogs{
			Chats: []tg.ChatClass{
				&tg.Chat{ID: 1, Title: "Group"},
			},
		},
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{ID: 1, Message: "Test", Date: now},
				},
			},
		},
	}

	c := &Connector{name: "test"}
	docs, err := c.fetchWithAPI(context.Background(), api, &stubDownloader{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs (window + message), got %d", len(docs))
	}
}

func TestFetchWithAPI_WithCursor(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		dialogs: &tg.MessagesDialogsSlice{
			Chats: []tg.ChatClass{
				&tg.Chat{ID: 1, Title: "Group"},
			},
			Count: 1,
		},
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{ID: 1, Message: "New message", Date: now},
				},
			},
		},
	}

	c := &Connector{name: "test"}
	cursor := &model.SyncCursor{
		CursorData: map[string]any{
			"last_message_date": float64(now - 3600),
		},
	}
	docs, err := c.fetchWithAPI(context.Background(), api, &stubDownloader{}, cursor, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs (window + message), got %d", len(docs))
	}
}

func TestFetchWithAPI_SyncSince(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		dialogs: &tg.MessagesDialogs{
			Chats: []tg.ChatClass{
				&tg.Chat{ID: 1, Title: "Group"},
			},
		},
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{ID: 1, Message: "Recent", Date: now},
				},
			},
		},
	}

	c := &Connector{name: "test", syncSince: time.Now().Add(-24 * time.Hour)}
	docs, err := c.fetchWithAPI(context.Background(), api, &stubDownloader{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs (window + message), got %d", len(docs))
	}
}

func TestFetch_NotAuthenticated(t *testing.T) {
	c := &Connector{apiID: 123, apiHash: "abc", phone: "+1"}
	// No session set
	_, err := c.Fetch(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for unauthenticated connector")
	}
}

func TestProcessDialogs_Channel(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesChannelMessages{
				Messages: []tg.MessageClass{
					&tg.Message{ID: 1, Message: "Channel post", Date: now},
				},
			},
		},
	}

	c := &Connector{name: "test"}
	chats := []tg.ChatClass{
		&tg.Channel{ID: 789, Title: "News Channel", AccessHash: 12345},
	}

	docs, err := c.processDialogs(context.Background(), api, &stubDownloader{}, chats, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs (window + message) from channel, got %d", len(docs))
	}
}

func TestExtractMessages(t *testing.T) {
	msgs := []tg.MessageClass{&tg.Message{ID: 1}}

	tests := []struct {
		name   string
		result tg.MessagesMessagesClass
		want   int
	}{
		{"MessagesMessages", &tg.MessagesMessages{Messages: msgs}, 1},
		{"MessagesMessagesSlice", &tg.MessagesMessagesSlice{Messages: msgs}, 1},
		{"MessagesChannelMessages", &tg.MessagesChannelMessages{Messages: msgs}, 1},
		{"MessagesNotModified", &tg.MessagesMessagesNotModified{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMessages(tt.result)
			if len(got) != tt.want {
				t.Errorf("expected %d, got %d", tt.want, len(got))
			}
		})
	}
}

func TestChatHelpers(t *testing.T) {
	chat := &tg.Chat{ID: 123, Title: "Group"}
	if chatTitle(chat) != "Group" {
		t.Error("wrong title")
	}
	if chatIdentifier(chat) != "123" {
		t.Error("wrong id")
	}
	if chatToInputPeer(chat) == nil {
		t.Error("expected non-nil input peer")
	}

	channel := &tg.Channel{ID: 456, Title: "Chan", AccessHash: 789}
	if chatTitle(channel) != "Chan" {
		t.Error("wrong channel title")
	}
	if chatIdentifier(channel) != "456" {
		t.Error("wrong channel id")
	}
	if chatToInputPeer(channel) == nil {
		t.Error("expected non-nil channel peer")
	}

	// Unknown chat type — exercises the default branches.
	unknown := &tg.ChatForbidden{ID: 999}
	if chatTitle(unknown) != "Unknown" {
		t.Errorf("expected 'Unknown' for unsupported chat type, got %q", chatTitle(unknown))
	}
	if chatIdentifier(unknown) != "0" {
		t.Errorf("expected '0' for unsupported chat type, got %q", chatIdentifier(unknown))
	}
	if chatToInputPeer(unknown) != nil {
		t.Error("expected nil input peer for unsupported chat type")
	}
}

func TestUserDisplayName_FallbackToID(t *testing.T) {
	u := &tg.User{ID: 42}
	got := userDisplayName(u)
	if got != "User 42" {
		t.Errorf("expected 'User 42', got %q", got)
	}
}

func TestDBSessionStorage_NotFound(t *testing.T) {
	store := make(map[string]string)
	getSetting := func(_ context.Context, key string) (string, error) {
		return store[key], nil
	}
	setSetting := func(_ context.Context, key, value string) error {
		store[key] = value
		return nil
	}
	s := NewDBSessionStorage("missing_key", getSetting, setSetting)

	if _, err := s.LoadSession(context.Background()); err == nil {
		t.Error("expected ErrNotFound for missing key")
	}
	if s.HasSession(context.Background()) {
		t.Error("expected HasSession=false for missing key")
	}
}

func TestDBSessionStorage_GetError(t *testing.T) {
	getSetting := func(_ context.Context, _ string) (string, error) {
		return "", errors.New("db error")
	}
	setSetting := func(_ context.Context, _, _ string) error {
		return nil
	}
	s := NewDBSessionStorage("k", getSetting, setSetting)
	if _, err := s.LoadSession(context.Background()); err == nil {
		t.Error("expected error from getSetting failure")
	}
}

func TestDBSessionStorage_BadBase64(t *testing.T) {
	store := map[string]string{"k": "not-valid-base64-!@#$"}
	getSetting := func(_ context.Context, key string) (string, error) {
		return store[key], nil
	}
	setSetting := func(_ context.Context, _, _ string) error {
		return nil
	}
	s := NewDBSessionStorage("k", getSetting, setSetting)
	if _, err := s.LoadSession(context.Background()); err == nil {
		t.Error("expected base64 decode error")
	}
}

func TestDBSessionStorage_Cached(t *testing.T) {
	calls := 0
	store := make(map[string]string)
	getSetting := func(_ context.Context, key string) (string, error) {
		calls++
		return store[key], nil
	}
	setSetting := func(_ context.Context, key, value string) error {
		store[key] = value
		return nil
	}
	s := NewDBSessionStorage("k", getSetting, setSetting)
	if err := s.StoreSession(context.Background(), []byte("data")); err != nil {
		t.Fatal(err)
	}
	// First load: cache hit (s.data was set during StoreSession)
	if _, err := s.LoadSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second load: also cache hit
	if _, err := s.LoadSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("expected 0 getSetting calls (data cached after StoreSession), got %d", calls)
	}
}

func TestHelpers(t *testing.T) {
	// Test userDisplayName
	tests := []struct {
		first, last, username, want string
	}{
		{"John", "Doe", "jd", "John Doe"},
		{"John", "", "jd", "John"},
		{"", "", "jd", "jd"},
	}
	for _, tt := range tests {
		u := &tg.User{FirstName: tt.first, LastName: tt.last, Username: tt.username}
		got := userDisplayName(u)
		if got != tt.want {
			t.Errorf("userDisplayName(%q,%q,%q) = %q, want %q", tt.first, tt.last, tt.username, got, tt.want)
		}
	}
}

// --- Conversation windowing tests ---

func TestWindowMessages_GroupsByTimeGap(t *testing.T) {
	c := &Connector{name: "test"}
	base := time.Date(2026, 4, 11, 10, 0, 0, 0, time.UTC)

	records := []messageRecord{
		{ID: 1, Text: "first", Date: base},
		{ID: 2, Text: "second", Date: base.Add(5 * time.Minute)},            // same window (5 min gap)
		{ID: 3, Text: "third", Date: base.Add(10 * time.Minute)},            // same window
		{ID: 4, Text: "fourth", Date: base.Add(2 * time.Hour)},              // new window (2h gap > 30 min)
		{ID: 5, Text: "fifth", Date: base.Add(2*time.Hour + 5*time.Minute)}, // same as fourth
	}

	docs, _ := c.windowMessages(records, "Test Chat", "100", nil, 0, 0)

	if len(docs) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(docs))
	}
	if mc := docs[0].Metadata["message_count"]; mc != 3 {
		t.Errorf("first window: expected 3 messages, got %v", mc)
	}
	if mc := docs[1].Metadata["message_count"]; mc != 2 {
		t.Errorf("second window: expected 2 messages, got %v", mc)
	}
	if !strings.Contains(docs[0].Content, "first") || !strings.Contains(docs[0].Content, "third") {
		t.Errorf("first window content: %q", docs[0].Content)
	}
}

func TestWindowMessages_RespectsCharCap(t *testing.T) {
	c := &Connector{name: "test"}
	base := time.Date(2026, 4, 11, 10, 0, 0, 0, time.UTC)

	// Each message is 1000 chars, so adding 3 of them in a row should split
	// after the second (2000 + 1000 > 2000 cap).
	bigText := strings.Repeat("a", 1000)
	records := []messageRecord{
		{ID: 1, Text: bigText, Date: base},
		{ID: 2, Text: bigText, Date: base.Add(1 * time.Minute)},
		{ID: 3, Text: bigText, Date: base.Add(2 * time.Minute)},
		{ID: 4, Text: bigText, Date: base.Add(3 * time.Minute)},
	}

	docs, _ := c.windowMessages(records, "Test", "100", nil, 0, 0)
	if len(docs) < 2 {
		t.Errorf("expected multiple windows due to char cap, got %d", len(docs))
	}
}

func TestWindowMessages_SingleMessage(t *testing.T) {
	c := &Connector{name: "test"}
	records := []messageRecord{
		{ID: 1, Text: "lonely message", Date: time.Now()},
	}
	docs, _ := c.windowMessages(records, "Test", "100", nil, 0, 0)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc for 1 message, got %d", len(docs))
	}
	if docs[0].Content != "lonely message" {
		t.Errorf("expected content 'lonely message', got %q", docs[0].Content)
	}
	if mc := docs[0].Metadata["message_count"]; mc != 1 {
		t.Errorf("expected message_count=1, got %v", mc)
	}
}

func TestWindowMessages_SortsChronologically(t *testing.T) {
	c := &Connector{name: "test"}
	base := time.Date(2026, 4, 11, 10, 0, 0, 0, time.UTC)

	// Input in reverse-chrono (newest first), as Telegram returns
	records := []messageRecord{
		{ID: 3, Text: "third", Date: base.Add(10 * time.Minute)},
		{ID: 2, Text: "second", Date: base.Add(5 * time.Minute)},
		{ID: 1, Text: "first", Date: base},
	}
	docs, _ := c.windowMessages(records, "Test", "100", nil, 0, 0)
	if len(docs) != 1 {
		t.Fatalf("expected 1 window, got %d", len(docs))
	}
	// Output should be in chronological order
	expected := "first\nsecond\nthird"
	if docs[0].Content != expected {
		t.Errorf("expected content %q, got %q", expected, docs[0].Content)
	}
}

func TestWindowMessages_EmptyInput(t *testing.T) {
	c := &Connector{name: "test"}
	docs, _ := c.windowMessages(nil, "Test", "100", nil, 0, 0)
	if len(docs) != 0 {
		t.Errorf("expected 0 docs for empty input, got %d", len(docs))
	}
}

func TestWindowMessages_DocMetadata(t *testing.T) {
	c := &Connector{name: "test"}
	base := time.Date(2026, 4, 11, 10, 0, 0, 0, time.UTC)
	records := []messageRecord{
		{ID: 100, Text: "hi", Date: base},
		{ID: 101, Text: "there", Date: base.Add(1 * time.Minute)},
	}
	docs, _ := c.windowMessages(records, "Friends", "42", nil, 0, 0)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	d := docs[0]
	if d.SourceID != "42:100-101" {
		t.Errorf("source_id = %q, want '42:100-101'", d.SourceID)
	}
	if d.Title != "Friends" {
		t.Errorf("title = %q, want 'Friends'", d.Title)
	}
	if d.Metadata["chat_name"] != "Friends" {
		t.Errorf("chat_name = %v", d.Metadata["chat_name"])
	}
	if d.Metadata["first_message_id"] != 100 {
		t.Errorf("first_message_id = %v", d.Metadata["first_message_id"])
	}
	if d.Metadata["last_message_id"] != 101 {
		t.Errorf("last_message_id = %v", d.Metadata["last_message_id"])
	}
	// CreatedAt should be the latest message's date
	if !d.CreatedAt.Equal(base.Add(1 * time.Minute)) {
		t.Errorf("CreatedAt = %v, want latest message date", d.CreatedAt)
	}
}

// --- Media handling tests ---

// samplePhoto returns a minimally valid *tg.Photo with one PhotoSize
// large enough to be picked by largestPhotoSize.
func samplePhoto() *tg.Photo {
	return &tg.Photo{
		ID:            1001,
		AccessHash:    2002,
		FileReference: []byte{0xde, 0xad, 0xbe, 0xef},
		Sizes: []tg.PhotoSizeClass{
			&tg.PhotoStrippedSize{Type: "i", Bytes: []byte{0x00}}, // inline — skipped
			&tg.PhotoSize{Type: "m", W: 320, H: 320, Size: 1024},
			&tg.PhotoSize{Type: "x", W: 1280, H: 1280, Size: 65536},
		},
	}
}

// sampleDocument returns a *tg.Document with a filename attribute.
func sampleDocument() *tg.Document {
	return &tg.Document{
		ID:            3003,
		AccessHash:    4004,
		FileReference: []byte{0x01, 0x02},
		MimeType:      "application/pdf",
		Size:          2048,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "report.pdf"},
		},
	}
}

func TestLargestPhotoSize(t *testing.T) {
	t.Run("picks largest PhotoSize", func(t *testing.T) {
		typ, sz, ok := largestPhotoSize(samplePhoto().Sizes)
		if !ok {
			t.Fatal("expected ok")
		}
		if typ != "x" || sz != 65536 {
			t.Errorf("got (%q, %d), want (x, 65536)", typ, sz)
		}
	})
	t.Run("only inline sizes returns ok=false", func(t *testing.T) {
		sizes := []tg.PhotoSizeClass{
			&tg.PhotoStrippedSize{Type: "i"},
			&tg.PhotoPathSize{Type: "j"},
		}
		if _, _, ok := largestPhotoSize(sizes); ok {
			t.Error("expected ok=false for inline-only sizes")
		}
	})
	t.Run("progressive size", func(t *testing.T) {
		sizes := []tg.PhotoSizeClass{
			&tg.PhotoSizeProgressive{Type: "p", Sizes: []int{100, 500, 1500}},
		}
		typ, sz, ok := largestPhotoSize(sizes)
		if !ok || typ != "p" || sz != 1500 {
			t.Errorf("progressive: (%q, %d, %v), want (p, 1500, true)", typ, sz, ok)
		}
	})
	t.Run("empty slice", func(t *testing.T) {
		if _, _, ok := largestPhotoSize(nil); ok {
			t.Error("expected ok=false for empty sizes")
		}
	})
}

func TestDocumentFilename(t *testing.T) {
	t.Run("with filename attr", func(t *testing.T) {
		if got := documentFilename(sampleDocument()); got != "report.pdf" {
			t.Errorf("got %q, want report.pdf", got)
		}
	})
	t.Run("without filename attr", func(t *testing.T) {
		doc := &tg.Document{Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAudio{Duration: 30},
		}}
		if got := documentFilename(doc); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestMediaLocation(t *testing.T) {
	t.Run("photo", func(t *testing.T) {
		loc, mime, fn, size, ok := mediaLocation(&tg.MessageMediaPhoto{Photo: samplePhoto()})
		if !ok {
			t.Fatal("expected ok")
		}
		pl, isPhoto := loc.(*tg.InputPhotoFileLocation)
		if !isPhoto {
			t.Fatalf("expected *InputPhotoFileLocation, got %T", loc)
		}
		if pl.ID != 1001 || pl.AccessHash != 2002 || pl.ThumbSize != "x" {
			t.Errorf("photo loc = %+v", pl)
		}
		if mime != "image/jpeg" || fn != "" || size != 65536 {
			t.Errorf("sidecar (%q, %q, %d) mismatch", mime, fn, size)
		}
	})
	t.Run("document", func(t *testing.T) {
		loc, mime, fn, size, ok := mediaLocation(&tg.MessageMediaDocument{Document: sampleDocument()})
		if !ok {
			t.Fatal("expected ok")
		}
		dl, isDoc := loc.(*tg.InputDocumentFileLocation)
		if !isDoc {
			t.Fatalf("expected *InputDocumentFileLocation, got %T", loc)
		}
		if dl.ID != 3003 || dl.AccessHash != 4004 {
			t.Errorf("document loc = %+v", dl)
		}
		if mime != "application/pdf" || fn != "report.pdf" || size != 2048 {
			t.Errorf("sidecar (%q, %q, %d) mismatch", mime, fn, size)
		}
	})
	t.Run("empty photo", func(t *testing.T) {
		if _, _, _, _, ok := mediaLocation(&tg.MessageMediaPhoto{Photo: &tg.PhotoEmpty{}}); ok {
			t.Error("expected ok=false for PhotoEmpty")
		}
	})
	t.Run("unsupported kinds", func(t *testing.T) {
		cases := []tg.MessageMediaClass{
			&tg.MessageMediaEmpty{},
			&tg.MessageMediaGeo{},
			&tg.MessageMediaContact{},
			&tg.MessageMediaWebPage{},
			&tg.MessageMediaPoll{},
		}
		for _, m := range cases {
			if _, _, _, _, ok := mediaLocation(m); ok {
				t.Errorf("%T: expected ok=false", m)
			}
		}
	})
}

func TestMediaToDocument_Photo(t *testing.T) {
	now := time.Now()
	store := newFakeBinaryStore()
	c := &Connector{
		name:        "tg",
		binaryStore: store,
		cacheConfig: connector.CacheConfig{Mode: "eager"},
	}
	dl := &stubDownloader{payload: []byte("JPEG-bytes")}
	m := &tg.Message{
		ID:      42,
		Date:    int(now.Unix()),
		Message: "a caption",
		Media:   &tg.MessageMediaPhoto{Photo: samplePhoto()},
	}

	doc, ok := c.mediaToDocument(context.Background(), dl, m, "Friends", "99")
	if !ok {
		t.Fatal("expected ok")
	}
	if doc.SourceID != "99:42:media" {
		t.Errorf("source_id = %q", doc.SourceID)
	}
	if doc.SourceType != "telegram" || doc.SourceName != "tg" {
		t.Errorf("source type/name wrong: %+v", doc)
	}
	if doc.MimeType != "image/jpeg" {
		t.Errorf("mime = %q", doc.MimeType)
	}
	// Post-download size should equal downloaded byte count (photo
	// advertised 65536 but the stub returned 10 bytes — media doc
	// should reflect reality).
	if doc.Size != int64(len("JPEG-bytes")) {
		t.Errorf("size = %d, want %d", doc.Size, len("JPEG-bytes"))
	}
	if doc.Title != "photo-42.jpg" { // synthesized from msg ID
		t.Errorf("title = %q", doc.Title)
	}
	if doc.Content != "a caption" { // no extractor → caption
		t.Errorf("content = %q", doc.Content)
	}
	if doc.Metadata["caption"] != "a caption" {
		t.Errorf("caption = %v", doc.Metadata["caption"])
	}
	// The parent-message pointer lives on Relations (attachment_of),
	// not in metadata — the dropped parent_message_id key was
	// strictly duplicated by the typed edge.
	if len(doc.Relations) != 1 || doc.Relations[0].Type != "attachment_of" ||
		doc.Relations[0].TargetSourceID != "99:42:msg" {
		t.Errorf("expected attachment_of → 99:42:msg, got %+v", doc.Relations)
	}
	if store.puts.Load() != 1 {
		t.Errorf("expected 1 eager Put, got %d", store.puts.Load())
	}
	if got := store.blobs[store.key("telegram", "tg", "99:42:media")]; string(got) != "JPEG-bytes" {
		t.Errorf("cached bytes = %q", got)
	}
}

func TestMediaToDocument_Document_TitleFromFilename(t *testing.T) {
	c := &Connector{name: "tg"}
	dl := &stubDownloader{payload: []byte("%PDF-...")}
	m := &tg.Message{
		ID:    7,
		Date:  int(time.Now().Unix()),
		Media: &tg.MessageMediaDocument{Document: sampleDocument()},
	}

	doc, ok := c.mediaToDocument(context.Background(), dl, m, "Some Chat", "5")
	if !ok {
		t.Fatal("expected ok")
	}
	if doc.Title != "report.pdf" {
		t.Errorf("title = %q, want report.pdf", doc.Title)
	}
	if doc.Metadata["filename"] != "report.pdf" {
		t.Errorf("filename metadata = %v", doc.Metadata["filename"])
	}
	// No caption, no extractor → content is empty
	if doc.Content != "" {
		t.Errorf("content = %q, want empty", doc.Content)
	}
}

func TestMediaToDocument_DownloadFailureSkips(t *testing.T) {
	c := &Connector{name: "tg"}
	dl := &stubDownloader{err: errors.New("boom")}
	m := &tg.Message{
		ID: 1, Date: int(time.Now().Unix()),
		Media: &tg.MessageMediaPhoto{Photo: samplePhoto()},
	}
	if _, ok := c.mediaToDocument(context.Background(), dl, m, "Chat", "1"); ok {
		t.Error("expected ok=false on download failure")
	}
}

func TestMediaToDocument_UnsupportedSkips(t *testing.T) {
	c := &Connector{name: "tg"}
	dl := &stubDownloader{}
	m := &tg.Message{ID: 1, Date: int(time.Now().Unix()), Media: &tg.MessageMediaGeo{}}
	if _, ok := c.mediaToDocument(context.Background(), dl, m, "Chat", "1"); ok {
		t.Error("expected ok=false for unsupported media")
	}
	if dl.calls.Load() != 0 {
		t.Errorf("downloader should not be called for unsupported media, got %d", dl.calls.Load())
	}
}

func TestFetchChatMessages_CaptionedMedia_EmitsBothDocs(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{
						ID: 10, Date: now, Message: "look at this photo",
						Media: &tg.MessageMediaPhoto{Photo: samplePhoto()},
					},
				},
			},
		},
	}
	store := newFakeBinaryStore()
	c := &Connector{
		name:        "tg",
		binaryStore: store,
		cacheConfig: connector.CacheConfig{Mode: "eager"},
	}
	dl := &stubDownloader{payload: []byte("photo-bytes")}

	docs, err := c.fetchChatMessages(context.Background(), api, dl, &tg.InputPeerChat{ChatID: 1}, "Chat", "55", map[int64]*tg.User{}, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Dual emission: window + per-message + media.
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs (window + message + media), got %d", len(docs))
	}
	var window, message, media *model.Document
	for i := range docs {
		switch {
		case strings.HasSuffix(docs[i].SourceID, ":media"):
			media = &docs[i]
		case strings.HasSuffix(docs[i].SourceID, ":msg"):
			message = &docs[i]
		default:
			window = &docs[i]
		}
	}
	if window == nil || message == nil || media == nil {
		t.Fatalf("missing window (%v), message (%v), or media (%v) doc", window, message, media)
	}
	if !strings.Contains(window.Content, "look at this photo") {
		t.Errorf("window doc missing caption text: %q", window.Content)
	}
	if message.Content != "look at this photo" {
		t.Errorf("message doc content = %q, want caption text", message.Content)
	}
	if !message.Hidden {
		t.Error("message doc should be Hidden=true")
	}
	if media.Metadata["caption"] != "look at this photo" {
		t.Errorf("media caption metadata = %v", media.Metadata["caption"])
	}
	// media doc carries attachment_of pointing at the per-message doc, not the window
	if len(media.Relations) != 1 || media.Relations[0].Type != model.RelationAttachmentOf ||
		!strings.HasSuffix(media.Relations[0].TargetSourceID, ":10:msg") {
		t.Errorf("media attachment_of relation wrong: %+v", media.Relations)
	}
	if store.puts.Load() != 1 {
		t.Errorf("expected 1 cache Put, got %d", store.puts.Load())
	}
}

func TestFetchChatMessages_MediaOnlyMessage_EmitsMediaDoc(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{
						ID: 11, Date: now, Message: "",
						Media: &tg.MessageMediaDocument{Document: sampleDocument()},
					},
				},
			},
		},
	}
	c := &Connector{name: "tg"}
	dl := &stubDownloader{payload: []byte("pdf-bytes")}

	docs, err := c.fetchChatMessages(context.Background(), api, dl, &tg.InputPeerChat{ChatID: 1}, "Chat", "55", map[int64]*tg.User{}, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Media-only message: no window (no text record), message + media = 2 docs.
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs (message + media), got %d", len(docs))
	}
	var msgDoc, mediaDoc *model.Document
	for i := range docs {
		if strings.HasSuffix(docs[i].SourceID, ":media") {
			mediaDoc = &docs[i]
		} else if strings.HasSuffix(docs[i].SourceID, ":msg") {
			msgDoc = &docs[i]
		}
	}
	if msgDoc == nil || mediaDoc == nil {
		t.Fatalf("missing message (%v) or media (%v) doc", msgDoc, mediaDoc)
	}
	// The media-only message has no member_of_window edge (there's no
	// window containing it — windows are built from text-bearing records).
	for _, rel := range msgDoc.Relations {
		if rel.Type == model.RelationMemberOfWindow {
			t.Errorf("media-only message should have no member_of_window edge, got %+v", rel)
		}
	}
}

func TestFetchChatMessages_DownloadFailure_TextStillWindowed(t *testing.T) {
	now := int(time.Now().Unix())
	api := &mockTelegramAPI{
		msgList: []tg.MessagesMessagesClass{
			&tg.MessagesMessages{
				Messages: []tg.MessageClass{
					&tg.Message{
						ID: 1, Date: now, Message: "hey",
						Media: &tg.MessageMediaPhoto{Photo: samplePhoto()},
					},
				},
			},
		},
	}
	c := &Connector{name: "tg"}
	dl := &stubDownloader{err: errors.New("network")}

	docs, err := c.fetchChatMessages(context.Background(), api, dl, &tg.InputPeerChat{ChatID: 1}, "Chat", "55", map[int64]*tg.User{}, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Window + per-message doc; media is skipped because download failed.
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs (window + message, no media), got %d", len(docs))
	}
	for _, d := range docs {
		if strings.HasSuffix(d.SourceID, ":media") {
			t.Errorf("media doc should have been skipped on download failure, got %q", d.SourceID)
		}
	}
}

func TestMediaLocation_PhotoWithOnlyInlineSizes(t *testing.T) {
	// A photo whose Sizes slice contains only inline previews
	// (stripped/cached/path) is undownloadable — mediaLocation should
	// bail with ok=false rather than returning a broken location.
	photo := &tg.Photo{
		ID: 1, AccessHash: 2, FileReference: []byte{0x00},
		Sizes: []tg.PhotoSizeClass{
			&tg.PhotoStrippedSize{Type: "i"},
			&tg.PhotoPathSize{Type: "j"},
		},
	}
	if _, _, _, _, ok := mediaLocation(&tg.MessageMediaPhoto{Photo: photo}); ok {
		t.Error("expected ok=false when photo has only inline sizes")
	}
}

func TestMediaLocation_DocumentEmpty(t *testing.T) {
	if _, _, _, _, ok := mediaLocation(&tg.MessageMediaDocument{Document: &tg.DocumentEmpty{}}); ok {
		t.Error("expected ok=false for DocumentEmpty")
	}
}

func TestMediaToDocument_ExtractorConsumesContent(t *testing.T) {
	// A plain-text document should flow through the default PlainText
	// extractor so its bytes end up in the Document's Content field,
	// overriding the caption fallback.
	c := &Connector{
		name:      "tg",
		extractor: extractor.NewRegistry("", nil), // PlainText-only registry
	}
	dl := &stubDownloader{payload: []byte("the quick brown fox")}
	m := &tg.Message{
		ID:      1,
		Date:    int(time.Now().Unix()),
		Message: "caption that should NOT win over extracted text",
		Media: &tg.MessageMediaDocument{Document: &tg.Document{
			ID: 1, AccessHash: 2, FileReference: []byte{0},
			MimeType: "text/plain",
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: "note.txt"},
			},
		}},
	}
	doc, ok := c.mediaToDocument(context.Background(), dl, m, "Chat", "7")
	if !ok {
		t.Fatal("expected ok")
	}
	if doc.Content != "the quick brown fox" {
		t.Errorf("content = %q, want extracted text", doc.Content)
	}
}

func TestMediaToDocument_TitleFallsBackToChatName(t *testing.T) {
	// A document with no filename attribute and a non-photo mime
	// should fall back to the chat name for Title — the
	// photo-filename synthesis branch shouldn't trigger here.
	c := &Connector{name: "tg"}
	dl := &stubDownloader{payload: []byte("ogg-voice-note")}
	m := &tg.Message{
		ID:   42,
		Date: int(time.Now().Unix()),
		Media: &tg.MessageMediaDocument{Document: &tg.Document{
			ID: 1, AccessHash: 2, FileReference: []byte{0},
			MimeType:   "audio/ogg",
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true, Duration: 5}},
		}},
	}
	doc, ok := c.mediaToDocument(context.Background(), dl, m, "Voice Buddy", "8")
	if !ok {
		t.Fatal("expected ok")
	}
	if doc.Title != "Voice Buddy" {
		t.Errorf("title = %q, want chat name 'Voice Buddy'", doc.Title)
	}
}

func TestMediaLocation_SkipsStickers(t *testing.T) {
	doc := &tg.Document{
		ID: 1, AccessHash: 2, MimeType: "application/x-tgsticker",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "AnimatedSticker.tgs"},
			&tg.DocumentAttributeSticker{Alt: "🙂"},
		},
	}
	if _, _, _, _, ok := mediaLocation(&tg.MessageMediaDocument{Document: doc}); ok {
		t.Error("expected ok=false for sticker documents")
	}
}

func TestMediaLocation_KeepsAnimatedGIF(t *testing.T) {
	// GIFs carry DocumentAttributeAnimated but no DocumentAttributeSticker —
	// they should still be indexed.
	doc := &tg.Document{
		ID: 5, AccessHash: 6, MimeType: "video/mp4",
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAnimated{},
			&tg.DocumentAttributeFilename{FileName: "funny.mp4"},
		},
	}
	if _, _, _, _, ok := mediaLocation(&tg.MessageMediaDocument{Document: doc}); !ok {
		t.Error("expected ok=true for GIF/animated (non-sticker) documents")
	}
}

func TestMediaToDocument_Photo_SynthesizesFilename(t *testing.T) {
	c := &Connector{name: "tg"}
	dl := &stubDownloader{payload: []byte("jpg")}
	m := &tg.Message{
		ID: 4242, Date: int(time.Now().Unix()),
		Media: &tg.MessageMediaPhoto{Photo: samplePhoto()},
	}
	doc, ok := c.mediaToDocument(context.Background(), dl, m, "Maria Pavlova", "99")
	if !ok {
		t.Fatal("expected ok")
	}
	if doc.Title != "photo-4242.jpg" {
		t.Errorf("title = %q, want photo-4242.jpg", doc.Title)
	}
	if doc.Metadata["filename"] != "photo-4242.jpg" {
		t.Errorf("filename metadata = %v", doc.Metadata["filename"])
	}
}

func TestMakeMessageDoc_ReplyAndSender(t *testing.T) {
	c := &Connector{name: "tg"}
	base := time.Date(2026, 4, 11, 10, 0, 0, 0, time.UTC)

	t.Run("same-chat reply emits reply_to relation", func(t *testing.T) {
		m := &tg.Message{
			ID: 500, Date: int(base.Unix()), Message: "ok",
			FromID:  &tg.PeerUser{UserID: 42},
			ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 499},
		}
		doc := c.makeMessageDoc(m, "Chat", "10", "10:499-500", nil, 0, 0)
		// Expect both member_of_window and reply_to relations.
		kinds := []string{}
		for _, r := range doc.Relations {
			kinds = append(kinds, r.Type)
		}
		sort.Strings(kinds)
		if !reflect.DeepEqual(kinds, []string{"member_of_window", "reply_to"}) {
			t.Errorf("relation types = %v, want member_of_window + reply_to", kinds)
		}
		if doc.Metadata["sender_id"] != int64(42) {
			t.Errorf("sender_id = %v, want 42", doc.Metadata["sender_id"])
		}
	})

	crossChatCases := []struct {
		name       string
		peer       tg.PeerClass
		wantTarget string
	}{
		{"channel peer", &tg.PeerChannel{ChannelID: 999}, "999:499:msg"},
		{"chat peer", &tg.PeerChat{ChatID: 777}, "777:499:msg"},
		{"user peer", &tg.PeerUser{UserID: 42}, "42:499:msg"},
	}
	for _, tc := range crossChatCases {
		t.Run("cross-chat reply resolves target: "+tc.name, func(t *testing.T) {
			m := &tg.Message{
				ID: 500, Date: int(base.Unix()),
				ReplyTo: &tg.MessageReplyHeader{
					ReplyToMsgID:  499,
					ReplyToPeerID: tc.peer,
				},
			}
			doc := c.makeMessageDoc(m, "Chat", "10", "", nil, 0, 0)
			var got *model.Relation
			for i, r := range doc.Relations {
				if r.Type == model.RelationReplyTo {
					got = &doc.Relations[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("expected reply_to relation, got none")
			}
			if got.TargetSourceID != tc.wantTarget {
				t.Errorf("TargetSourceID = %q, want %q", got.TargetSourceID, tc.wantTarget)
			}
		})
	}

	t.Run("no FromID leaves sender_id unset", func(t *testing.T) {
		m := &tg.Message{ID: 1, Date: int(base.Unix()), Message: "x"}
		doc := c.makeMessageDoc(m, "Chat", "10", "", nil, 0, 0)
		if _, ok := doc.Metadata["sender_id"]; ok {
			t.Errorf("sender_id should be unset when FromID is nil")
		}
	})

	t.Run("no window means no member_of_window edge", func(t *testing.T) {
		m := &tg.Message{ID: 1, Date: int(base.Unix())}
		doc := c.makeMessageDoc(m, "Chat", "10", "", nil, 0, 0)
		for _, r := range doc.Relations {
			if r.Type == model.RelationMemberOfWindow {
				t.Error("should have no member_of_window when windowSourceID is empty")
			}
		}
	})

	t.Run("DM outgoing with nil FromID attributes to selfID", func(t *testing.T) {
		m := &tg.Message{ID: 20, Date: int(base.Unix()), Message: "hi", Out: true}
		userMap := map[int64]*tg.User{
			7001: {ID: 7001, FirstName: "Me", Username: "me_u"},
		}
		doc := c.makeMessageDoc(m, "DM Peer", "8888", "", userMap, 7001, 8888)
		if doc.Metadata["sender_id"] != int64(7001) {
			t.Errorf("sender_id = %v, want 7001 (self)", doc.Metadata["sender_id"])
		}
		if name, _ := doc.Metadata["sender_name"].(string); name != "Me" {
			t.Errorf("sender_name = %q, want Me", name)
		}
	})

	t.Run("DM incoming with nil FromID attributes to dmPeerID", func(t *testing.T) {
		m := &tg.Message{ID: 21, Date: int(base.Unix()), Message: "hey", Out: false}
		userMap := map[int64]*tg.User{
			8888: {ID: 8888, FirstName: "Maria"},
		}
		doc := c.makeMessageDoc(m, "DM Peer", "8888", "", userMap, 7001, 8888)
		if doc.Metadata["sender_id"] != int64(8888) {
			t.Errorf("sender_id = %v, want 8888 (dm peer)", doc.Metadata["sender_id"])
		}
		if name, _ := doc.Metadata["sender_name"].(string); name != "Maria" {
			t.Errorf("sender_name = %q, want Maria", name)
		}
	})

	t.Run("group chat with nil FromID and no dm context stays anonymous", func(t *testing.T) {
		m := &tg.Message{ID: 22, Date: int(base.Unix()), Message: "x"}
		doc := c.makeMessageDoc(m, "Group", "10", "", nil, 0, 0)
		if _, ok := doc.Metadata["sender_id"]; ok {
			t.Errorf("sender_id should not be set without FromID or DM context")
		}
	})

	t.Run("user map populates sender_name and sender_username", func(t *testing.T) {
		m := &tg.Message{
			ID: 7, Date: int(base.Unix()), Message: "hi",
			FromID: &tg.PeerUser{UserID: 42},
		}
		userMap := map[int64]*tg.User{
			42: {ID: 42, FirstName: "Alice", LastName: "Kim", Username: "alice_k"},
		}
		doc := c.makeMessageDoc(m, "Chat", "10", "", userMap, 0, 0)
		if name, _ := doc.Metadata["sender_name"].(string); name != "Alice Kim" {
			t.Errorf("sender_name = %q, want Alice Kim", name)
		}
		if uname, _ := doc.Metadata["sender_username"].(string); uname != "alice_k" {
			t.Errorf("sender_username = %q, want alice_k", uname)
		}
	})

	t.Run("unknown sender falls back to plain sender_id", func(t *testing.T) {
		m := &tg.Message{
			ID: 8, Date: int(base.Unix()), Message: "hi",
			FromID: &tg.PeerUser{UserID: 99},
		}
		doc := c.makeMessageDoc(m, "Chat", "10", "", map[int64]*tg.User{}, 0, 0)
		if _, ok := doc.Metadata["sender_name"]; ok {
			t.Errorf("sender_name should be unset when user not in map")
		}
		if doc.Metadata["sender_id"] != int64(99) {
			t.Errorf("sender_id = %v, want 99", doc.Metadata["sender_id"])
		}
	})

	t.Run("sender_avatar_key set only when user has a profile photo", func(t *testing.T) {
		m := &tg.Message{
			ID: 9, Date: int(base.Unix()), Message: "hi",
			FromID: &tg.PeerUser{UserID: 42},
		}
		userMap := map[int64]*tg.User{
			42: {ID: 42, FirstName: "Alice", Photo: &tg.UserProfilePhoto{PhotoID: 5555}},
		}
		doc := c.makeMessageDoc(m, "Chat", "10", "", userMap, 0, 0)
		if got, _ := doc.Metadata["sender_avatar_key"].(string); got != AvatarSourceID(42) {
			t.Errorf("sender_avatar_key = %q, want %q", got, AvatarSourceID(42))
		}

		userMap[42] = &tg.User{ID: 42, FirstName: "Alice"}
		doc2 := c.makeMessageDoc(m, "Chat", "10", "", userMap, 0, 0)
		if _, ok := doc2.Metadata["sender_avatar_key"]; ok {
			t.Errorf("sender_avatar_key should be unset when no profile photo")
		}
	})
}

func TestMakeWindowDoc_EmitsAnchorCreatedAt(t *testing.T) {
	c := &Connector{name: "tg"}
	base := time.Date(2026, 4, 11, 10, 0, 0, 0, time.UTC)
	records := []messageRecord{
		{ID: 1, Text: "hi", Date: base},
		{ID: 2, Text: "there", Date: base.Add(time.Minute)},
	}
	docs, _ := c.windowMessages(records, "Chat", "10", nil, 0, 0)
	if len(docs) != 1 {
		t.Fatalf("expected 1 window doc, got %d", len(docs))
	}
	meta := docs[0].Metadata
	if got, _ := meta["anchor_message_id"].(int); got != 1 {
		t.Errorf("anchor_message_id = %v, want 1", meta["anchor_message_id"])
	}
	if got, _ := meta["anchor_created_at"].(string); got != base.Format(time.RFC3339) {
		t.Errorf("anchor_created_at = %q, want %q", got, base.Format(time.RFC3339))
	}
}

func TestMakeWindowDoc_EmitsMessageLines(t *testing.T) {
	c := &Connector{name: "tg"}
	base := time.Date(2026, 4, 11, 10, 0, 0, 0, time.UTC)

	records := []messageRecord{
		{ID: 1, Text: "hi", Date: base, FromID: &tg.PeerUser{UserID: 42}},
		{ID: 2, Text: "you too", Date: base.Add(time.Minute), Out: true},
		{ID: 3, Text: "ok", Date: base.Add(2 * time.Minute)},
	}
	userMap := map[int64]*tg.User{
		42:   {ID: 42, FirstName: "Alice", Username: "alice_k", Photo: &tg.UserProfilePhoto{PhotoID: 1}},
		7001: {ID: 7001, FirstName: "Me"},
		8888: {ID: 8888, FirstName: "Maria"},
	}

	docs, _ := c.windowMessages(records, "Chat", "10", userMap, 7001, 8888)
	if len(docs) != 1 {
		t.Fatalf("expected 1 window doc, got %d", len(docs))
	}
	lines, ok := docs[0].Metadata["message_lines"].([]map[string]any)
	if !ok {
		t.Fatalf("message_lines missing or wrong type: %T", docs[0].Metadata["message_lines"])
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if name := lines[0]["sender_name"]; name != "Alice" {
		t.Errorf("line[0].sender_name = %v, want Alice", name)
	}
	if key, _ := lines[0]["sender_avatar_key"].(string); key != AvatarSourceID(42) {
		t.Errorf("line[0].sender_avatar_key = %q, want %s", key, AvatarSourceID(42))
	}
	if sid, _ := lines[1]["sender_id"].(int64); sid != 7001 {
		t.Errorf("line[1].sender_id = %v, want 7001 (self)", sid)
	}
	if sid, _ := lines[2]["sender_id"].(int64); sid != 8888 {
		t.Errorf("line[2].sender_id = %v, want 8888 (dm peer)", sid)
	}
	if lines[0]["text"] != "hi" {
		t.Errorf("line[0].text = %v, want hi", lines[0]["text"])
	}
	if lines[0]["id"] != 1 {
		t.Errorf("line[0].id = %v, want 1", lines[0]["id"])
	}
}

func TestExtractHistoryUsers(t *testing.T) {
	u := []tg.UserClass{&tg.User{ID: 1}, &tg.User{ID: 2}}

	cases := []struct {
		name string
		in   tg.MessagesMessagesClass
		want int
	}{
		{"MessagesMessages", &tg.MessagesMessages{Users: u}, 2},
		{"MessagesMessagesSlice", &tg.MessagesMessagesSlice{Users: u}, 2},
		{"MessagesChannelMessages", &tg.MessagesChannelMessages{Users: u}, 2},
		{"unsupported type returns nil", &tg.MessagesMessagesNotModified{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractHistoryUsers(tc.in)
			if len(got) != tc.want {
				t.Errorf("len = %d, want %d", len(got), tc.want)
			}
		})
	}
}

func TestPeerChatID(t *testing.T) {
	cases := []struct {
		name   string
		in     tg.PeerClass
		want   string
		wantOK bool
	}{
		{"user", &tg.PeerUser{UserID: 42}, "42", true},
		{"chat", &tg.PeerChat{ChatID: 100}, "100", true},
		{"channel", &tg.PeerChannel{ChannelID: 500}, "500", true},
		{"unsupported", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := peerChatID(tc.in)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("value = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildUserMap_FiltersNonUsers(t *testing.T) {
	users := []tg.UserClass{
		&tg.User{ID: 1, FirstName: "A"},
		&tg.UserEmpty{ID: 2},
		&tg.User{ID: 3, FirstName: "B"},
	}
	got := buildUserMap(users)
	if len(got) != 2 {
		t.Fatalf("expected 2 users (empty filtered), got %d: %+v", len(got), got)
	}
	if _, ok := got[2]; ok {
		t.Errorf("UserEmpty should not appear in map")
	}
}

func TestHasDownloadableAvatar(t *testing.T) {
	cases := []struct {
		name string
		u    *tg.User
		want bool
	}{
		{"nil user", nil, false},
		{"no photo field", &tg.User{ID: 1}, false},
		{"photo with id=0 is empty placeholder", &tg.User{ID: 1, Photo: &tg.UserProfilePhoto{PhotoID: 0}}, false},
		{"photo with non-zero id is downloadable", &tg.User{ID: 1, Photo: &tg.UserProfilePhoto{PhotoID: 5}}, true},
		{"empty UserProfilePhoto type is not downloadable", &tg.User{ID: 1, Photo: &tg.UserProfilePhotoEmpty{}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasDownloadableAvatar(tc.u); got != tc.want {
				t.Errorf("hasDownloadableAvatar = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAvatarSourceID_IsStable(t *testing.T) {
	if AvatarSourceID(42) != "avatars:42" {
		t.Errorf("unexpected format: %q", AvatarSourceID(42))
	}
	if AvatarSourceID(-1) != "avatars:-1" {
		t.Errorf("negative ids still encode as-is: %q", AvatarSourceID(-1))
	}
}

type stubBinaryStore struct {
	existsMap map[string]bool
	puts      []stubPut
	existsErr error
	putErr    error
}

type stubPut struct {
	sourceType, sourceName, sourceID string
	size                             int64
}

func (s *stubBinaryStore) Put(ctx context.Context, sourceType, sourceName, sourceID string, r io.Reader, size int64) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.puts = append(s.puts, stubPut{sourceType, sourceName, sourceID, size})
	if s.existsMap == nil {
		s.existsMap = map[string]bool{}
	}
	s.existsMap[sourceType+"/"+sourceName+"/"+sourceID] = true
	_, _ = io.Copy(io.Discard, r)
	return nil
}

func (s *stubBinaryStore) Get(ctx context.Context, sourceType, sourceName, sourceID string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (s *stubBinaryStore) Exists(ctx context.Context, sourceType, sourceName, sourceID string) (bool, error) {
	if s.existsErr != nil {
		return false, s.existsErr
	}
	return s.existsMap[sourceType+"/"+sourceName+"/"+sourceID], nil
}

func TestEnsureAvatarCached(t *testing.T) {
	ctx := context.Background()
	user := &tg.User{
		ID: 42, AccessHash: 100,
		Photo: &tg.UserProfilePhoto{PhotoID: 5555},
	}

	t.Run("no-op without binary store", func(t *testing.T) {
		c := &Connector{name: "tg"}
		c.ensureAvatarCached(ctx, &stubDownloader{}, user)
	})

	t.Run("no-op when cache mode is not eager", func(t *testing.T) {
		store := &stubBinaryStore{}
		c := &Connector{name: "tg", binaryStore: store, cacheConfig: connector.CacheConfig{Mode: "lazy"}}
		c.ensureAvatarCached(ctx, &stubDownloader{}, user)
		if len(store.puts) != 0 {
			t.Errorf("should not have written anything in lazy mode")
		}
	})

	t.Run("skips users without a photo", func(t *testing.T) {
		store := &stubBinaryStore{}
		c := &Connector{name: "tg", binaryStore: store, cacheConfig: connector.CacheConfig{Mode: "eager"}}
		c.ensureAvatarCached(ctx, &stubDownloader{}, &tg.User{ID: 99})
		if len(store.puts) != 0 {
			t.Errorf("should not have fetched a photoless user")
		}
	})

	t.Run("skips users already cached", func(t *testing.T) {
		store := &stubBinaryStore{existsMap: map[string]bool{
			"telegram/tg/" + AvatarSourceID(42): true,
		}}
		c := &Connector{name: "tg", binaryStore: store, cacheConfig: connector.CacheConfig{Mode: "eager"}}
		c.ensureAvatarCached(ctx, &stubDownloader{}, user)
		if len(store.puts) != 0 {
			t.Errorf("should not re-fetch a cached avatar")
		}
	})

	t.Run("downloads and stores when missing", func(t *testing.T) {
		store := &stubBinaryStore{}
		c := &Connector{name: "tg", binaryStore: store, cacheConfig: connector.CacheConfig{Mode: "eager"}}
		c.ensureAvatarCached(ctx, &stubDownloader{payload: []byte("jpeg-bytes")}, user)
		if len(store.puts) != 1 {
			t.Fatalf("expected 1 put, got %d", len(store.puts))
		}
		if store.puts[0].sourceID != AvatarSourceID(42) {
			t.Errorf("wrong sourceID: %s", store.puts[0].sourceID)
		}
	})

	t.Run("silent on download failure", func(t *testing.T) {
		store := &stubBinaryStore{}
		c := &Connector{name: "tg", binaryStore: store, cacheConfig: connector.CacheConfig{Mode: "eager"}}
		c.ensureAvatarCached(ctx, &stubDownloader{err: errors.New("boom")}, user)
		if len(store.puts) != 0 {
			t.Errorf("should not have written after download failure")
		}
	})
}

func TestDisplayName_FallbackOrder(t *testing.T) {
	cases := []struct {
		name string
		u    *tg.User
		want string
	}{
		{"nil returns empty", nil, ""},
		{"first+last", &tg.User{FirstName: "Alice", LastName: "Kim"}, "Alice Kim"},
		{"first only", &tg.User{FirstName: "Alice"}, "Alice"},
		{"username", &tg.User{Username: "alice_k"}, "alice_k"},
		{"id fallback", &tg.User{ID: 42}, "User 42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DisplayName(tc.u)
			if got != tc.want {
				t.Errorf("DisplayName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetExtractor_AndBinaryStore(t *testing.T) {
	c := &Connector{}
	c.SetExtractor(nil) // just verify it accepts nil without panic
	if c.extractor != nil {
		t.Error("expected nil extractor after SetExtractor(nil)")
	}
	store := newFakeBinaryStore()
	cfg := connector.CacheConfig{Mode: "eager"}
	c.SetBinaryStore(store, cfg)
	if c.binaryStore != store {
		t.Error("expected binaryStore to be set")
	}
	if c.cacheConfig.Mode != "eager" {
		t.Error("expected cacheConfig to be stored")
	}
}
