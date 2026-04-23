package imap

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/testutil"
)

func dialInsecure(address string, _ *imapclient.Options) (*imapclient.Client, error) {
	return imapclient.DialInsecure(address, nil)
}

func TestLive_Validate_Success(t *testing.T) {
	addr, cleanup := startFakeIMAPServer(
		map[string][]fakeMessage{"INBOX": {}},
		"user@test.com", "secret",
	)
	defer cleanup()

	c := &Connector{
		server: addr[:strings.LastIndex(addr, ":")],
		port: func() int {
			var p int
			for _, ch := range addr[strings.LastIndex(addr, ":")+1:] {
				p = p*10 + int(ch-'0')
			}
			return p
		}(),
		username: "user@test.com",
		password: "secret",
		dial:     dialInsecure,
	}

	if err := c.Validate(); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
}

func TestLive_Validate_BadPassword(t *testing.T) {
	addr, cleanup := startFakeIMAPServer(
		map[string][]fakeMessage{"INBOX": {}},
		"user@test.com", "secret",
	)
	defer cleanup()

	host, port := parseAddr(addr)
	c := &Connector{
		server:   host,
		port:     port,
		username: "user@test.com",
		password: "wrong",
		dial:     dialInsecure,
	}

	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("error = %v, want 'authentication failed'", err)
	}
}

func TestLive_Fetch_FullSync(t *testing.T) {
	messages := map[string][]fakeMessage{
		"INBOX": {
			{
				uid:  1,
				date: time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC),
				envelope: &imap.Envelope{
					Subject:   "First email",
					Date:      time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC),
					MessageID: "msg1@test.com",
					From:      []imap.Address{{Name: "Alice", Mailbox: "alice", Host: "test.com"}},
					To:        []imap.Address{{Mailbox: "bob", Host: "test.com"}},
				},
				body: buildTestEmail("Hello from email one"),
			},
			{
				uid:  2,
				date: time.Date(2026, 1, 11, 10, 0, 0, 0, time.UTC),
				envelope: &imap.Envelope{
					Subject:   "Second email",
					Date:      time.Date(2026, 1, 11, 10, 0, 0, 0, time.UTC),
					MessageID: "msg2@test.com",
					From:      []imap.Address{{Mailbox: "charlie", Host: "test.com"}},
				},
				body: buildTestEmail("Hello from email two"),
			},
		},
	}

	addr, cleanup := startFakeIMAPServer(messages, "user@test.com", "secret")
	defer cleanup()

	host, port := parseAddr(addr)
	c := &Connector{
		name:     "test-imap",
		server:   host,
		port:     port,
		username: "user@test.com",
		password: "secret",
		folders:  []string{"INBOX"},
		dial:     dialInsecure,
	}

	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatalf("Fetch failed: %v", result.Err)
	}

	if len(result.Documents) != 2 {
		t.Fatalf("got %d docs, want 2", len(result.Documents))
	}

	doc1 := result.Documents[0]
	if doc1.Title != "First email" {
		t.Errorf("doc1.Title = %q, want %q", doc1.Title, "First email")
	}
	if !strings.Contains(doc1.Content, "Hello from email one") {
		t.Errorf("doc1.Content = %q, want to contain email body", doc1.Content)
	}
	if doc1.SourceID != "INBOX:1" {
		t.Errorf("doc1.SourceID = %q, want %q", doc1.SourceID, "INBOX:1")
	}
	if doc1.URL != "mid:msg1@test.com" {
		t.Errorf("doc1.URL = %q, want %q", doc1.URL, "mid:msg1@test.com")
	}

	if result.LastCursor == nil {
		t.Fatal("cursor is nil")
	}
	uidVal, ok := result.LastCursor.CursorData["uid:INBOX"].(float64)
	if !ok || imap.UID(uidVal) != 2 {
		t.Errorf("cursor uid:INBOX = %v, want 2", result.LastCursor.CursorData["uid:INBOX"])
	}
}

func TestLive_Fetch_IncrementalSync(t *testing.T) {
	messages := map[string][]fakeMessage{
		"INBOX": {
			{
				uid:  1,
				date: time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC),
				envelope: &imap.Envelope{
					Subject: "Old email", Date: time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC),
					MessageID: "old@test.com",
				},
				body: buildTestEmail("Old content"),
			},
			{
				uid:  5,
				date: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
				envelope: &imap.Envelope{
					Subject: "New email", Date: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
					MessageID: "new@test.com",
				},
				body: buildTestEmail("New content"),
			},
		},
	}

	addr, cleanup := startFakeIMAPServer(messages, "user@test.com", "secret")
	defer cleanup()

	host, port := parseAddr(addr)
	c := &Connector{
		name:     "test-imap",
		server:   host,
		port:     port,
		username: "user@test.com",
		password: "secret",
		folders:  []string{"INBOX"},
		dial:     dialInsecure,
	}

	cursor := &model.SyncCursor{
		CursorData: map[string]any{"uid:INBOX": float64(1)},
	}

	result := testutil.RunFetch(t, c, cursor)
	if result.Err != nil {
		t.Fatalf("Fetch failed: %v", result.Err)
	}

	if len(result.Documents) != 1 {
		t.Fatalf("got %d docs, want 1 (incremental)", len(result.Documents))
	}
	if result.Documents[0].Title != "New email" {
		t.Errorf("doc.Title = %q, want %q", result.Documents[0].Title, "New email")
	}

	uidVal, ok := result.LastCursor.CursorData["uid:INBOX"].(float64)
	if !ok || imap.UID(uidVal) != 5 {
		t.Errorf("cursor uid:INBOX = %v, want 5", result.LastCursor.CursorData["uid:INBOX"])
	}
}

