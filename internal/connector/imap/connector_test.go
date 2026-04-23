package imap

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/pipeline/extractor"
	"github.com/muty/nexus/internal/testutil"
)

// runStreamFetch drives c.streamFetchWithClient against a mock and
// returns the collected stream. Used by the per-folder streaming
// tests below — exercises the real streaming code path without
// needing a live IMAP dial/login.
func runStreamFetch(t *testing.T, c *Connector, mbc mailboxClient, cursor *model.SyncCursor) *testutil.StreamResult {
	t.Helper()
	return runStreamFetchCtx(t, c, mbc, cursor, context.Background())
}

func runStreamFetchCtx(t *testing.T, c *Connector, mbc mailboxClient, cursor *model.SyncCursor, ctx context.Context) *testutil.StreamResult {
	t.Helper()
	items := make(chan model.FetchItem)
	errs := make(chan error, 1)
	go func() {
		defer close(items)
		defer close(errs)
		if err := c.streamFetchWithClient(ctx, mbc, cursor, items); err != nil {
			errs <- err
		}
	}()
	return testutil.CollectStream(t, items, errs)
}

// --- Mock mailbox client ---

type mockMailboxClient struct {
	selectErr error
	// selectData controls the (UIDValidity, HighestModSeq) returned
	// by SelectFolder. Zero-valued by default, which means the
	// connector falls back to UID-range delta since CONDSTORE looks
	// unavailable. Tests that want to exercise CONDSTORE fast-skip
	// or delta paths set these fields explicitly.
	selectData imap.SelectData
	uids       []imap.UID
	searchErr  error
	msgs       []*imapclient.FetchMessageBuffer
	fetchErr   error

	// failOnAllSearch, when true, returns an error from the *first*
	// (no-criteria) SearchUIDs call — the deletion-sync enumeration
	// pass — while leaving the second cursor-filtered call working.
	// Lets tests exercise the "enumeration failed → opt out of
	// deletion sync" behavior in isolation.
	failOnAllSearch bool
	searchCallCount int
}

func (m *mockMailboxClient) SelectFolder(_ string, _ bool) (*imap.SelectData, error) {
	if m.selectErr != nil {
		return nil, m.selectErr
	}
	d := m.selectData
	return &d, nil
}

func (m *mockMailboxClient) SearchUIDs(criteria *imap.SearchCriteria) ([]imap.UID, error) {
	m.searchCallCount++
	if m.failOnAllSearch && criteria != nil && len(criteria.UID) == 0 && criteria.Since.IsZero() && criteria.ModSeq == nil {
		return nil, fmt.Errorf("simulated SEARCH ALL failure")
	}
	return m.uids, m.searchErr
}

func (m *mockMailboxClient) FetchMessages(_ []imap.UID, _ *imap.FetchOptions, yield func(*imapclient.FetchMessageBuffer) bool) error {
	if m.fetchErr != nil {
		return m.fetchErr
	}
	for _, msg := range m.msgs {
		if !yield(msg) {
			return nil
		}
	}
	return nil
}

// --- Configure tests ---

