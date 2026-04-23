package telegram

import (
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

// TestResolveSinceDate_NilCursorNoSyncSince: no bound at all →
// zero, meaning "paginate all history".
func TestResolveSinceDate_NilCursorNoSyncSince(t *testing.T) {
	c := &Connector{}
	if got := c.resolveSinceDate(nil); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

// TestResolveSinceDate_CursorWins: a persisted
// last_message_date on the cursor takes precedence over the
// connector-level syncSince cutoff.
func TestResolveSinceDate_CursorWins(t *testing.T) {
	c := &Connector{syncSince: time.Unix(100, 0)}
	cursor := &model.SyncCursor{
		CursorData: map[string]any{
			"last_message_date": float64(500),
		},
	}
	if got := c.resolveSinceDate(cursor); got != 500 {
		t.Errorf("expected 500, got %d", got)
	}
}

// TestResolveSinceDate_SyncSinceFallback: no cursor, but a
// syncSince cutoff → cutoff wins.
func TestResolveSinceDate_SyncSinceFallback(t *testing.T) {
	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &Connector{syncSince: since}
	if got := c.resolveSinceDate(nil); got != int(since.Unix()) {
		t.Errorf("expected %d, got %d", since.Unix(), got)
	}
}

// TestResolveSinceDate_CursorWithoutLastMessageDate: a cursor
// shape that doesn't carry the expected key falls through.
func TestResolveSinceDate_CursorWithoutLastMessageDate(t *testing.T) {
	c := &Connector{}
	cursor := &model.SyncCursor{CursorData: map[string]any{"other": "x"}}
	if got := c.resolveSinceDate(cursor); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}
