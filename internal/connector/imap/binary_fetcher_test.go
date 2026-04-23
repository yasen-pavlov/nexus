package imap

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/testutil"
)

// fakeBinaryStore is a minimal in-memory BinaryStoreAPI stub for tests.
// Counts Put/Get/Exists calls so tests can assert cache behavior
// (was IMAP dialed or was the blob served from cache).
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
		// Mirror the real store's os.ErrNotExist contract so the
		// connector's fall-through-on-miss logic exercises correctly.
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

// Mimic os.ErrNotExist comparability via errors.Is. The real
// BinaryStore returns os.ErrNotExist directly; we wrap to avoid an os
// import in tests while still tripping errors.Is via the Is method.
func (*notExistErr) Is(target error) bool {
	return target.Error() == "file does not exist"
}

// TestParseSourceID exercises the regex-based source ID decoder.
func TestParseSourceID(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantFolder string
		wantUID    imap.UID
		wantIdx    int
		wantAtt    bool
		wantErr    bool
	}{
		{"email", "INBOX:42", "INBOX", 42, 0, false, false},
		{"attachment", "INBOX:42:attachment:3", "INBOX", 42, 3, true, false},
		{"nested folder", "Archive/2026:7", "Archive/2026", 7, 0, false, false},
		{"folder with colon", "Label:X:100", "Label:X", 100, 0, false, false},
		{"attachment zero idx", "Sent:1:attachment:0", "Sent", 1, 0, true, false},
		{"empty", "", "", 0, 0, false, true},
		{"no colon", "INBOX", "", 0, 0, false, true},
		{"trailing colon", "INBOX:", "", 0, 0, false, true},
		{"non-numeric uid", "INBOX:abc", "", 0, 0, false, true},
		{"truncated attachment", "INBOX:1:attachment:", "INBOX:1:attachment", 0, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSourceID(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.folder != tt.wantFolder {
				t.Errorf("folder = %q, want %q", got.folder, tt.wantFolder)
			}
			if got.uid != tt.wantUID {
				t.Errorf("uid = %d, want %d", got.uid, tt.wantUID)
			}
			if got.isAttachment != tt.wantAtt {
				t.Errorf("isAttachment = %v, want %v", got.isAttachment, tt.wantAtt)
			}
			if got.attachmentIdx != tt.wantIdx {
				t.Errorf("attachmentIdx = %d, want %d", got.attachmentIdx, tt.wantIdx)
			}
		})
	}
}

// newTestConnectorWithServer spins up a fake IMAP server populated
// with a single INBOX message built from textBody + attachments,
// and returns a Connector wired to talk to it along with a cleanup.
func newTestConnectorWithServer(t *testing.T, textBody string, atts []testAttachment) (*Connector, *fakeBinaryStore, []byte, func()) {
	t.Helper()
	body := buildMultipartMessage(textBody, atts)
	env := &imap.Envelope{
		Subject: "Preview me", Date: time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC),
		MessageID: "m1@test.com",
	}
	addr, cleanup := startFakeIMAPServer(
		map[string][]fakeMessage{"INBOX": {{uid: 1, date: env.Date, envelope: env, body: body}}},
		"user", "pw",
	)

	host, port := parseAddr(addr)
	store := newFakeBinaryStore()
	c := &Connector{
		name:        "icloud",
		server:      host,
		port:        port,
		username:    "user",
		password:    "pw",
		folders:     []string{"INBOX"},
		dial:        dialInsecure,
		binaryStore: store,
		cacheConfig: connector.CacheConfig{Mode: "lazy"},
	}
	return c, store, body, cleanup
}

func TestFetchBinary_Attachment_HappyPath(t *testing.T) {
	payload := []byte("PDF-BYTES-12345")
	c, _, _, cleanup := newTestConnectorWithServer(t, "body text",
		[]testAttachment{{filename: "receipt.pdf", contentType: "application/pdf", data: payload}},
	)
	defer cleanup()

	bc, err := c.FetchBinary(context.Background(), "INBOX:1:attachment:0")
	if err != nil {
		t.Fatalf("FetchBinary: %v", err)
	}
	defer bc.Reader.Close() //nolint:errcheck // test cleanup

	got, err := io.ReadAll(bc.Reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("got %q, want %q", got, payload)
	}
	if bc.MimeType != "application/pdf" {
		t.Errorf("MimeType = %q, want application/pdf", bc.MimeType)
	}
	if bc.Filename != "receipt.pdf" {
		t.Errorf("Filename = %q, want receipt.pdf", bc.Filename)
	}
	if bc.Size != int64(len(payload)) {
		t.Errorf("Size = %d, want %d", bc.Size, len(payload))
	}
}

