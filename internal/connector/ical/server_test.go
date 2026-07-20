package ical

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/muty/nexus/internal/model"
)

// fakeBackend is a minimal in-memory read-only CalDAV backend used to exercise
// the connector's discovery + Fetch round-trip without a real server.
type fakeBackend struct {
	objects []caldav.CalendarObject // resources in the single calendar
}

const (
	fakePrincipal = "/principal/"
	fakeHomeSet   = "/principal/calendars/"
	fakeCalPath   = "/principal/calendars/work/"
)

func (b *fakeBackend) CurrentUserPrincipal(_ context.Context) (string, error) {
	return fakePrincipal, nil
}
func (b *fakeBackend) CalendarHomeSetPath(_ context.Context) (string, error) {
	return fakeHomeSet, nil
}
func (b *fakeBackend) ListCalendars(_ context.Context) ([]caldav.Calendar, error) {
	return []caldav.Calendar{{
		Path: fakeCalPath, Name: "Work",
		SupportedComponentSet: []string{"VEVENT"},
	}}, nil
}
func (b *fakeBackend) GetCalendar(_ context.Context, path string) (*caldav.Calendar, error) {
	return &caldav.Calendar{Path: path, Name: "Work", SupportedComponentSet: []string{"VEVENT"}}, nil
}
func (b *fakeBackend) GetCalendarObject(_ context.Context, path string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	for i := range b.objects {
		if b.objects[i].Path == path {
			return &b.objects[i], nil
		}
	}
	return nil, webdav.NewHTTPError(http.StatusNotFound, nil)
}
func (b *fakeBackend) ListCalendarObjects(_ context.Context, _ string, _ *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	return b.objects, nil
}
func (b *fakeBackend) QueryCalendarObjects(_ context.Context, _ string, _ *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	return b.objects, nil
}

// write paths — unused by the read-only connector.
func (b *fakeBackend) CreateCalendar(context.Context, *caldav.Calendar) error { return nil }
func (b *fakeBackend) PutCalendarObject(context.Context, string, *ical.Calendar, *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	return nil, webdav.NewHTTPError(http.StatusForbidden, nil)
}
func (b *fakeBackend) DeleteCalendarObject(context.Context, string) error { return nil }

func makeObject(t *testing.T, path, etag string, lines ...string) caldav.CalendarObject {
	t.Helper()
	cal := ics(lines...)
	// ContentLength must be the real encoded length: the server's PROPFIND
	// advertises it (ReadDir requires getcontentlength) AND its GET sets it as
	// the Content-Length header, so a wrong value truncates the body.
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return caldav.CalendarObject{Path: path, ETag: etag, Data: cal, ContentLength: int64(buf.Len())}
}

func newFakeServer(t *testing.T, b *fakeBackend) *Connector {
	t.Helper()
	handler := &caldav.Handler{Backend: b}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c := &Connector{
		name:      "cal",
		username:  "u",
		password:  "p",
		endpoint:  srv.URL,
		client:    srv.Client(),
		calendars: []string{fakeCalPath},
		now:       func() time.Time { return fixedNow },
	}
	return c
}

func TestServer_Discover(t *testing.T) {
	c := newFakeServer(t, &fakeBackend{})
	res, err := c.DiscoverResources(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(res) != 1 || res[0].Name != "Work" || res[0].ID != fakeCalPath {
		t.Fatalf("unexpected discovery: %+v", res)
	}
}

func TestServer_DiscoverFiltersNonEvents(t *testing.T) {
	// A backend whose only calendar holds VTODOs (reminders) — discovery must
	// filter it out since this connector indexes events.
	b := &fakeBackend{}
	c := newFakeServer(t, b)
	c2 := *c // copy; override the listing via a reminders-only backend
	rb := &remindersBackend{}
	handler := &caldav.Handler{Backend: rb}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c2.endpoint = srv.URL
	c2.client = srv.Client()

	res, err := c2.DiscoverResources(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("VTODO-only calendar should be filtered out, got %+v", res)
	}
}

// remindersBackend is a fakeBackend whose calendar supports only VTODO.
type remindersBackend struct{ fakeBackend }

func (b *remindersBackend) ListCalendars(_ context.Context) ([]caldav.Calendar, error) {
	return []caldav.Calendar{{Path: "/principal/calendars/reminders/", Name: "Reminders", SupportedComponentSet: []string{"VTODO"}}}, nil
}

func TestListCalendarETags_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Connector{username: "u", password: "p", endpoint: srv.URL, client: srv.Client()}
	if _, err := c.listCalendarETags(context.Background(), "/cal/"); err == nil {
		t.Error("a non-207 REPORT response should error")
	}
}

