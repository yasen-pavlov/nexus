package ical

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"github.com/muty/nexus/internal/model"
)

// event decodes a single VEVENT (wrapped in a VCALENDAR) for helper tests.
func event(lines ...string) ical.Event {
	return ics(append([]string{"BEGIN:VEVENT"}, append(lines, "END:VEVENT")...)...).Events()[0]
}

func TestEmitItem(t *testing.T) {
	// Open (buffered) channel — the send wins.
	ch := make(chan model.FetchItem, 1)
	if !emitItem(context.Background(), ch, model.FetchItem{}) {
		t.Error("expected true when the item is sent")
	}

	// Cancelled context with no receiver — the ctx.Done branch wins.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if emitItem(ctx, make(chan model.FetchItem), model.FetchItem{}) {
		t.Error("expected false when the context is cancelled before send")
	}
}

func TestCursorString(t *testing.T) {
	if got := cursorString(nil, "k"); got != "" {
		t.Errorf("nil cursor = %q, want empty", got)
	}
	cur := &model.SyncCursor{CursorData: map[string]any{"k": "v", "n": 5}}
	if got := cursorString(cur, "k"); got != "v" {
		t.Errorf("present key = %q, want v", got)
	}
	if got := cursorString(cur, "n"); got != "" {
		t.Errorf("non-string value = %q, want empty", got)
	}
	if got := cursorString(cur, "missing"); got != "" {
		t.Errorf("missing key = %q, want empty", got)
	}
}

func TestLoadManifest(t *testing.T) {
	if m := loadManifest(nil); len(m) != 0 {
		t.Errorf("nil cursor = %v, want empty", m)
	}
	if m := loadManifest(&model.SyncCursor{CursorData: map[string]any{}}); len(m) != 0 {
		t.Errorf("missing manifest = %v, want empty", m)
	}
	if m := loadManifest(&model.SyncCursor{CursorData: map[string]any{"manifest": ""}}); len(m) != 0 {
		t.Errorf("empty manifest = %v, want empty", m)
	}
	if m := loadManifest(&model.SyncCursor{CursorData: map[string]any{"manifest": "not json"}}); len(m) != 0 {
		t.Errorf("invalid json = %v, want empty", m)
	}
	m := loadManifest(&model.SyncCursor{CursorData: map[string]any{
		"manifest": `{"/cal/":{"/cal/a.ics":"etag-1"}}`,
	}})
	if m["/cal/"]["/cal/a.ics"] != "etag-1" {
		t.Errorf("round-tripped manifest = %v, want etag-1", m)
	}
}

// TestNearest (covering next/recent/zero cases) lives in connector_test.go.
// This covers the remaining default branch: a recent occurrence nearer than
// the next one.
func TestNearestRecentCloser(t *testing.T) {
	now := fixedNow
	next := now.Add(2 * time.Hour)
	closerRecent := now.Add(-1 * time.Hour)
	if got := nearest(now, next, closerRecent); !got.Equal(closerRecent) {
		t.Errorf("recent closer (1h<2h) = %v, want recent", got)
	}
}

func TestOrganizer(t *testing.T) {
	if got := organizer(event("SUMMARY:no organizer", "DTSTART:20260610T090000Z")); got != "" {
		t.Errorf("absent organizer = %q, want empty", got)
	}
	withCN := event("SUMMARY:x", "ORGANIZER;CN=Alice Boss:mailto:alice@example.com")
	if got := organizer(withCN); got != "Alice Boss" {
		t.Errorf("CN organizer = %q, want Alice Boss", got)
	}
	mailtoOnly := event("SUMMARY:x", "ORGANIZER:mailto:bob@example.com")
	if got := organizer(mailtoOnly); got != "bob@example.com" {
		t.Errorf("mailto organizer = %q, want stripped address", got)
	}
}

func TestBuildDocumentUntitled(t *testing.T) {
	c := testConnector(time.Time{})
	docs := c.eventsToDocuments(
		ics("BEGIN:VEVENT", "UID:no-title", "DTSTART:20260610T090000Z", "END:VEVENT"),
		"/cal/no-title.ics", "/cal/", "cal",
	)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].Title != "(untitled event)" {
		t.Errorf("title = %q, want (untitled event)", docs[0].Title)
	}
}

func TestEventTimeAbsentProp(t *testing.T) {
	ev := event("SUMMARY:x", "DTSTART:20260610T090000Z")
	if got := eventEnd(ev); !got.IsZero() {
		t.Errorf("absent DTEND = %v, want zero", got)
	}
}