func TestConfigure_Valid(t *testing.T) {
	c := &Connector{port: 993, folders: []string{"INBOX"}}
	err := c.Configure(connector.Config{
		"name":     "my-mail",
		"server":   "imap.mail.me.com",
		"username": "user@icloud.com",
		"password": "app-specific-pass",
		"folders":  "INBOX,Sent",
		"port":     "993",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.name != "my-mail" {
		t.Errorf("name = %q, want %q", c.name, "my-mail")
	}
	if c.server != "imap.mail.me.com" {
		t.Errorf("server = %q, want %q", c.server, "imap.mail.me.com")
	}
	if c.port != 993 {
		t.Errorf("port = %d, want %d", c.port, 993)
	}
	if c.username != "user@icloud.com" {
		t.Errorf("username = %q, want %q", c.username, "user@icloud.com")
	}
	if len(c.folders) != 2 || c.folders[0] != "INBOX" || c.folders[1] != "Sent" {
		t.Errorf("folders = %v, want [INBOX Sent]", c.folders)
	}
}

func TestConfigure_Defaults(t *testing.T) {
	c := &Connector{port: 993, folders: []string{"INBOX"}}
	err := c.Configure(connector.Config{
		"server":   "imap.example.com",
		"username": "user@example.com",
		"password": "pass",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.name != "imap" {
		t.Errorf("name = %q, want %q", c.name, "imap")
	}
	if c.port != 993 {
		t.Errorf("port = %d, want %d", c.port, 993)
	}
	if len(c.folders) != 1 || c.folders[0] != "INBOX" {
		t.Errorf("folders = %v, want [INBOX]", c.folders)
	}
}

func TestConfigure_MissingServer(t *testing.T) {
	c := &Connector{port: 993, folders: []string{"INBOX"}}
	err := c.Configure(connector.Config{
		"username": "user@example.com",
		"password": "pass",
	})
	if err == nil || !strings.Contains(err.Error(), "server is required") {
		t.Errorf("error = %v, want 'server is required'", err)
	}
}

func TestConfigure_MissingUsername(t *testing.T) {
	c := &Connector{port: 993, folders: []string{"INBOX"}}
	err := c.Configure(connector.Config{
		"server":   "imap.example.com",
		"password": "pass",
	})
	if err == nil || !strings.Contains(err.Error(), "username is required") {
		t.Errorf("error = %v, want 'username is required'", err)
	}
}

func TestConfigure_MissingPassword(t *testing.T) {
	c := &Connector{port: 993, folders: []string{"INBOX"}}
	err := c.Configure(connector.Config{
		"server":   "imap.example.com",
		"username": "user@example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "password is required") {
		t.Errorf("error = %v, want 'password is required'", err)
	}
}

func TestConfigure_InvalidPort(t *testing.T) {
	c := &Connector{port: 993, folders: []string{"INBOX"}}
	err := c.Configure(connector.Config{
		"server":   "imap.example.com",
		"username": "user@example.com",
		"password": "pass",
		"port":     "abc",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid port") {
		t.Errorf("error = %v, want 'invalid port'", err)
	}
}

func TestConfigure_SyncSinceDate(t *testing.T) {
	c := &Connector{port: 993, folders: []string{"INBOX"}}
	err := c.Configure(connector.Config{
		"server":     "imap.example.com",
		"username":   "user@example.com",
		"password":   "pass",
		"sync_since": "2025-06-01",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.syncSince.IsZero() {
		t.Error("syncSince should not be zero")
	}
	expected := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if !c.syncSince.Equal(expected) {
		t.Errorf("syncSince = %v, want %v", c.syncSince, expected)
	}
}

func TestType_And_Name(t *testing.T) {
	c := &Connector{name: "my-mail"}
	if c.Type() != "imap" {
		t.Errorf("Type() = %q, want %q", c.Type(), "imap")
	}
	if c.Name() != "my-mail" {
		t.Errorf("Name() = %q, want %q", c.Name(), "my-mail")
	}
}

func TestSetExtractor(t *testing.T) {
	c := &Connector{}
	reg := extractor.NewRegistry("", nil)
	c.SetExtractor(reg)
	if c.extractor != reg {
		t.Error("SetExtractor did not set the extractor")
	}
}

func TestValidate_MissingFields(t *testing.T) {
	c := &Connector{}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "server, username, and password are required") {
		t.Errorf("error = %v, want mention of required fields", err)
	}
}

func TestValidate_ConnectionFailure(t *testing.T) {
	c := &Connector{
		server: "imap.example.com", port: 993,
		username: "user@example.com", password: "pass",
		dial: func(_ string, _ *imapclient.Options) (*imapclient.Client, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "cannot connect") {
		t.Errorf("error = %v, want 'cannot connect'", err)
	}
}

func TestRegistration(t *testing.T) {
	c, err := connector.Create("imap")
	if err != nil {
		t.Fatalf("connector.Create(\"imap\") failed: %v", err)
	}
	if c.Type() != "imap" {
		t.Errorf("Type() = %q, want %q", c.Type(), "imap")
	}
}

// --- streaming Fetch tests (per-folder enumeration + body fetch) ---

func TestStreamFetch_EnumeratesAllUIDsAcrossFolders(t *testing.T) {
	c := &Connector{name: "test-mail", folders: []string{"INBOX", "Sent"}}
	mock := &mockMailboxClient{
		uids: []imap.UID{1, 2, 7},
		msgs: []*imapclient.FetchMessageBuffer{},
	}

	result := runStreamFetch(t, c, mock, nil)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	// Two folders × three UIDs each = six source ids, prefixed by folder.
	// The connector emits lex-sorted per-folder, and folders are
	// iterated in lex order (INBOX < Sent) → global stream is sorted.
	want := []string{"INBOX:1", "INBOX:2", "INBOX:7", "Sent:1", "Sent:2", "Sent:7"}
	got := append([]string{}, result.SourceIDs...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SourceIDs = %v, want %v", got, want)
	}
}

func TestStreamFetch_EnumerationFailureAbortsSync(t *testing.T) {
	c := &Connector{name: "test-mail", folders: []string{"INBOX"}}
	mock := &mockMailboxClient{
		uids:            []imap.UID{1},
		failOnAllSearch: true,
	}
	result := runStreamFetch(t, c, mock, nil)
	if result.Err == nil {
		t.Fatal("expected error from failed SEARCH ALL")
	}
}

func TestStreamFetch_SingleFolder(t *testing.T) {
	c := &Connector{name: "test-mail", folders: []string{"INBOX"}}
	mock := &mockMailboxClient{
		uids: []imap.UID{1, 2},
		msgs: []*imapclient.FetchMessageBuffer{
			{
				UID: 1,
				Envelope: &imap.Envelope{
					Subject:   "Hello",
					Date:      time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
					MessageID: "msg1@example.com",
					From:      []imap.Address{{Name: "Alice", Mailbox: "alice", Host: "example.com"}},
				},
				BodySection: []imapclient.FetchBodySectionBuffer{
					{Bytes: buildMIMEMessage("text/plain", "Hello world")},
				},
			},
			{
				UID: 2,
				Envelope: &imap.Envelope{
					Subject:   "Hi again",
					Date:      time.Date(2026, 1, 16, 10, 0, 0, 0, time.UTC),
					MessageID: "msg2@example.com",
				},
				BodySection: []imapclient.FetchBodySectionBuffer{
					{Bytes: buildMIMEMessage("text/plain", "Second email")},
				},
			},
		},
	}

	result := runStreamFetch(t, c, mock, nil)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("got %d docs, want 2", len(result.Documents))
	}
	if result.LastCursor == nil {
		t.Fatal("expected at least one checkpoint")
	}
	uidVal, ok := result.LastCursor.CursorData["uid:INBOX"].(float64)
	if !ok || uidVal != 2 {
		t.Errorf("cursor uid:INBOX = %v, want 2", result.LastCursor.CursorData["uid:INBOX"])
	}
	if result.Documents[0].Title != "Hello" {
		t.Errorf("doc[0].Title = %q, want %q", result.Documents[0].Title, "Hello")
	}
	if result.Documents[0].SourceID != "INBOX:1" {
		t.Errorf("doc[0].SourceID = %q, want %q", result.Documents[0].SourceID, "INBOX:1")
	}
}

func TestStreamFetch_MultipleFolders(t *testing.T) {
	c := &Connector{name: "test-mail", folders: []string{"INBOX", "Sent"}}
	mock := &mockMailboxClient{
		uids: []imap.UID{10},
		msgs: []*imapclient.FetchMessageBuffer{
			{
				UID: 10,
				Envelope: &imap.Envelope{
					Subject:   "Test",
					Date:      time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC),
					MessageID: "msg10@example.com",
				},
				BodySection: []imapclient.FetchBodySectionBuffer{
					{Bytes: buildMIMEMessage("text/plain", "Body")},
				},
			},
		},
	}

	result := runStreamFetch(t, c, mock, nil)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	// Same mock returns same msg for both folders
	if len(result.Documents) != 2 {
		t.Fatalf("got %d docs, want 2 (one per folder)", len(result.Documents))
	}
	// Both folders should advance their per-folder UID cursor entries.
	if result.LastCursor == nil {
		t.Fatal("expected cursor")
	}
	if v, _ := result.LastCursor.CursorData["uid:INBOX"].(float64); v != 10 {
		t.Errorf("uid:INBOX = %v, want 10", result.LastCursor.CursorData["uid:INBOX"])
	}
	if v, _ := result.LastCursor.CursorData["uid:Sent"].(float64); v != 10 {
		t.Errorf("uid:Sent = %v, want 10", result.LastCursor.CursorData["uid:Sent"])
	}
}

func TestStreamFetch_WithCursor(t *testing.T) {
	c := &Connector{name: "test-mail", folders: []string{"INBOX"}}
	mock := &mockMailboxClient{
		uids: []imap.UID{51},
		msgs: []*imapclient.FetchMessageBuffer{
			{
				UID: 51,
				Envelope: &imap.Envelope{
					Subject:   "New message",
					Date:      time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC),
					MessageID: "msg51@example.com",
				},
				BodySection: []imapclient.FetchBodySectionBuffer{
					{Bytes: buildMIMEMessage("text/plain", "New content")},
				},
			},
		},
	}

	cursor := &model.SyncCursor{
		CursorData: map[string]any{"uid:INBOX": float64(50)},
	}

	result := runStreamFetch(t, c, mock, cursor)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Documents) != 1 {
		t.Fatalf("got %d docs, want 1", len(result.Documents))
	}
	if v, _ := result.LastCursor.CursorData["uid:INBOX"].(float64); v != 51 {
		t.Errorf("uid:INBOX = %v, want 51", result.LastCursor.CursorData["uid:INBOX"])
	}
}

func TestStreamFetch_NoMessages(t *testing.T) {
	c := &Connector{name: "test-mail", folders: []string{"INBOX"}}
	mock := &mockMailboxClient{uids: nil}

	result := runStreamFetch(t, c, mock, nil)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Documents) != 0 {
		t.Errorf("got %d docs, want 0", len(result.Documents))
	}
	// Empty folder still emits a checkpoint so the cursor can carry
	// forward (e.g. CONDSTORE ModSeq in a later step).
	if result.LastCursor == nil {
		t.Error("expected a checkpoint even for empty folder")
	}
}

func TestStreamFetch_SelectError(t *testing.T) {
	c := &Connector{name: "test-mail", folders: []string{"INBOX"}}
	mock := &mockMailboxClient{selectErr: fmt.Errorf("folder not found")}

	result := runStreamFetch(t, c, mock, nil)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "select") {
		t.Errorf("error = %v, want 'select' error", result.Err)
	}
}

func TestStreamFetch_SearchError(t *testing.T) {
	c := &Connector{name: "test-mail", folders: []string{"INBOX"}}
	mock := &mockMailboxClient{searchErr: fmt.Errorf("search failed")}

	result := runStreamFetch(t, c, mock, nil)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "search") {
		t.Errorf("error = %v, want 'search' error", result.Err)
	}
}

func TestStreamFetch_FetchError(t *testing.T) {
	c := &Connector{name: "test-mail", folders: []string{"INBOX"}}
	mock := &mockMailboxClient{
		uids:     []imap.UID{1},
		fetchErr: fmt.Errorf("fetch failed"),
	}

	result := runStreamFetch(t, c, mock, nil)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "fetch") {
		t.Errorf("error = %v, want 'fetch' error", result.Err)
	}
}