func TestServer_Validate(t *testing.T) {
	c := newFakeServer(t, &fakeBackend{})
	if err := c.Validate(); err != nil {
		t.Errorf("validate against fake server: %v", err)
	}
}

// collectFetch drains a Fetch run into its parts.
func collectFetch(t *testing.T, c *Connector, cursor *model.SyncCursor) (docs []model.Document, sourceIDs []string, lastCursor *model.SyncCursor) {
	t.Helper()
	items, errs := c.Fetch(context.Background(), cursor)
	for it := range items {
		switch {
		case it.Doc != nil:
			docs = append(docs, *it.Doc)
		case it.SourceID != nil:
			sourceIDs = append(sourceIDs, *it.SourceID)
		case it.Checkpoint != nil:
			// Regression guard: every emitted checkpoint must carry the ETag
			// manifest. A manifest-less checkpoint (the old mid-run emission)
			// would overwrite cursor_data wholesale and erase the persisted
			// manifest, forcing a full re-GET of every resource next run.
			if _, ok := it.Checkpoint.CursorData["manifest"]; !ok {
				t.Errorf("checkpoint missing manifest key (would erase ETag state): %v", it.Checkpoint.CursorData)
			}
			lastCursor = it.Checkpoint
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("fetch error: %v", err)
	}
	return docs, sourceIDs, lastCursor
}

func TestServer_FetchAndIncremental(t *testing.T) {
	b := &fakeBackend{objects: []caldav.CalendarObject{
		makeObject(t, fakeCalPath+"e1.ics", "etag-1",
			"BEGIN:VEVENT", "UID:e1", "DTSTAMP:20260101T000000Z", "SUMMARY:Lunch", "DTSTART:20260610T120000Z", "END:VEVENT"),
		makeObject(t, fakeCalPath+"e2.ics", "etag-2",
			"BEGIN:VEVENT", "UID:e2", "DTSTAMP:20260101T000000Z", "SUMMARY:Standup", "DTSTART:20260601T090000Z", "RRULE:FREQ=WEEKLY", "END:VEVENT"),
	}}
	c := newFakeServer(t, b)

	// First sync: both events fetched + enumerated.
	docs, sids, cur := collectFetch(t, c, nil)
	if len(docs) != 2 {
		t.Fatalf("first sync: expected 2 docs, got %d", len(docs))
	}
	if len(sids) != 2 {
		t.Fatalf("first sync: expected 2 source ids, got %d (%v)", len(sids), sids)
	}
	// SourceIDs must be lex-sorted.
	if !isLexSorted(sids) {
		t.Errorf("source ids not lex-sorted: %v", sids)
	}
	if cur == nil {
		t.Fatal("expected a final checkpoint cursor")
	}

	// Second sync with the cursor and UNCHANGED ETags: no docs re-emitted, but
	// the full SourceID set is still enumerated.
	docs2, sids2, _ := collectFetch(t, c, cur)
	if len(docs2) != 0 {
		t.Errorf("incremental: expected 0 re-emitted docs, got %d", len(docs2))
	}
	if len(sids2) != 2 {
		t.Errorf("incremental: expected 2 enumerated source ids, got %d", len(sids2))
	}

	// Third sync after one ETag changes: only that resource re-emits.
	b.objects[0].ETag = "etag-1b"
	docs3, _, _ := collectFetch(t, c, cur)
	if len(docs3) != 1 || docs3[0].Title != "Lunch" {
		t.Errorf("after etag change: expected only Lunch re-emitted, got %v", titles(docs3))
	}
}

// TestServer_TruncationGuard verifies that when the prior manifest knew of many
// resources but the listing now returns very few, deletion is opted out (no
// SourceID enumeration) while still indexing what was fetched.
func TestServer_TruncationGuard(t *testing.T) {
	b := &fakeBackend{objects: []caldav.CalendarObject{
		makeObject(t, fakeCalPath+"e1.ics", "etag-1",
			"BEGIN:VEVENT", "UID:e1", "DTSTAMP:20260101T000000Z", "SUMMARY:E1", "DTSTART:20260601T090000Z", "END:VEVENT"),
		makeObject(t, fakeCalPath+"e2.ics", "etag-2",
			"BEGIN:VEVENT", "UID:e2", "DTSTAMP:20260101T000000Z", "SUMMARY:E2", "DTSTART:20260601T090000Z", "END:VEVENT"),
	}}
	c := newFakeServer(t, b)

	// Prior manifest claims 40 hrefs for this calendar; the live listing now
	// returns only 2 → suspicious shrink → guard trips.
	big := manifest{fakeCalPath: {}}
	for i := 0; i < 40; i++ {
		big[fakeCalPath][fmt.Sprintf("/h%d.ics", i)] = "e"
	}
	docs, sids, _ := collectFetch(t, c, c.newCursor(big))
	if len(sids) != 0 {
		t.Errorf("guard should suppress SourceID enumeration, got %d", len(sids))
	}
	if len(docs) != 2 {
		t.Errorf("guard should still index fetched docs, got %d", len(docs))
	}
}

// TestServer_FetchUsesDisplayName verifies that synced event documents carry
// the calendar's CalDAV display name ("Work") rather than the opaque last path
// segment ("work") — the fix for iCloud calendars whose URLs end in a UUID.
func TestServer_FetchUsesDisplayName(t *testing.T) {
	b := &fakeBackend{objects: []caldav.CalendarObject{
		makeObject(t, fakeCalPath+"e1.ics", "etag-1",
			"BEGIN:VEVENT", "UID:e1", "DTSTAMP:20260101T000000Z", "SUMMARY:Lunch", "DTSTART:20260610T120000Z", "END:VEVENT"),
	}}
	c := newFakeServer(t, b)

	docs, _, _ := collectFetch(t, c, nil)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].Metadata["calendar"] != "Work" {
		t.Errorf("calendar meta = %v, want display name %q (not the path segment)", docs[0].Metadata["calendar"], "Work")
	}
}