func TestFetchBinary_RawEmail(t *testing.T) {
	c, _, body, cleanup := newTestConnectorWithServer(t, "hi",
		[]testAttachment{{filename: "a.bin", contentType: "application/octet-stream", data: []byte("x")}},
	)
	defer cleanup()

	bc, err := c.FetchBinary(context.Background(), "INBOX:1")
	if err != nil {
		t.Fatalf("FetchBinary: %v", err)
	}
	defer bc.Reader.Close() //nolint:errcheck // test cleanup

	got, _ := io.ReadAll(bc.Reader)
	if !bytes.Equal(got, body) {
		t.Errorf("raw email body mismatch")
	}
	if bc.MimeType != "message/rfc822" {
		t.Errorf("MimeType = %q, want message/rfc822", bc.MimeType)
	}
	if !strings.HasSuffix(bc.Filename, ".eml") {
		t.Errorf("Filename = %q, want *.eml", bc.Filename)
	}
}

func TestFetchBinary_CacheMiss_PopulatesStore(t *testing.T) {
	payload := []byte("attach-bytes")
	c, store, _, cleanup := newTestConnectorWithServer(t, "body",
		[]testAttachment{{filename: "f.bin", contentType: "application/octet-stream", data: payload}},
	)
	defer cleanup()

	// Precondition: cache is empty.
	if ok, _ := store.Exists(context.Background(), "imap", c.name, "INBOX:1:attachment:0"); ok {
		t.Fatal("precondition: cache should be empty")
	}

	if _, err := c.FetchBinary(context.Background(), "INBOX:1:attachment:0"); err != nil {
		t.Fatal(err)
	}

	// Store should have the blob.
	r, err := store.Get(context.Background(), "imap", c.name, "INBOX:1:attachment:0")
	if err != nil {
		t.Fatalf("blob should be cached after miss, got err %v", err)
	}
	defer r.Close() //nolint:errcheck // test cleanup
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, payload) {
		t.Errorf("cached bytes mismatch: got %q, want %q", got, payload)
	}
	if puts := store.puts.Load(); puts < 1 {
		t.Errorf("expected at least 1 Put, got %d", puts)
	}
}

func TestFetchBinary_CacheHit_SkipsIMAP(t *testing.T) {
	// Build a connector pointed at a nonexistent IMAP server — the
	// dial will fail if it's ever invoked. Seed the cache directly.
	store := newFakeBinaryStore()
	c := &Connector{
		name:        "icloud",
		server:      "127.0.0.1",
		port:        1, // nothing listens here
		username:    "u",
		password:    "p",
		dial:        dialInsecure,
		binaryStore: store,
		cacheConfig: connector.CacheConfig{Mode: "lazy"},
	}
	cached := []byte("cached-bytes")
	if err := store.Put(context.Background(), "imap", c.name, "INBOX:1:attachment:0", bytes.NewReader(cached), int64(len(cached))); err != nil {
		t.Fatal(err)
	}

	bc, err := c.FetchBinary(context.Background(), "INBOX:1:attachment:0")
	if err != nil {
		t.Fatalf("FetchBinary on cache hit should not error (IMAP never dialed): %v", err)
	}
	defer bc.Reader.Close() //nolint:errcheck // test cleanup
	got, _ := io.ReadAll(bc.Reader)
	if !bytes.Equal(got, cached) {
		t.Errorf("cache hit returned wrong bytes: got %q, want %q", got, cached)
	}
}

func TestFetchBinary_ModeNone_SkipsCache(t *testing.T) {
	payload := []byte("no-cache-bytes")
	c, store, _, cleanup := newTestConnectorWithServer(t, "body",
		[]testAttachment{{filename: "f.bin", contentType: "application/octet-stream", data: payload}},
	)
	defer cleanup()
	c.cacheConfig = connector.CacheConfig{Mode: "none"}

	// Call twice. Neither call should touch the cache.
	for i := 0; i < 2; i++ {
		bc, err := c.FetchBinary(context.Background(), "INBOX:1:attachment:0")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		_, _ = io.ReadAll(bc.Reader)
		_ = bc.Reader.Close()
	}

	if gets := store.gets.Load(); gets != 0 {
		t.Errorf("mode=none: store.Get should not be called, got %d", gets)
	}
	if puts := store.puts.Load(); puts != 0 {
		t.Errorf("mode=none: store.Put should not be called, got %d", puts)
	}
}