func TestStreamFetch_ContextCancelled(t *testing.T) {
	c := &Connector{name: "test-mail", folders: []string{"INBOX", "Sent"}}
	mock := &mockMailboxClient{
		uids: []imap.UID{1},
		msgs: []*imapclient.FetchMessageBuffer{
			{
				UID: 1,
				Envelope: &imap.Envelope{
					Subject: "Test", Date: time.Now(), MessageID: "m@x.com",
				},
				BodySection: []imapclient.FetchBodySectionBuffer{
					{Bytes: buildMIMEMessage("text/plain", "body")},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	result := runStreamFetchCtx(t, c, mock, nil, ctx)
	// Cancellation surfaces as either a terminal error or early
	// closure; at minimum, the stream must not hang.
	_ = result
}

func TestStreamFetch_SyncSince(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &Connector{name: "test-mail", folders: []string{"INBOX"}, syncSince: since}
	mock := &mockMailboxClient{uids: nil}

	result := runStreamFetch(t, c, mock, nil)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Documents) != 0 {
		t.Errorf("got %d docs, want 0", len(result.Documents))
	}
}

// TestCopyCursorData covers the helper that defensively copies
// cursor state before mid-sync mutation.
func TestCopyCursorData(t *testing.T) {
	t.Run("nil cursor", func(t *testing.T) {
		got := copyCursorData(nil)
		if got == nil || len(got) != 0 {
			t.Fatal("expected empty non-nil map")
		}
	})
	t.Run("nil CursorData field", func(t *testing.T) {
		got := copyCursorData(&model.SyncCursor{})
		if got == nil || len(got) != 0 {
			t.Errorf("nil CursorData should yield empty map, got %v", got)
		}
	})
	t.Run("independent of original", func(t *testing.T) {
		orig := &model.SyncCursor{CursorData: map[string]any{"uid:INBOX": float64(42)}}
		got := copyCursorData(orig)
		got["uid:INBOX"] = float64(99)
		if v, _ := orig.CursorData["uid:INBOX"].(float64); v != 42 {
			t.Errorf("mutation leaked back to original: %v", v)
		}
	})
}

// TestCursorGetters_AbsentOrMalformed exercises the defensive
// zero-return paths in cursorUIDValidityForFolder and
// cursorModSeqForFolder — cases where the cursor is nil, the key
// is absent, or the value has the wrong type.
func TestCursorGetters_AbsentOrMalformed(t *testing.T) {
	if got := cursorUIDValidityForFolder(nil, "INBOX"); got != 0 {
		t.Errorf("nil cursor uidvalidity = %d, want 0", got)
	}
	if got := cursorModSeqForFolder(nil, "INBOX"); got != 0 {
		t.Errorf("nil cursor modseq = %d, want 0", got)
	}
	bad := &model.SyncCursor{CursorData: map[string]any{
		"uidvalidity:INBOX": "not-a-number",
		"modseq:INBOX":      "bad",
	}}
	if got := cursorUIDValidityForFolder(bad, "INBOX"); got != 0 {
		t.Errorf("malformed uidvalidity = %d, want 0", got)
	}
	if got := cursorModSeqForFolder(bad, "INBOX"); got != 0 {
		t.Errorf("malformed modseq = %d, want 0", got)
	}
}

// TestStripHTMLRegex covers the naive-regex fallback path — used
// only when html.Parse fails outright. Guarded here so future
// refactors don't accidentally tombstone it.
func TestStripHTMLRegex(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"<p>Hello</p>", "Hello"},
		{"<div><b>Bold</b> and <i>italic</i></div>", "Bold and italic"},
		{"", ""},
		{"No tags here", "No tags here"},
		{"   leading and trailing whitespace   ", "leading and trailing whitespace"},
	}
	for _, tc := range cases {
		if got := stripHTMLRegex(tc.in); got != tc.want {
			t.Errorf("stripHTMLRegex(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- CONDSTORE tests ---

func TestStreamFetch_CondStore_FastSkipUnchangedFolder(t *testing.T) {
	// When the server's HighestModSeq matches the cursor's cached
	// value, the connector still runs SEARCH ALL and emits the
	// folder's SourceIDs (so the merge-diff has a complete view),
	// but skips the delta SEARCH + body FETCH. Fast-skipping the
	// enumeration would cause mass deletions — see the comment on
	// streamFolder.
	c := &Connector{name: "test-mail", folders: []string{"INBOX"}}
	mock := &mockMailboxClient{
		selectData: imap.SelectData{UIDValidity: 42, HighestModSeq: 100},
		uids:       []imap.UID{1, 2, 3},
	}
	cursor := &model.SyncCursor{
		CursorData: map[string]any{
			"uidvalidity:INBOX": float64(42),
			"modseq:INBOX":      float64(100),
		},
	}
	result := runStreamFetch(t, c, mock, cursor)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if len(result.Documents) != 0 {
		t.Errorf("fast-skip should emit no docs, got %d", len(result.Documents))
	}
	// SourceIDs must still flow so the pipeline's merge-diff sees
	// a complete picture of the folder's state.
	if len(result.SourceIDs) != 3 {
		t.Errorf("fast-skip should still emit every source id (3), got %v", result.SourceIDs)
	}
	if result.LastCursor == nil {
		t.Fatal("expected a checkpoint even on fast-skip")
	}
	if v, _ := result.LastCursor.CursorData["modseq:INBOX"].(float64); v != 100 {
		t.Errorf("modseq:INBOX = %v, want 100", result.LastCursor.CursorData["modseq:INBOX"])
	}
	// SEARCH ALL is expected (1 call); the delta SEARCH is skipped.
	if mock.searchCallCount != 1 {
		t.Errorf("expected exactly 1 SEARCH call (SEARCH ALL), got %d", mock.searchCallCount)
	}
}

func TestStreamFetch_CondStore_UIDValidityChangeDropsCache(t *testing.T) {
	// Server returns a new UIDVALIDITY. The cached HighestModSeq is
	// stale and must not gate the fast-skip — a full enumeration
	// runs.
	c := &Connector{name: "test-mail", folders: []string{"INBOX"}}
	mock := &mockMailboxClient{
		selectData: imap.SelectData{UIDValidity: 999, HighestModSeq: 100},
		uids:       []imap.UID{1, 2},
		msgs: []*imapclient.FetchMessageBuffer{
			{
				UID: 1,
				Envelope: &imap.Envelope{
					Subject: "A", Date: time.Now(), MessageID: "a@x.com",
				},
				BodySection: []imapclient.FetchBodySectionBuffer{
					{Bytes: buildMIMEMessage("text/plain", "A body")},
				},
			},
			{
				UID: 2,
				Envelope: &imap.Envelope{
					Subject: "B", Date: time.Now(), MessageID: "b@x.com",
				},
				BodySection: []imapclient.FetchBodySectionBuffer{
					{Bytes: buildMIMEMessage("text/plain", "B body")},
				},
			},
		},
	}
	cursor := &model.SyncCursor{
		CursorData: map[string]any{
			"uidvalidity:INBOX": float64(42), // mismatches server
			"modseq:INBOX":      float64(100),
		},
	}
	result := runStreamFetch(t, c, mock, cursor)
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	// With cached modseq invalidated, the connector falls back to
	// UID-range delta (lastUID=0 → everything) and fetches both.
	if len(result.Documents) != 2 {
		t.Fatalf("UIDValidity change should force re-fetch, got %d docs", len(result.Documents))
	}
	if v, _ := result.LastCursor.CursorData["uidvalidity:INBOX"].(float64); v != 999 {
		t.Errorf("uidvalidity:INBOX = %v, want 999", v)
	}
}

// --- Fetch (top-level) tests ---

func TestFetch_ConnectionError(t *testing.T) {
	c := &Connector{
		name: "test", server: "imap.example.com", port: 993,
		username: "user@example.com", password: "pass",
		folders: []string{"INBOX"},
		dial: func(_ string, _ *imapclient.Options) (*imapclient.Client, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	result := testutil.RunFetch(t, c, nil)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "connect") {
		t.Errorf("error = %v, want 'connect'", result.Err)
	}
}

// --- messageToDocuments tests ---

func TestMessageToDocuments_BasicEmail(t *testing.T) {
	c := &Connector{name: "test-mail"}
	msg := &imapclient.FetchMessageBuffer{
		UID: 42,
		Envelope: &imap.Envelope{
			Subject:   "Test Subject",
			Date:      time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
			MessageID: "abc123@example.com",
			From:      []imap.Address{{Name: "Alice", Mailbox: "alice", Host: "example.com"}},
			To:        []imap.Address{{Mailbox: "bob", Host: "example.com"}},
		},
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: buildMIMEMessage("text/plain", "Hello Bob!")},
		},
	}

	docs := c.messageToDocuments(msg, "INBOX")
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}

	doc := docs[0]
	if doc.SourceType != "imap" {
		t.Errorf("SourceType = %q, want %q", doc.SourceType, "imap")
	}
	if doc.SourceName != "test-mail" {
		t.Errorf("SourceName = %q, want %q", doc.SourceName, "test-mail")
	}
	if doc.SourceID != "INBOX:42" {
		t.Errorf("SourceID = %q, want %q", doc.SourceID, "INBOX:42")
	}
	if doc.Title != "Test Subject" {
		t.Errorf("Title = %q, want %q", doc.Title, "Test Subject")
	}
	if !strings.Contains(doc.Content, "Hello Bob!") {
		t.Errorf("Content = %q, want to contain 'Hello Bob!'", doc.Content)
	}
	if doc.URL != "mid:abc123@example.com" {
		t.Errorf("URL = %q, want %q", doc.URL, "mid:abc123@example.com")
	}
	if doc.Visibility != "private" {
		t.Errorf("Visibility = %q, want %q", doc.Visibility, "private")
	}

	md := doc.Metadata
	if md["folder"] != "INBOX" {
		t.Errorf("metadata folder = %v, want INBOX", md["folder"])
	}
	from, _ := md["from"].(string)
	if !strings.Contains(from, "Alice") {
		t.Errorf("metadata from = %q, want to contain Alice", from)
	}
	to, _ := md["to"].(string)
	if !strings.Contains(to, "bob@example.com") {
		t.Errorf("metadata to = %q, want to contain bob@example.com", to)
	}
}

func TestMessageToDocuments_NilEnvelope(t *testing.T) {
	c := &Connector{name: "test-mail"}
	docs := c.messageToDocuments(&imapclient.FetchMessageBuffer{UID: 1}, "INBOX")
	if len(docs) != 0 {
		t.Errorf("got %d docs, want 0 for nil envelope", len(docs))
	}
}

func TestMessageToDocuments_WithAttachments(t *testing.T) {
	c := &Connector{name: "test-mail"}

	body := buildMultipartMessage(
		"Email body here.",
		[]testAttachment{{filename: "report.pdf", contentType: "application/pdf", data: []byte("pdf")}},
	)

	msg := &imapclient.FetchMessageBuffer{
		UID: 100,
		Envelope: &imap.Envelope{
			Subject:   "Report",
			Date:      time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC),
			MessageID: "msg100@example.com",
			From:      []imap.Address{{Mailbox: "sender", Host: "example.com"}},
			To:        []imap.Address{{Mailbox: "recipient", Host: "example.com"}},
			Cc:        []imap.Address{{Mailbox: "cc-user", Host: "example.com"}},
		},
		BodySection: []imapclient.FetchBodySectionBuffer{{Bytes: body}},
	}

	// Without extractor: 2 docs — the email body AND the attachment doc with empty
	// content. Attachments are always emitted (even when extraction is impossible)
	// so they remain discoverable by metadata and previewable via BinaryFetcher.
	docs := c.messageToDocuments(msg, "INBOX")
	if len(docs) != 2 {
		t.Fatalf("got %d docs without extractor, want 2 (email + empty-content attachment)", len(docs))
	}

	md := docs[0].Metadata
	if md["has_attachments"] != true {
		t.Error("expected has_attachments = true")
	}
	filenames, ok := md["attachment_filenames"].([]string)
	if !ok || len(filenames) != 1 || filenames[0] != "report.pdf" {
		t.Errorf("attachment_filenames = %v, want [report.pdf]", md["attachment_filenames"])
	}
	if _, ok := md["cc"]; !ok {
		t.Error("expected cc in metadata")
	}

	// Verify the attachment doc has empty content but full metadata
	att := docs[1]
	if att.Content != "" {
		t.Errorf("expected empty content for unextractable attachment, got %q", att.Content)
	}
	if att.Title != "report.pdf" {
		t.Errorf("expected attachment title 'report.pdf', got %q", att.Title)
	}
	if att.MimeType != "application/pdf" {
		t.Errorf("expected MimeType 'application/pdf', got %q", att.MimeType)
	}
	if att.Size != int64(len("pdf")) {
		t.Errorf("expected Size %d, got %d", len("pdf"), att.Size)
	}
	if att.SourceID != "INBOX:100:attachment:0" {
		t.Errorf("expected attachment SourceID 'INBOX:100:attachment:0', got %q", att.SourceID)
	}
}

func TestMessageToDocuments_WithExtractor(t *testing.T) {
	reg := extractor.NewRegistry("", nil)
	c := &Connector{name: "test-mail", extractor: reg}

	body := buildMultipartMessage(
		"Email body.",
		[]testAttachment{{filename: "notes.txt", contentType: "text/plain", data: []byte("Attachment text content")}},
	)

	msg := &imapclient.FetchMessageBuffer{
		UID: 200,
		Envelope: &imap.Envelope{
			Subject:   "With Attachment",
			Date:      time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC),
			MessageID: "msg200@example.com",
			From:      []imap.Address{{Mailbox: "sender", Host: "example.com"}},
		},
		BodySection: []imapclient.FetchBodySectionBuffer{{Bytes: body}},
	}

	docs := c.messageToDocuments(msg, "INBOX")
	// Should have 2 docs: email + attachment (text/plain is handled by PlainText extractor)
	if len(docs) != 2 {
		t.Fatalf("got %d docs with extractor, want 2", len(docs))
	}

	attDoc := docs[1]
	if attDoc.SourceID != "INBOX:200:attachment:0" {
		t.Errorf("attachment SourceID = %q, want %q", attDoc.SourceID, "INBOX:200:attachment:0")
	}
	if attDoc.Title != "notes.txt" {
		t.Errorf("attachment Title = %q, want %q", attDoc.Title, "notes.txt")
	}
	if !strings.Contains(attDoc.Content, "Attachment text content") {
		t.Errorf("attachment Content = %q, want to contain 'Attachment text content'", attDoc.Content)
	}

	attMd := attDoc.Metadata
	if attMd["parent_subject"] != "With Attachment" {
		t.Errorf("parent_subject = %v, want 'With Attachment'", attMd["parent_subject"])
	}
	// The typed attachment_of edge replaces the old parent_message_id
	// metadata key — its target_source_id is the parent email's
	// source_id, not the RFC 5322 Message-ID (that stays denormalized
	// on the email chunk as IMAPMessageID for the /related resolver).
	if len(attDoc.Relations) != 1 || attDoc.Relations[0].Type != "attachment_of" ||
		attDoc.Relations[0].TargetSourceID != "INBOX:200" {
		t.Errorf("expected attachment_of → INBOX:200, got %+v", attDoc.Relations)
	}
	if attDoc.MimeType != "text/plain" {
		t.Errorf("expected attachment MimeType 'text/plain', got %q", attDoc.MimeType)
	}
	if attDoc.Size != int64(len("Attachment text content")) {
		t.Errorf("expected Size %d, got %d", len("Attachment text content"), attDoc.Size)
	}
}

func TestMessageToDocuments_ZeroDate(t *testing.T) {
	c := &Connector{name: "test-mail"}
	msg := &imapclient.FetchMessageBuffer{
		UID: 1,
		Envelope: &imap.Envelope{
			Subject: "No date",
		},
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: buildMIMEMessage("text/plain", "content")},
		},
	}

	docs := c.messageToDocuments(msg, "INBOX")
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}
	if docs[0].CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero when envelope date is zero")
	}
}

