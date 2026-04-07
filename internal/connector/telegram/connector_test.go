package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
)

// mockTelegramAPI implements telegramAPI for testing.
type mockTelegramAPI struct {
	dialogs tg.MessagesDialogsClass
	msgList []tg.MessagesMessagesClass // returned in order
	msgIdx  int
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

	docs, err := c.processDialogs(context.Background(), api, chats, nil, 0)
	if err != nil {
		t.Fatalf("processDialogs failed: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}
	if docs[0].Content != "Hello group!" {
		t.Errorf("expected 'Hello group!', got %q", docs[0].Content)
	}
	if docs[0].SourceType != "telegram" {
		t.Errorf("expected source_type 'telegram', got %q", docs[0].SourceType)
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

	docs, err := c.processDialogs(context.Background(), api, chats, nil, 0)
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

	docs, err := c.processDialogs(context.Background(), api, nil, users, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
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

	docs, err := c.processDialogs(context.Background(), api, nil, users, 0)
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

	docs, err := c.fetchChatMessages(context.Background(), api, inputPeer, "Test", "123", sinceDate)
	if err != nil {
		t.Fatal(err)
	}
	// Should only get the recent message, old one is before sinceDate
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc (filtered by date), got %d", len(docs))
	}
	if docs[0].Content != "Recent" {
		t.Errorf("expected 'Recent', got %q", docs[0].Content)
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
	docs, err := c.fetchChatMessages(context.Background(), api, &tg.InputPeerChat{ChatID: 1}, "Chat", "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Errorf("expected 1 doc (skipping empty/service), got %d", len(docs))
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
	docs, err := c.fetchWithAPI(context.Background(), api, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
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
	docs, err := c.fetchWithAPI(context.Background(), api, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
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
	docs, err := c.fetchWithAPI(context.Background(), api, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
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

	docs, err := c.processDialogs(context.Background(), api, chats, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc from channel, got %d", len(docs))
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
	if chatToInputPeer(channel) == nil {
		t.Error("expected non-nil channel peer")
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
