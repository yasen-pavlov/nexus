package paperless

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

// TestInitialDocsURL_NoCursorNoSyncSince covers the default path:
// no cursor, no syncSince cutoff → no modified__gt filter.
func TestInitialDocsURL_NoCursorNoSyncSince(t *testing.T) {
	c := &Connector{baseURL: "http://p"}
	got := c.initialDocsURL(nil)
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("modified__gt") != "" {
		t.Errorf("expected no modified__gt, got %s", u.Query().Get("modified__gt"))
	}
	if u.Query().Get("ordering") != "modified" {
		t.Errorf("ordering = %q, want modified", u.Query().Get("ordering"))
	}
}

// TestInitialDocsURL_CursorWins: a persisted cursor's
// last_sync_time takes precedence over syncSince.
func TestInitialDocsURL_CursorWins(t *testing.T) {
	c := &Connector{baseURL: "http://p", syncSince: time.Now()}
	cursor := &model.SyncCursor{
		CursorData: map[string]any{
			"last_sync_time": "2026-01-01T00:00:00Z",
		},
	}
	got := c.initialDocsURL(cursor)
	if !strings.Contains(got, "modified__gt=2026-01-01") {
		t.Errorf("expected cursor date in URL, got %q", got)
	}
}

// TestInitialDocsURL_SyncSinceFallback: with no cursor but a
// syncSince cutoff, that cutoff becomes the modified__gt bound.
func TestInitialDocsURL_SyncSinceFallback(t *testing.T) {
	since := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	c := &Connector{baseURL: "http://p", syncSince: since}
	got := c.initialDocsURL(nil)
	u, _ := url.Parse(got)
	if u.Query().Get("modified__gt") != since.Format(time.RFC3339) {
		t.Errorf("modified__gt = %q, want %q", u.Query().Get("modified__gt"), since.Format(time.RFC3339))
	}
}

// TestInitialDocsURL_CursorWithoutLastSyncTime: a cursor present
// but with unrecognized data falls through to syncSince (or empty
// when syncSince is zero).
func TestInitialDocsURL_CursorWithoutLastSyncTime(t *testing.T) {
	c := &Connector{baseURL: "http://p"}
	cursor := &model.SyncCursor{CursorData: map[string]any{"other": "x"}}
	got := c.initialDocsURL(cursor)
	u, _ := url.Parse(got)
	if u.Query().Get("modified__gt") != "" {
		t.Errorf("expected no filter, got %q", u.Query().Get("modified__gt"))
	}
}

// TestNewPaperlessCursor stamps the provided time into both
// last_sync_time and LastSync, and marks the run successful.
func TestNewPaperlessCursor(t *testing.T) {
	ts := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	cur := newPaperlessCursor(ts)
	if got, _ := cur.CursorData["last_sync_time"].(string); got != ts.Format(time.RFC3339Nano) {
		t.Errorf("last_sync_time = %q, want %q", got, ts.Format(time.RFC3339Nano))
	}
	if !cur.LastSync.Equal(ts) {
		t.Errorf("LastSync = %v, want %v", cur.LastSync, ts)
	}
	if cur.LastStatus != "success" {
		t.Errorf("LastStatus = %q, want success", cur.LastStatus)
	}
}
