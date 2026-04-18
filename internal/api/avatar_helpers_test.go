package api

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestConnectorHasAvatar_FalseWithoutBinaryStore(t *testing.T) {
	h := &handler{log: zap.NewNop()}
	if h.connectorHasAvatar(context.Background(), "telegram", "tg", "9001") {
		t.Errorf("expected false when binaryStore is nil")
	}
}

func TestConnectorHasAvatar_FalseForUnsupportedSource(t *testing.T) {
	// binaryStore being non-nil isn't strictly required — avatarCacheKey
	// returns ok=false first. Pass a zero handler; the early return hits
	// before any nil deref.
	h := &handler{log: zap.NewNop()}
	if h.connectorHasAvatar(context.Background(), "filesystem", "fs", "anything") {
		t.Errorf("expected false for unsupported source type")
	}
}

func TestAvatarCacheKey(t *testing.T) {
	cases := []struct {
		name       string
		sourceType string
		externalID string
		wantKey    string
		wantOK     bool
	}{
		{"telegram numeric", "telegram", "9001", "avatars:9001", true},
		{"telegram non-numeric rejected", "telegram", "abc", "", false},
		{"unsupported source", "imap", "alice@example.com", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := avatarCacheKey(tc.sourceType, tc.externalID)
			if ok != tc.wantOK {
				t.Errorf("avatarCacheKey ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.wantKey {
				t.Errorf("avatarCacheKey key = %q, want %q", got, tc.wantKey)
			}
		})
	}
}
