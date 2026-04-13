package telegram

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/connector"
)

// errorBinaryStore is a BinaryStoreAPI whose Get always fails with a
// non-os.ErrNotExist error — used to exercise the "cache read:" error
// wrapping path in FetchBinary.
type errorBinaryStore struct{ err error }

func (e *errorBinaryStore) Put(_ context.Context, _, _, _ string, _ io.Reader, _ int64) error {
	return nil
}

func (e *errorBinaryStore) Get(_ context.Context, _, _, _ string) (io.ReadCloser, error) {
	return nil, e.err
}

func (e *errorBinaryStore) Exists(_ context.Context, _, _, _ string) (bool, error) {
	return false, nil
}

func TestMediaSourceIDRe(t *testing.T) {
	valid := []string{
		"123:456:media",
		"1:1:media",
		"-1002000000000:42:media", // supergroup-style negative ID
	}
	invalid := []string{
		"",
		"123:456",            // no :media
		"123:456:attachment", // wrong suffix
		"123:abc:media",      // non-numeric msg id
		"123:456:media:extra",
	}
	for _, s := range valid {
		if !mediaSourceIDRe.MatchString(s) {
			t.Errorf("expected %q to match", s)
		}
	}
	for _, s := range invalid {
		if mediaSourceIDRe.MatchString(s) {
			t.Errorf("expected %q to NOT match", s)
		}
	}
}

func TestFetchBinary_CacheHit(t *testing.T) {
	store := newFakeBinaryStore()
	_ = store.Put(context.Background(), "telegram", "tg", "55:42:media", strings.NewReader("hello"), 5)
	c := &Connector{
		name:        "tg",
		binaryStore: store,
		cacheConfig: connector.CacheConfig{Mode: "eager"},
	}
	bc, err := c.FetchBinary(context.Background(), "55:42:media")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	defer bc.Reader.Close() //nolint:errcheck // test
	body, err := io.ReadAll(bc.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Errorf("body = %q, want hello", body)
	}
	// MimeType + Filename intentionally empty on cache hit — the
	// download handler synthesizes fallbacks from chunk.Title.
	if bc.MimeType != "" || bc.Filename != "" {
		t.Errorf("unexpected mime/filename: %+v", bc)
	}
}

func TestFetchBinary_CacheMiss_ClearError(t *testing.T) {
	store := newFakeBinaryStore()
	c := &Connector{
		name:        "tg",
		binaryStore: store,
		cacheConfig: connector.CacheConfig{Mode: "eager"},
	}
	_, err := c.FetchBinary(context.Background(), "99:1:media")
	if err == nil {
		t.Fatal("expected error on cache miss")
	}
	if !strings.Contains(err.Error(), "not cached") {
		t.Errorf("error message should mention cache miss: %v", err)
	}
	if !strings.Contains(err.Error(), "re-sync") {
		t.Errorf("error message should suggest re-sync remedy: %v", err)
	}
}

func TestFetchBinary_InvalidSourceID(t *testing.T) {
	c := &Connector{name: "tg", binaryStore: newFakeBinaryStore(), cacheConfig: connector.CacheConfig{Mode: "eager"}}
	if _, err := c.FetchBinary(context.Background(), "not-a-valid-id"); err == nil {
		t.Error("expected error for malformed source id")
	}
}

func TestFetchBinary_NoStore_ReturnsError(t *testing.T) {
	c := &Connector{name: "tg"} // no binaryStore, no cacheConfig
	_, err := c.FetchBinary(context.Background(), "1:2:media")
	if err == nil {
		t.Fatal("expected error when cache disabled")
	}
	if !strings.Contains(err.Error(), "cache disabled") {
		t.Errorf("error should mention cache disabled: %v", err)
	}
}

func TestFetchBinary_CacheReadError_WrapsError(t *testing.T) {
	// A non-miss error from BinaryStore.Get should be wrapped with the
	// "cache read:" prefix so operators can distinguish an unexpected
	// store failure from a plain miss.
	c := &Connector{
		name:        "tg",
		binaryStore: &errorBinaryStore{err: errors.New("disk on fire")},
		cacheConfig: connector.CacheConfig{Mode: "eager"},
	}
	_, err := c.FetchBinary(context.Background(), "1:2:media")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cache read") {
		t.Errorf("error should be wrapped with 'cache read:' prefix: %v", err)
	}
	if !strings.Contains(err.Error(), "disk on fire") {
		t.Errorf("error should propagate underlying cause: %v", err)
	}
}

func TestFetchBinary_CacheModeNone_ReturnsError(t *testing.T) {
	c := &Connector{
		name:        "tg",
		binaryStore: newFakeBinaryStore(),
		cacheConfig: connector.CacheConfig{Mode: "none"},
	}
	if _, err := c.FetchBinary(context.Background(), "1:2:media"); err == nil {
		t.Fatal("expected error when cache mode is none")
	}
}
