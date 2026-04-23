package api

import (
	"net/http/httptest"
	"testing"
)

// TestBuildSearchRequest_Defaults covers the missing-params path:
// no limit, no offset, no filters → limit defaults to 20, offset to 0.
func TestBuildSearchRequest_Defaults(t *testing.T) {
	req := httptest.NewRequest("GET", "/search?q=x", nil)
	got := buildSearchRequest(withAdminContext(req), "x")
	if got.Limit != 20 {
		t.Errorf("Limit = %d, want 20", got.Limit)
	}
	if got.Offset != 0 {
		t.Errorf("Offset = %d, want 0", got.Offset)
	}
	if got.Query != "x" {
		t.Errorf("Query = %q, want x", got.Query)
	}
}

// TestBuildSearchRequest_ClampsLimitToMax exercises the defensive
// upper bound so an attacker can't pull millions of rows.
func TestBuildSearchRequest_ClampsLimitToMax(t *testing.T) {
	req := httptest.NewRequest("GET", "/search?q=x&limit=99999", nil)
	got := buildSearchRequest(withAdminContext(req), "x")
	if got.Limit != maxSearchLimit {
		t.Errorf("Limit = %d, want %d (clamped)", got.Limit, maxSearchLimit)
	}
}

// TestBuildSearchRequest_ClampsNegativeOffset covers the offset<0
// branch; a negative offset always snaps to 0.
func TestBuildSearchRequest_ClampsNegativeOffset(t *testing.T) {
	req := httptest.NewRequest("GET", "/search?q=x&offset=-5", nil)
	got := buildSearchRequest(withAdminContext(req), "x")
	if got.Offset != 0 {
		t.Errorf("Offset = %d, want 0", got.Offset)
	}
}

// TestBuildSearchRequest_ClampsOffsetToMax covers the offset>max
// branch — beyond max we silently cap rather than 400-erroring.
func TestBuildSearchRequest_ClampsOffsetToMax(t *testing.T) {
	req := httptest.NewRequest("GET", "/search?q=x&offset=99999999", nil)
	got := buildSearchRequest(withAdminContext(req), "x")
	if got.Offset != maxSearchOffset {
		t.Errorf("Offset = %d, want %d (clamped)", got.Offset, maxSearchOffset)
	}
}

// TestParseCSV_* cover the CSV parser used by search filter
// parameters. Edge cases verified: empty input yields nil (not
// empty slice), surrounding whitespace is trimmed, empty segments
// are dropped.
func TestParseCSV_EmptyYieldsNil(t *testing.T) {
	if got := parseCSV(""); got != nil {
		t.Errorf("expected nil for empty, got %v", got)
	}
}

func TestParseCSV_TrimsAndDropsEmpties(t *testing.T) {
	got := parseCSV("  imap , , telegram  ,   ")
	if len(got) != 2 || got[0] != "imap" || got[1] != "telegram" {
		t.Errorf("parseCSV = %v, want [imap telegram]", got)
	}
}

func TestParseCSV_Single(t *testing.T) {
	got := parseCSV("solo")
	if len(got) != 1 || got[0] != "solo" {
		t.Errorf("parseCSV = %v, want [solo]", got)
	}
}

// TestBuildSearchRequest_ParsesFilters covers the sources/source_names
// CSV parsing + date-string passthrough.
func TestBuildSearchRequest_ParsesFilters(t *testing.T) {
	req := httptest.NewRequest("GET",
		"/search?q=x&sources=imap,telegram&source_names=inbox,chats&date_from=2026-01-01&date_to=2026-06-01",
		nil)
	got := buildSearchRequest(withAdminContext(req), "x")
	if len(got.Sources) != 2 || got.Sources[0] != "imap" || got.Sources[1] != "telegram" {
		t.Errorf("Sources = %v", got.Sources)
	}
	if len(got.SourceNames) != 2 {
		t.Errorf("SourceNames = %v", got.SourceNames)
	}
	if got.DateFrom != "2026-01-01" || got.DateTo != "2026-06-01" {
		t.Errorf("dates = %q/%q", got.DateFrom, got.DateTo)
	}
}