func TestMessageToDocuments_NoBody(t *testing.T) {
	c := &Connector{name: "test-mail"}
	msg := &imapclient.FetchMessageBuffer{
		UID: 1,
		Envelope: &imap.Envelope{
			Subject: "Empty body",
			Date:    time.Now(),
		},
	}

	docs := c.messageToDocuments(msg, "INBOX")
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}
	// Content may be empty — that's fine
}

func TestMessageToDocuments_NoMessageID(t *testing.T) {
	c := &Connector{name: "test-mail"}
	msg := &imapclient.FetchMessageBuffer{
		UID: 1,
		Envelope: &imap.Envelope{
			Subject: "No ID",
			Date:    time.Now(),
		},
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: buildMIMEMessage("text/plain", "test")},
		},
	}

	docs := c.messageToDocuments(msg, "INBOX")
	if docs[0].URL != "" {
		t.Errorf("URL = %q, want empty when no MessageID", docs[0].URL)
	}
}

// --- formatAddresses tests ---

func TestFormatAddresses(t *testing.T) {
	tests := []struct {
		name  string
		addrs []imap.Address
		want  string
	}{
		{"with name", []imap.Address{{Name: "Alice", Mailbox: "alice", Host: "example.com"}}, "Alice <alice@example.com>"},
		{"without name", []imap.Address{{Mailbox: "bob", Host: "example.com"}}, "bob@example.com"},
		{"multiple", []imap.Address{
			{Name: "Alice", Mailbox: "alice", Host: "example.com"},
			{Mailbox: "bob", Host: "example.com"},
		}, "Alice <alice@example.com>, bob@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAddresses(tt.addrs)
			if got != tt.want {
				t.Errorf("formatAddresses() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Parser tests ---

func TestParseEmailBody_PlainText(t *testing.T) {
	raw := buildMIMEMessage("text/plain", "Hello, this is a test email.")
	content, attachments := parseEmailBody(raw)
	if !strings.Contains(content, "Hello, this is a test email.") {
		t.Errorf("content = %q, want test text", content)
	}
	if len(attachments) != 0 {
		t.Errorf("attachments = %d, want 0", len(attachments))
	}
}

func TestParseEmailBody_HTMLOnly(t *testing.T) {
	raw := buildMIMEMessage("text/html", "<html><body><p>Hello <b>world</b></p></body></html>")
	content, _ := parseEmailBody(raw)
	if !strings.Contains(content, "Hello") || !strings.Contains(content, "world") {
		t.Errorf("content = %q, want stripped HTML", content)
	}
	if strings.Contains(content, "<") {
		t.Errorf("content = %q, should not contain HTML tags", content)
	}
}

func TestParseEmailBody_MultipartWithAttachment(t *testing.T) {
	raw := buildMultipartMessage(
		"Body text.",
		[]testAttachment{{filename: "doc.pdf", contentType: "application/pdf", data: []byte("PDF")}},
	)
	content, attachments := parseEmailBody(raw)
	if !strings.Contains(content, "Body text.") {
		t.Errorf("content = %q, want body text", content)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(attachments))
	}
	if attachments[0].Filename != "doc.pdf" {
		t.Errorf("filename = %q, want doc.pdf", attachments[0].Filename)
	}
}

func TestParseEmailBody_Empty(t *testing.T) {
	content, attachments := parseEmailBody(nil)
	if content != "" || len(attachments) != 0 {
		t.Errorf("expected empty results for nil input")
	}
}

func TestParseEmailBody_NonMIME(t *testing.T) {
	raw := []byte("Just plain text.")
	content, _ := parseEmailBody(raw)
	if content != "Just plain text." {
		t.Errorf("content = %q, want raw text", content)
	}
}

func TestParseEmailBody_PreferPlainOverHTML(t *testing.T) {
	raw := buildMultipartAlternative("Plain version", "<p>HTML version</p>")
	content, _ := parseEmailBody(raw)
	if !strings.Contains(content, "Plain version") {
		t.Errorf("content = %q, want plain text preferred", content)
	}
}

func TestStripHTML(t *testing.T) {
	// The DOM-aware stripper preserves paragraph structure with newlines but
	// drops <script>/<style>/<head> entirely and unwraps anchor hrefs.
	tests := []struct {
		name, input, wantContains, wantNotContains string
	}{
		{"simple tags", "<p>Hello</p>", "Hello", "<"},
		{"nested tags", "<div><p>Hello <b>world</b></p></div>", "Hello world", "<"},
		{"two paragraphs", "<p>Hello</p><p>World</p>", "Hello", ""},
		{"empty", "", "", ""},
		{"no tags", "Just text", "Just text", ""},
		{"drops script", "<p>Hello</p><script>var x = 1;</script><p>World</p>", "Hello", "var x"},
		{"drops style", "<style>.x{color:red}</style><p>Hello</p>", "Hello", "color:red"},
		{"drops head", "<html><head><title>Subj</title></head><body><p>Body</p></body></html>", "Body", "Subj"},
		{"unwraps anchor href", `<p>Click <a href="https://track.example.com/abc">here</a>.</p>`, "here", "track.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTML(tt.input)
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Errorf("stripHTML(%q) = %q, want to contain %q", tt.input, got, tt.wantContains)
			}
			if tt.wantNotContains != "" && strings.Contains(got, tt.wantNotContains) {
				t.Errorf("stripHTML(%q) = %q, should NOT contain %q", tt.input, got, tt.wantNotContains)
			}
		})
	}
}

func TestCleanEmailText_StripsTrackingURLs(t *testing.T) {
	in := "Hello!\n\nClick https://click.mailgun.com/abc123 to confirm.\nThanks."
	out := cleanEmailText(in)
	if strings.Contains(out, "click.mailgun.com") {
		t.Errorf("expected tracking URL stripped, got %q", out)
	}
	if !strings.Contains(out, "Hello!") || !strings.Contains(out, "Thanks.") {
		t.Errorf("expected surrounding text preserved, got %q", out)
	}
}

func TestCleanEmailText_StripsLongOpaqueURLs(t *testing.T) {
	in := "See: https://example.com/r?a=eyJ1cmwiOiJodHRwczovL3RyYWNrLmV4YW1wbGUuY29tL2FiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6MDEyMzQ1Njc4OWFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6IiwidWlkIjoiMTIzNDUifQ end."
	out := cleanEmailText(in)
	if strings.Contains(out, "eyJ1") {
		t.Errorf("expected long opaque URL stripped, got %q", out)
	}
	if !strings.Contains(out, "See:") || !strings.Contains(out, "end.") {
		t.Errorf("expected surrounding text preserved, got %q", out)
	}
}

func TestCleanEmailText_RemovesQuotedReplies(t *testing.T) {
	in := "Sounds good, agreed.\n\nOn Wed, Apr 9, 2026 at 3:14 PM, Bob <bob@example.com> wrote:\n> Should we meet tomorrow?\n> I'm free at 2pm.\n> -Bob"
	out := cleanEmailText(in)
	if !strings.Contains(out, "Sounds good") {
		t.Errorf("expected reply text preserved, got %q", out)
	}
	if strings.Contains(out, "wrote:") {
		t.Errorf("expected 'wrote:' header removed, got %q", out)
	}
	if strings.Contains(out, "tomorrow") {
		t.Errorf("expected quoted reply lines removed, got %q", out)
	}
}

func TestCleanEmailText_RemovesSignature(t *testing.T) {
	in := "Body of the email.\nMore body.\n\n-- \nJohn Doe\njohn@example.com\nVP of Stuff"
	out := cleanEmailText(in)
	if !strings.Contains(out, "Body of the email") || !strings.Contains(out, "More body") {
		t.Errorf("expected body preserved, got %q", out)
	}
	if strings.Contains(out, "John Doe") || strings.Contains(out, "VP of Stuff") {
		t.Errorf("expected signature removed, got %q", out)
	}
}

func TestCleanEmailText_Empty(t *testing.T) {
	if got := cleanEmailText(""); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestCleanEmailText_MultiLocaleQuotedReplies(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantKeep    string
		wantDropped string
	}{
		{
			name:        "english",
			in:          "Looks good.\n\nOn Wed, Apr 9, 2026 at 3:14 PM, Bob <bob@example.com> wrote:\n> Meeting tomorrow?",
			wantKeep:    "Looks good.",
			wantDropped: "wrote:",
		},
		{
			name:        "german",
			in:          "Danke!\n\nAm 09.04.2026 um 15:14 schrieb Hans <hans@example.de>:\n> Treffen wir uns morgen?",
			wantKeep:    "Danke!",
			wantDropped: "schrieb",
		},
		{
			name:        "french",
			in:          "Merci.\n\nLe mercredi 9 avril 2026, Marie <marie@example.fr> a écrit :\n> On se voit demain ?",
			wantKeep:    "Merci.",
			wantDropped: "a écrit",
		},
		{
			name:        "spanish",
			in:          "Perfecto.\n\nEl mié, 9 abr 2026 a las 15:14, Ana <ana@example.es> escribió:\n> ¿Nos vemos mañana?",
			wantKeep:    "Perfecto.",
			wantDropped: "escribió",
		},
		{
			name:        "italian",
			in:          "Ok!\n\nIl giorno mer 9 apr 2026 alle ore 15:14, Luca <luca@example.it> ha scritto:\n> Ci vediamo domani?",
			wantKeep:    "Ok!",
			wantDropped: "ha scritto",
		},
		{
			name:        "portuguese",
			in:          "Obrigado.\n\nEm qua., 9 de abr. de 2026 às 15:14, João <joao@example.pt> escreveu:\n> Nos vemos amanhã?",
			wantKeep:    "Obrigado.",
			wantDropped: "escreveu",
		},
		{
			name:        "dutch",
			in:          "Bedankt.\n\nOp wo 9 apr 2026 om 15:14 schreef Jan <jan@example.nl>:\n> Zien we elkaar morgen?",
			wantKeep:    "Bedankt.",
			wantDropped: "schreef",
		},
		{
			name:        "bulgarian",
			in:          "Звучи добре.\n\nНа ср, 9.04.2026 г., 15:14 ч. Иван <ivan@example.bg> написа:\n> Ще се видим ли утре?",
			wantKeep:    "Звучи добре.",
			wantDropped: "написа",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := cleanEmailText(tt.in)
			if !strings.Contains(out, tt.wantKeep) {
				t.Errorf("expected body %q preserved, got %q", tt.wantKeep, out)
			}
			if strings.Contains(out, tt.wantDropped) {
				t.Errorf("expected reply header %q removed, got %q", tt.wantDropped, out)
			}
		})
	}
}

// --- Test helpers for building MIME messages ---

type testAttachment struct {
	filename    string
	contentType string
	data        []byte
}

func buildMIMEMessage(contentType, body string) []byte {
	var buf strings.Builder
	fmt.Fprintf(&buf, "Content-Type: %s; charset=utf-8\r\n", contentType)
	buf.WriteString("\r\n")
	buf.WriteString(body)
	return []byte(buf.String())
}

func buildMultipartMessage(bodyText string, attachments []testAttachment) []byte {
	boundary := "boundary123"
	var buf strings.Builder
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%s\r\n", boundary)
	buf.WriteString("\r\n")

	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(bodyText)
	buf.WriteString("\r\n")

	for _, att := range attachments {
		buf.WriteString("--" + boundary + "\r\n")
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", att.contentType)
		fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=%q\r\n", att.filename)
		buf.WriteString("\r\n")
		buf.Write(att.data)
		buf.WriteString("\r\n")
	}

	buf.WriteString("--" + boundary + "--\r\n")
	return []byte(buf.String())
}

func buildMultipartAlternative(plainText, htmlText string) []byte {
	boundary := "altboundary456"
	var buf strings.Builder
	fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%s\r\n", boundary)
	buf.WriteString("\r\n")

	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(plainText)
	buf.WriteString("\r\n")

	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(htmlText)
	buf.WriteString("\r\n")

	buf.WriteString("--" + boundary + "--\r\n")
	return []byte(buf.String())
}
