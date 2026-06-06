package ical

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/muty/nexus/internal/model"
)

// fixedNow is the clock used across these tests for deterministic recurrence.
var fixedNow = time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)

func testConnector(syncSince time.Time) *Connector {
	return &Connector{name: "cal", syncSince: syncSince, now: func() time.Time { return fixedNow }}
}

// ics joins lines with CRLF and wraps them in a VCALENDAR.
func ics(lines ...string) *ical.Calendar {
	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" +
		strings.Join(lines, "\r\n") + "\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(body)).Decode()
	if err != nil {
		panic(err)
	}
	return cal
}

func docByTitle(docs []model.Document, title string) *model.Document {
	for i := range docs {
		if docs[i].Title == title {
			return &docs[i]
		}
	}
	return nil
}

func titles(docs []model.Document) []string {
	out := make([]string, len(docs))
	for i := range docs {
		out[i] = docs[i].Title
	}
	return out
}

func TestEventsToDocuments_OneOff(t *testing.T) {
	c := testConnector(time.Time{})
	cal := ics(
		"BEGIN:VEVENT",
		"UID:oneoff-1",
		"SUMMARY:Dentist appointment",
		"DESCRIPTION:Regular checkup",
		"LOCATION:123 Main St",
		"DTSTART:20260610T090000Z",
		"DTEND:20260610T093000Z",
		"END:VEVENT",
	)
	docs := c.eventsToDocuments(cal, "/u/calendars/personal/oneoff-1.ics", "/u/calendars/personal/", "personal")
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	d := docs[0]
	if d.Title != "Dentist appointment" {
		t.Errorf("title = %q", d.Title)
	}
	if d.SourceType != "ical" || d.SourceName != "cal" {
		t.Errorf("source mismatch: %+v", d)
	}
	if d.SourceID != "/u/calendars/personal/oneoff-1.ics" {
		t.Errorf("sourceID = %q", d.SourceID)
	}
	if !strings.Contains(d.Content, "Regular checkup") || !strings.Contains(d.Content, "123 Main St") {
		t.Errorf("content missing description/location: %q", d.Content)
	}
	if recurring, _ := d.Metadata["recurring"].(bool); recurring {
		t.Error("one-off should not be recurring")
	}
	want := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	if !d.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v (event DTSTART)", d.CreatedAt, want)
	}
}

func TestEventsToDocuments_Recurring(t *testing.T) {
	c := testConnector(time.Time{})
	// Weekly standup on Mondays, started before fixedNow (a Saturday).
	cal := ics(
		"BEGIN:VEVENT",
		"UID:standup-1",
		"SUMMARY:Team Standup",
		"DTSTART:20260601T090000Z",
		"DTEND:20260601T091500Z",
		"RRULE:FREQ=WEEKLY;BYDAY=MO",
		"END:VEVENT",
	)
	docs := c.eventsToDocuments(cal, "/u/calendars/work/standup-1.ics", "/u/calendars/work/", "work")
	if len(docs) != 1 {
		t.Fatalf("series-level expected 1 doc, got %d", len(docs))
	}
	d := docs[0]
	if recurring, _ := d.Metadata["recurring"].(bool); !recurring {
		t.Error("expected recurring=true")
	}
	if rrule, _ := d.Metadata["rrule"].(string); !strings.Contains(rrule, "FREQ=WEEKLY") {
		t.Errorf("rrule metadata = %q", rrule)
	}
	if _, ok := d.Metadata["next_occurrence"]; !ok {
		t.Error("expected next_occurrence in metadata")
	}
	if _, ok := d.Metadata["recent_occurrence"]; !ok {
		t.Error("expected recent_occurrence in metadata")
	}
	// CreatedAt anchors to the occurrence nearest fixedNow (2026-06-06 Sat):
	// recent Monday 06-01, next Monday 06-08. 06-08 is closer (2d vs 5d).
	wantNext := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC)
	if !d.CreatedAt.Equal(wantNext) {
		t.Errorf("CreatedAt = %v, want nearest occurrence %v", d.CreatedAt, wantNext)
	}
}

