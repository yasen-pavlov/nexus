package api

import (
	"testing"
	"time"
)

// TestSortPerSourceStats_RecencyThenTypeName exercises every branch
// of the comparator: matching times fall through to source-type tie
// break, matching types fall through to source-name tie break, and
// nil LatestIndexedAt sorts last.
func TestSortPerSourceStats_RecencyThenTypeName(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	t2 := t0.Add(2 * time.Hour)

	rows := []AdminPerSourceStats{
		{SourceType: "filesystem", SourceName: "a", LatestIndexedAt: &t1},
		{SourceType: "imap", SourceName: "z", LatestIndexedAt: &t0},
		{SourceType: "filesystem", SourceName: "never-synced", LatestIndexedAt: nil},
		{SourceType: "filesystem", SourceName: "tied", LatestIndexedAt: &t2},
		{SourceType: "filesystem", SourceName: "tied-later-lex", LatestIndexedAt: &t2},
	}

	sortPerSourceStats(rows)

	// Most recent wins: t2 > t1 > t0 > nil. Within the t2 tie,
	// the source-name tiebreaker puts "tied" before "tied-later-lex".
	want := []string{"tied", "tied-later-lex", "a", "z", "never-synced"}
	for i, r := range rows {
		if r.SourceName != want[i] {
			t.Errorf("row[%d] = %q, want %q (full order %v)", i, r.SourceName, want, rowNames(rows))
		}
	}
}

// TestSortPerSourceStats_EqualTimesDifferentTypes covers the
// source-type tiebreaker branch (when times are equal AND names
// happen to be in reverse alpha order — types decide first).
func TestSortPerSourceStats_EqualTimesDifferentTypes(t *testing.T) {
	t0 := time.Now()
	rows := []AdminPerSourceStats{
		{SourceType: "paperless", SourceName: "shared", LatestIndexedAt: &t0},
		{SourceType: "filesystem", SourceName: "shared", LatestIndexedAt: &t0},
	}
	sortPerSourceStats(rows)
	if rows[0].SourceType != "filesystem" {
		t.Errorf("expected filesystem first (lex before paperless), got %v", rowNames(rows))
	}
}

func rowNames(rows []AdminPerSourceStats) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.SourceType + "/" + r.SourceName
	}
	return out
}