func TestCalShort(t *testing.T) {
	cases := map[string]string{
		"/u/calendars/work/": "work",
		"/u/calendars/work":  "work",
		"personal":           "personal",
		"":                   "",
	}
	for in, want := range cases {
		if got := calShort(in); got != want {
			t.Errorf("calShort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSourceID(t *testing.T) {
	if got := sourceID("/cal/a.ics", ""); got != "/cal/a.ics" {
		t.Errorf("master sourceID = %q", got)
	}
	if got := sourceID("/cal/a.ics", "20260608T090000Z"); got != "/cal/a.ics:20260608T090000Z" {
		t.Errorf("override sourceID = %q", got)
	}
}

func TestNewCursorAndSyncSinceKey(t *testing.T) {
	// No sync window, no manifest: timestamp-only checkpoint.
	c := testConnector(time.Time{})
	cur := c.newCursor(nil)
	if cur.CursorData["sync_since"] != "" {
		t.Errorf("zero syncSince key = %v, want empty", cur.CursorData["sync_since"])
	}
	if _, ok := cur.CursorData["manifest"]; ok {
		t.Error("nil manifest should not persist a manifest key")
	}
	if !cur.LastSync.Equal(fixedNow) || cur.LastStatus != "success" {
		t.Errorf("cursor checkpoint = %+v", cur)
	}

	// With a window + manifest: both persist.
	c2 := testConnector(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	cur2 := c2.newCursor(manifest{"/cal/": {"/cal/a.ics": "etag"}})
	if cur2.CursorData["sync_since"] != "2026-01-02" {
		t.Errorf("syncSince key = %v, want 2026-01-02", cur2.CursorData["sync_since"])
	}
	if loadManifest(cur2)["/cal/"]["/cal/a.ics"] != "etag" {
		t.Errorf("manifest did not round-trip through newCursor: %v", cur2.CursorData["manifest"])
	}
}

func TestStrPtr(t *testing.T) {
	if p := strPtr("hello"); p == nil || *p != "hello" {
		t.Errorf("strPtr = %v", p)
	}
}

func TestCaldavClientBadEndpoint(t *testing.T) {
	c := &Connector{endpoint: "://bad", client: http.DefaultClient}
	if _, err := c.caldavClient(); err == nil {
		t.Error("expected an error for an unparseable endpoint")
	}
}

// TestServer_FetchCancelled drives Fetch with an already-cancelled context to
// exercise the cancellation branches across Fetch/streamFetch/syncCalendar.
func TestServer_FetchCancelled(t *testing.T) {
	b := &fakeBackend{objects: []caldav.CalendarObject{
		makeObject(t, fakeCalPath+"e1.ics", "etag-1",
			"BEGIN:VEVENT", "UID:e1", "DTSTAMP:20260101T000000Z",
			"SUMMARY:Lunch", "DTSTART:20260610T120000Z", "END:VEVENT"),
	}}
	c := newFakeServer(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	items, errs := c.Fetch(ctx, nil)
	for range items { //nolint:revive // draining the channel
	}
	<-errs // a cancelled run closes errs (nil or context error both acceptable)
}

func TestRecurrenceAnchors(t *testing.T) {
	// Open-ended weekly series: next + recent + anchor all resolve.
	c := testConnector(time.Time{})
	ev := event("SUMMARY:x", "DTSTART:20260601T090000Z", "RRULE:FREQ=WEEKLY")
	anchor, next, recent, ok := c.recurrenceAnchors(ev, eventStart(ev))
	if !ok || anchor.IsZero() || next.IsZero() || recent.IsZero() {
		t.Fatalf("open series: ok=%v anchor=%v next=%v recent=%v", ok, anchor, next, recent)
	}

	// Bounded series entirely before a far-future sync window → no occurrence
	// in range → ok=false.
	cFuture := testConnector(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	bounded := event("SUMMARY:x", "DTSTART:20260601T090000Z", "RRULE:FREQ=DAILY;COUNT=3")
	if _, _, _, ok := cFuture.recurrenceAnchors(bounded, eventStart(bounded)); ok {
		t.Error("expected ok=false when the series ends before the sync window")
	}

	// Non-IANA TZID makes RecurrenceSet fail → occurrence math is skipped and the
	// anchor falls back to dtstart.
	tz := event("SUMMARY:x",
		"DTSTART;TZID=W. Europe Standard Time:20260601T090000", "RRULE:FREQ=WEEKLY")
	dtstart := eventStart(tz)
	anchor, next, recent, ok = c.recurrenceAnchors(tz, dtstart)
	if !ok || !anchor.Equal(dtstart) || !next.IsZero() || !recent.IsZero() {
		t.Errorf("TZID fallback: ok=%v anchor=%v (want %v) next=%v recent=%v",
			ok, anchor, dtstart, next, recent)
	}
}

func TestValidateRequiresCredentials(t *testing.T) {
	c := &Connector{endpoint: "https://caldav.icloud.com"}
	if err := c.Validate(); err == nil {
		t.Error("expected an error when username/password are empty")
	}
	c.username = "alice@example.com"
	if err := c.Validate(); err == nil {
		t.Error("expected an error when only the username is set")
	}
}