func TestEventsToDocuments_Override(t *testing.T) {
	c := testConnector(time.Time{})
	cal := ics(
		"BEGIN:VEVENT",
		"UID:series-1",
		"SUMMARY:Weekly Sync",
		"DTSTART:20260601T090000Z",
		"RRULE:FREQ=WEEKLY",
		"END:VEVENT",
		"BEGIN:VEVENT",
		"UID:series-1",
		"RECURRENCE-ID:20260608T090000Z",
		"SUMMARY:Weekly Sync (moved)",
		"DTSTART:20260608T140000Z",
		"END:VEVENT",
	)
	docs := c.eventsToDocuments(cal, "/u/calendars/work/series-1.ics", "/u/calendars/work/", "work")
	if len(docs) != 2 {
		t.Fatalf("expected master + override = 2 docs, got %d (%v)", len(docs), titles(docs))
	}
	master := docByTitle(docs, "Weekly Sync")
	override := docByTitle(docs, "Weekly Sync (moved)")
	if master == nil || override == nil {
		t.Fatalf("missing master or override; got %v", titles(docs))
	}
	if master.SourceID != "/u/calendars/work/series-1.ics" {
		t.Errorf("master sourceID = %q", master.SourceID)
	}
	// Override is a colon-suffixed child of the master href, so the pipeline's
	// colon-suffix rule preserves it when only the master is enumerated.
	if !strings.HasPrefix(override.SourceID, "/u/calendars/work/series-1.ics:") {
		t.Errorf("override sourceID should suffix with :recurrence-id, got %q", override.SourceID)
	}
}

func TestEventsToDocuments_OrganizerAttendeesStatus(t *testing.T) {
	c := testConnector(time.Time{})
	cal := ics(
		"BEGIN:VEVENT",
		"UID:meeting-1",
		"SUMMARY:Project kickoff",
		"DTSTART:20260610T090000Z",
		"STATUS:CONFIRMED",
		"ORGANIZER;CN=Alice Boss:mailto:alice@example.com",
		"ATTENDEE;CN=Bob Dev:mailto:bob@example.com",
		"ATTENDEE:mailto:carol@example.com",
		"END:VEVENT",
	)
	docs := c.eventsToDocuments(cal, "/u/calendars/work/meeting-1.ics", "/u/calendars/work/", "work")
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	d := docs[0]
	if d.Metadata["organizer"] != "Alice Boss" {
		t.Errorf("organizer = %v (want CN)", d.Metadata["organizer"])
	}
	att, _ := d.Metadata["attendees"].([]string)
	if len(att) != 2 || att[0] != "Bob Dev" || att[1] != "carol@example.com" {
		t.Errorf("attendees = %v (want CN then mailto-stripped)", att)
	}
	if d.Metadata["status"] != "CONFIRMED" {
		t.Errorf("status = %v", d.Metadata["status"])
	}
	if !strings.Contains(d.Content, "Organizer: Alice Boss") ||
		!strings.Contains(d.Content, "Attendees: Bob Dev, carol@example.com") {
		t.Errorf("content missing organizer/attendees lines: %q", d.Content)
	}
}

func TestEventsToDocuments_SyncSinceFiltersOldOneOff(t *testing.T) {
	c := testConnector(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	cal := ics(
		"BEGIN:VEVENT",
		"UID:ancient",
		"SUMMARY:Old meeting",
		"DTSTART:20250301T090000Z",
		"END:VEVENT",
	)
	docs := c.eventsToDocuments(cal, "/u/calendars/personal/ancient.ics", "/u/calendars/personal/", "personal")
	if len(docs) != 0 {
		t.Errorf("event before sync_since should be filtered, got %d docs", len(docs))
	}
}

func TestEventsToDocuments_NonIANATZID(t *testing.T) {
	c := testConnector(time.Time{})
	// Outlook/Teams events use Microsoft timezone names, which aren't IANA, so
	// go-ical's typed getter errors — the connector must fall back to parsing
	// the raw value so the event still gets a date (CreatedAt + dtstart meta).
	cal := ics(
		"BEGIN:VEVENT",
		"UID:teams-1",
		"SUMMARY:Teams sync",
		"DTSTART;TZID=W. Europe Standard Time:20260610T090000",
		"DTEND;TZID=W. Europe Standard Time:20260610T093000",
		"END:VEVENT",
	)
	docs := c.eventsToDocuments(cal, "/u/calendars/work/teams-1.ics", "/u/calendars/work/", "work")
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	d := docs[0]
	want := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	if !d.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v (fallback-parsed)", d.CreatedAt, want)
	}
	if d.Metadata["dtstart"] != want.Format(time.RFC3339) {
		t.Errorf("dtstart meta = %v, want %v", d.Metadata["dtstart"], want.Format(time.RFC3339))
	}
}

func TestReadStringSlice(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want []string
	}{
		{"json array", []any{"a", "b"}, []string{"a", "b"}},
		{"string slice", []string{"x"}, []string{"x"}},
		{"comma string", "p1, p2 ,p3", []string{"p1", "p2", "p3"}},
		{"empty", "", nil},
		{"missing", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{}
			if tc.val != nil {
				cfg["calendars"] = tc.val
			}
			got := readStringSlice(cfg, "calendars")
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