func TestLive_Fetch_MultipleFolders(t *testing.T) {
	messages := map[string][]fakeMessage{
		"INBOX": {
			{
				uid:  1,
				date: time.Now(),
				envelope: &imap.Envelope{
					Subject: "Inbox msg", Date: time.Now(), MessageID: "inbox@test.com",
				},
				body: buildTestEmail("Inbox body"),
			},
		},
		"Sent": {
			{
				uid:  1,
				date: time.Now(),
				envelope: &imap.Envelope{
					Subject: "Sent msg", Date: time.Now(), MessageID: "sent@test.com",
				},
				body: buildTestEmail("Sent body"),
			},
		},
	}

	addr, cleanup := startFakeIMAPServer(messages, "user@test.com", "secret")
	defer cleanup()

	host, port := parseAddr(addr)
	c := &Connector{
		name:     "test-imap",
		server:   host,
		port:     port,
		username: "user@test.com",
		password: "secret",
		folders:  []string{"INBOX", "Sent"},
		dial:     dialInsecure,
	}

	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatalf("Fetch failed: %v", result.Err)
	}

	if len(result.Documents) != 2 {
		t.Fatalf("got %d docs, want 2", len(result.Documents))
	}

	sourceIDs := map[string]bool{}
	for _, doc := range result.Documents {
		sourceIDs[doc.SourceID] = true
	}
	if !sourceIDs["INBOX:1"] || !sourceIDs["Sent:1"] {
		t.Errorf("sourceIDs = %v, want INBOX:1 and Sent:1", sourceIDs)
	}

	if _, ok := result.LastCursor.CursorData["uid:INBOX"]; !ok {
		t.Error("missing cursor for INBOX")
	}
	if _, ok := result.LastCursor.CursorData["uid:Sent"]; !ok {
		t.Error("missing cursor for Sent")
	}
}

func TestLive_Fetch_EmptyMailbox(t *testing.T) {
	messages := map[string][]fakeMessage{
		"INBOX": {},
	}

	addr, cleanup := startFakeIMAPServer(messages, "user@test.com", "secret")
	defer cleanup()

	host, port := parseAddr(addr)
	c := &Connector{
		name: "test-imap", server: host, port: port,
		username: "user@test.com", password: "secret",
		folders: []string{"INBOX"}, dial: dialInsecure,
	}

	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatalf("Fetch failed: %v", result.Err)
	}
	if len(result.Documents) != 0 {
		t.Errorf("got %d docs, want 0", len(result.Documents))
	}
}

func TestLive_Fetch_WithSyncSince(t *testing.T) {
	messages := map[string][]fakeMessage{
		"INBOX": {
			{
				uid:  1,
				date: time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC),
				envelope: &imap.Envelope{
					Subject: "Old", Date: time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC),
					MessageID: "old@test.com",
				},
				body: buildTestEmail("Old"),
			},
			{
				uid:  2,
				date: time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC),
				envelope: &imap.Envelope{
					Subject: "Recent", Date: time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC),
					MessageID: "recent@test.com",
				},
				body: buildTestEmail("Recent"),
			},
		},
	}

	addr, cleanup := startFakeIMAPServer(messages, "user@test.com", "secret")
	defer cleanup()

	host, port := parseAddr(addr)
	c := &Connector{
		name: "test-imap", server: host, port: port,
		username: "user@test.com", password: "secret",
		folders: []string{"INBOX"}, dial: dialInsecure,
		syncSince: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	result := testutil.RunFetch(t, c, nil)
	if result.Err != nil {
		t.Fatalf("Fetch failed: %v", result.Err)
	}

	if len(result.Documents) != 1 {
		t.Fatalf("got %d docs, want 1", len(result.Documents))
	}
	if result.Documents[0].Title != "Recent" {
		t.Errorf("doc.Title = %q, want %q", result.Documents[0].Title, "Recent")
	}
}

// parseAddr splits "host:port" into components.
func parseAddr(addr string) (string, int) {
	idx := strings.LastIndex(addr, ":")
	host := addr[:idx]
	var port int
	for _, ch := range addr[idx+1:] {
		port = port*10 + int(ch-'0')
	}
	return host, port
}