func TestServer_EmptySelectionClears(t *testing.T) {
	c := newFakeServer(t, &fakeBackend{})
	c.calendars = nil // nothing selected
	docs, sids, _ := collectFetch(t, c, nil)
	if len(docs) != 0 || len(sids) != 0 {
		t.Errorf("empty selection should emit nothing, got %d docs / %d sids", len(docs), len(sids))
	}
}

// TestGetCalendarData_RetriesThrottle verifies a 429 with Retry-After is
// retried (not skipped) and the resource eventually fetched.
func TestGetCalendarData_RetriesThrottle(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = io.WriteString(w, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n"+
			"BEGIN:VEVENT\r\nUID:x\r\nDTSTAMP:20260101T000000Z\r\nSUMMARY:Hi\r\nDTSTART:20260101T000000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	}))
	defer srv.Close()

	c := &Connector{username: "u", password: "p", endpoint: srv.URL, client: srv.Client(), now: time.Now}
	cal, err := c.getCalendarData(context.Background(), "/x.ics")
	if err != nil {
		t.Fatalf("getCalendarData: %v", err)
	}
	if cal == nil || len(cal.Events()) != 1 {
		t.Fatalf("expected 1 event after retry, got %v", cal)
	}
	if calls < 2 {
		t.Errorf("expected a retry (>=2 calls), got %d", calls)
	}
}

// TestGetCalendarData_StatusHandling verifies a 404 is skipped (nil,nil) and a
// 500 surfaces as an error.
func TestGetCalendarData_StatusHandling(t *testing.T) {
	t.Run("404 skips", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		c := &Connector{username: "u", password: "p", endpoint: srv.URL, client: srv.Client(), now: time.Now}
		cal, err := c.getCalendarData(context.Background(), "/gone.ics")
		if err != nil || cal != nil {
			t.Errorf("404 should be (nil,nil), got cal=%v err=%v", cal, err)
		}
	})
	t.Run("500 errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := &Connector{username: "u", password: "p", endpoint: srv.URL, client: srv.Client(), now: time.Now}
		if _, err := c.getCalendarData(context.Background(), "/x.ics"); err == nil {
			t.Error("500 should surface as an error")
		}
	})
}

func isLexSorted(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}