func TestFetchBinary_EagerMode_PopulatesDuringSync(t *testing.T) {
	payload := []byte("eager-attach")
	c, store, _, cleanup := newTestConnectorWithServer(t, "body",
		[]testAttachment{{filename: "e.bin", contentType: "application/octet-stream", data: payload}},
	)
	defer cleanup()
	c.cacheConfig = connector.CacheConfig{Mode: "eager"}

	// Run a full Fetch — eager-mode hook should Put each attachment.
	// Drain the stream so the Fetch goroutine completes.
	if result := testutil.RunFetch(t, c, nil); result.Err != nil {
		t.Fatal(result.Err)
	}

	// Verify the attachment was cached without a FetchBinary call.
	ok, _ := store.Exists(context.Background(), "imap", c.name, "INBOX:1:attachment:0")
	if !ok {
		t.Error("eager mode: attachment should be cached after Fetch")
	}
	r, err := store.Get(context.Background(), "imap", c.name, "INBOX:1:attachment:0")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, payload) {
		t.Errorf("eager-cached bytes mismatch: got %q want %q", got, payload)
	}
}

func TestFetchBinary_InvalidSourceID(t *testing.T) {
	c := &Connector{name: "icloud"}
	for _, bad := range []string{"", "not-valid", "INBOX:", "INBOX:abc"} {
		if _, err := c.FetchBinary(context.Background(), bad); err == nil {
			t.Errorf("FetchBinary(%q): expected error for malformed source id", bad)
		}
	}
}

func TestFetchBinary_UIDNotFound(t *testing.T) {
	c, _, _, cleanup := newTestConnectorWithServer(t, "body",
		[]testAttachment{{filename: "f.bin", contentType: "application/octet-stream", data: []byte("x")}},
	)
	defer cleanup()

	_, err := c.FetchBinary(context.Background(), "INBOX:999")
	if err == nil {
		t.Error("expected error for unknown UID")
	}
}

func TestFetchBinary_AttachmentIndexOutOfRange(t *testing.T) {
	c, _, _, cleanup := newTestConnectorWithServer(t, "body",
		[]testAttachment{{filename: "f.bin", contentType: "application/octet-stream", data: []byte("x")}},
	)
	defer cleanup()

	_, err := c.FetchBinary(context.Background(), "INBOX:1:attachment:5")
	if err == nil {
		t.Error("expected error for out-of-range attachment index")
	}
}

// TestSetBinaryStore verifies the CacheAware wiring entry point sets
// both the store reference and the resolved policy.
func TestSetBinaryStore(t *testing.T) {
	c := &Connector{name: "x"}
	store := newFakeBinaryStore()
	cfg := connector.CacheConfig{Mode: "eager"}
	c.SetBinaryStore(store, cfg)
	if c.binaryStore != store {
		t.Error("binaryStore not wired")
	}
	if c.cacheConfig.Mode != "eager" {
		t.Errorf("cacheConfig.Mode = %q, want eager", c.cacheConfig.Mode)
	}
}

// TestFetchBinary_IMAPDialFailure exercises the error path when the
// IMAP connection can't be established.
func TestFetchBinary_IMAPDialFailure(t *testing.T) {
	c := &Connector{
		name:     "icloud",
		server:   "127.0.0.1",
		port:     1, // nothing listens
		username: "u", password: "p",
		dial: dialInsecure,
	}
	_, err := c.FetchBinary(context.Background(), "INBOX:1")
	if err == nil {
		t.Error("expected dial error")
	}
}

// TestFetchBinary_NoBinaryStore exercises the path where a connector
// has no cache wired up — FetchBinary still fetches from IMAP and
// returns the bytes without cache interaction.
func TestFetchBinary_NoBinaryStore(t *testing.T) {
	payload := []byte("no-cache")
	c, _, _, cleanup := newTestConnectorWithServer(t, "body",
		[]testAttachment{{filename: "f.bin", contentType: "application/octet-stream", data: payload}},
	)
	defer cleanup()
	c.binaryStore = nil
	c.cacheConfig = connector.CacheConfig{}

	bc, err := c.FetchBinary(context.Background(), "INBOX:1:attachment:0")
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Reader.Close() //nolint:errcheck // test cleanup
	got, _ := io.ReadAll(bc.Reader)
	if !bytes.Equal(got, payload) {
		t.Errorf("bytes mismatch with nil store")
	}
}
