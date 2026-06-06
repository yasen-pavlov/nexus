package ical

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-webdav/caldav"
	"github.com/muty/nexus/internal/connector"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/netguard"
)

func TestConfigure(t *testing.T) {
	c := &Connector{endpoint: defaultEndpoint, now: time.Now}
	err := c.Configure(connector.Config{
		"name":       "my-cal",
		"username":   "me@icloud.com",
		"password":   "app-pw",
		"endpoint":   "https://caldav.example.com/",
		"calendars":  []any{"/a/", "/b/"},
		"sync_since": "2025-01-01",
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if c.Name() != "my-cal" || c.Type() != "ical" {
		t.Errorf("name/type = %q/%q", c.Name(), c.Type())
	}
	if c.username != "me@icloud.com" || c.password != "app-pw" {
		t.Errorf("creds not set: %+v", c)
	}
	if c.endpoint != "https://caldav.example.com" { // trailing slash trimmed
		t.Errorf("endpoint = %q", c.endpoint)
	}
	if len(c.calendars) != 2 {
		t.Errorf("calendars = %v", c.calendars)
	}
	if c.syncSince.IsZero() {
		t.Error("sync_since not parsed")
	}
}

func TestConfigure_DefaultsAndRequired(t *testing.T) {
	c := &Connector{endpoint: defaultEndpoint, now: time.Now}
	if err := c.Configure(connector.Config{"username": "u", "password": "p"}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if c.Name() != "ical" {
		t.Errorf("default name = %q", c.Name())
	}
	if c.endpoint != defaultEndpoint {
		t.Errorf("endpoint should default, got %q", c.endpoint)
	}

	if err := c.Configure(connector.Config{"password": "p"}); err == nil {
		t.Error("expected error for missing username")
	}
	if err := c.Configure(connector.Config{"username": "u"}); err == nil {
		t.Error("expected error for missing password")
	}
}

func TestValidate_RequiresCreds(t *testing.T) {
	c := &Connector{}
	if err := c.Validate(); err == nil {
		t.Error("expected error for missing creds")
	}
}

// TestValidate_NetworkError exercises the unreachable-endpoint branch: the
// netguard client refuses the loopback target, so FindCurrentUserPrincipal
// errors and Validate returns the "cannot reach" path.
func TestValidate_NetworkError(t *testing.T) {
	c := &Connector{
		username: "u", password: "p",
		endpoint: "http://127.0.0.1:1",
		client:   netguard.NewClient(2 * time.Second),
		now:      time.Now,
	}
	if err := c.Validate(); err == nil {
		t.Error("expected error against an unreachable endpoint")
	}
}

func TestDiscoverResources_NetworkError(t *testing.T) {
	c := &Connector{
		username: "u", password: "p",
		endpoint: "http://127.0.0.1:1",
		client:   netguard.NewClient(2 * time.Second),
	}
	if _, err := c.DiscoverResources(context.Background()); err == nil {
		t.Error("expected error against an unreachable endpoint")
	}
}

func TestIsAuthError(t *testing.T) {
	cases := map[string]bool{
		"caldav: 401 Unauthorized":  true,
		"got 403 forbidden":         true,
		"unauthorized":              true,
		"connection refused":        false,
		"500 internal server error": false,
	}
	for msg, want := range cases {
		if got := isAuthError(errString(msg)); got != want {
			t.Errorf("isAuthError(%q) = %v, want %v", msg, got, want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestSupportsEvents(t *testing.T) {
	cases := []struct {
		comps []string
		want  bool
	}{
		{nil, true},                         // empty set → assume events
		{[]string{"VEVENT"}, true},          //
		{[]string{"VEVENT", "VTODO"}, true}, //
		{[]string{"VTODO"}, false},          // reminders list
	}
	for _, tc := range cases {
		got := supportsEvents(caldav.Calendar{SupportedComponentSet: tc.comps})
		if got != tc.want {
			t.Errorf("supportsEvents(%v) = %v, want %v", tc.comps, got, tc.want)
		}
	}
}

func TestCalendarNameAndShort(t *testing.T) {
	if calShort("/u/calendars/work/") != "work" {
		t.Errorf("calShort = %q", calShort("/u/calendars/work/"))
	}
	if calShort("solo") != "solo" {
		t.Errorf("calShort(solo) = %q", calShort("solo"))
	}
	if calendarName(caldav.Calendar{Name: "Home"}) != "Home" {
		t.Error("calendarName should prefer Name")
	}
	if calendarName(caldav.Calendar{Path: "/u/x/"}) != "u/x" {
		t.Errorf("calendarName fallback = %q", calendarName(caldav.Calendar{Path: "/u/x/"}))
	}
}

func TestNearest(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	next := now.Add(48 * time.Hour)
	recent := now.Add(-120 * time.Hour)
	if got := nearest(now, next, recent); !got.Equal(next) {
		t.Errorf("nearest should pick closer next, got %v", got)
	}
	if got := nearest(now, time.Time{}, recent); !got.Equal(recent) {
		t.Errorf("nearest with no next should pick recent, got %v", got)
	}
	if got := nearest(now, next, time.Time{}); !got.Equal(next) {
		t.Errorf("nearest with no recent should pick next, got %v", got)
	}
	if got := nearest(now, time.Time{}, time.Time{}); !got.IsZero() {
		t.Errorf("nearest with neither should be zero, got %v", got)
	}
}

func TestThrottleDelay(t *testing.T) {
	if got := throttleDelay(0, 5*time.Second); got != 5*time.Second {
		t.Errorf("Retry-After should win: %v", got)
	}
	if got := throttleDelay(0, 0); got != time.Second {
		t.Errorf("attempt 0 = %v, want 1s", got)
	}
	if got := throttleDelay(2, 0); got != 4*time.Second {
		t.Errorf("attempt 2 = %v, want 4s", got)
	}
	if got := throttleDelay(10, 0); got != 30*time.Second {
		t.Errorf("should cap at 30s, got %v", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := map[string]time.Duration{
		"":        0,
		"3":       3 * time.Second,
		" 5 ":     5 * time.Second,
		"garbage": 0,
		"0":       0,
		"-1":      0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestResolveURL(t *testing.T) {
	got, err := resolveURL("https://caldav.icloud.com", "/123/calendars/x%20y.ics")
	if err != nil {
		t.Fatalf("resolveURL: %v", err)
	}
	// Existing percent-encoding must be preserved (not re-encoded).
	if got != "https://caldav.icloud.com/123/calendars/x%20y.ics" {
		t.Errorf("resolveURL = %q", got)
	}
	// A malformed percent-escape in the path surfaces as an error.
	if _, err := resolveURL("https://x", "/bad%zz"); err == nil {
		t.Error("malformed path should error")
	}
}

func TestSleepCtx(t *testing.T) {
	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Error("sleepCtx should complete and return true")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(ctx, time.Hour) {
		t.Error("cancelled context should return false immediately")
	}
}

func TestSuspiciousShrink(t *testing.T) {
	big := manifest{"/a/": {}}
	for i := 0; i < 40; i++ {
		big["/a/"][string(rune('a'+i%26))+strconv.Itoa(i)] = "etag"
	}
	if !suspiciousShrink(big, make([]string, 5)) {
		t.Error("40→5 should look like a truncated listing")
	}
	if suspiciousShrink(big, make([]string, 30)) {
		t.Error("40→30 is normal churn, not suspicious")
	}
	if suspiciousShrink(manifest{}, nil) {
		t.Error("no baseline → never suspicious")
	}
	if suspiciousShrink(manifest{"/a/": {"h": "e"}}, nil) {
		t.Error("tiny baseline → not suspicious (avoid false positives)")
	}
}

func TestManifestRoundTrip(t *testing.T) {
	c := &Connector{now: func() time.Time { return time.Unix(1000, 0).UTC() }, syncSince: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	m := manifest{
		"/cal/a/": {"/cal/a/e1.ics": "v1"},
	}
	cur := c.newCursor(m)
	if cur.CursorData["sync_since"] != "2025-01-01" {
		t.Errorf("sync_since key = %v", cur.CursorData["sync_since"])
	}
	// Round-trip via a SyncCursor (CursorData survives JSON as map[string]any).
	loaded := loadManifest(&model.SyncCursor{CursorData: cur.CursorData})
	if got := loaded["/cal/a/"]["/cal/a/e1.ics"]; got != "v1" {
		t.Errorf("manifest round-trip lost data: %+v", loaded)
	}
	// A nil/empty cursor yields an empty manifest, not nil deref.
	if loadManifest(nil) == nil {
		t.Error("loadManifest(nil) should return empty manifest")
	}
}
