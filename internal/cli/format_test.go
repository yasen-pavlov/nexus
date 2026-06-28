package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

func TestFormatSearchResultsEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := formatSearchResults(&buf, &model.SearchResult{Query: "ghosts"}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `No results for "ghosts"`) {
		t.Fatalf("unexpected: %s", buf.String())
	}

	// A nil result must not panic.
	buf.Reset()
	if err := formatSearchResults(&buf, nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestFormatSearchResultsWithExplain(t *testing.T) {
	res := &model.SearchResult{
		Query:      "report",
		TotalCount: 1,
		Documents: []model.DocumentHit{{
			Document: model.Document{
				Title:      "Q1 Report",
				SourceType: "paperless",
				SourceName: "docs",
				URL:        "https://paperless.local/documents/7",
				CreatedAt:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
			},
			Headline:     "the <mark>report</mark> figures",
			ScoreDetails: &model.ScoreDetails{Final: 0.91, Retrieval: 0.4, Reranker: 0.88, RecencyFactor: 1.0, MetadataBonus: 0.1},
		}},
	}
	var buf bytes.Buffer
	if err := formatSearchResults(&buf, res, true); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Q1 Report", "paperless · docs · 2026-01-15", "the report figures", "https://paperless.local/documents/7", "final=0.910", "1 result for"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<mark>") {
		t.Fatalf("tags not stripped:\n%s", out)
	}
}

func TestSnippetTruncatesOnRuneBoundary(t *testing.T) {
	// Cyrillic content: byte-truncation would split a rune; rune-truncation must not.
	long := strings.Repeat("я", snippetMaxRunes+50)
	hit := &model.DocumentHit{Document: model.Document{Content: long}}
	got := snippet(hit)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
	// The trimmed prefix (sans ellipsis) must be valid, exactly snippetMaxRunes runes.
	trimmed := strings.TrimSuffix(got, "…")
	if n := len([]rune(trimmed)); n != snippetMaxRunes {
		t.Fatalf("rune count = %d, want %d", n, snippetMaxRunes)
	}
}

func TestSnippetFallsBackToContent(t *testing.T) {
	hit := &model.DocumentHit{Document: model.Document{Content: "  body   text  here "}}
	if got := snippet(hit); got != "body text here" {
		t.Fatalf("whitespace not collapsed: %q", got)
	}

	// HTML entities in extracted content are decoded for the human view.
	ent := &model.DocumentHit{Document: model.Document{Content: "7&#x2F;30 A&amp;B"}}
	if got := snippet(ent); got != "7/30 A&B" {
		t.Fatalf("entities not decoded: %q", got)
	}
	// Untitled hit renders a placeholder.
	var buf bytes.Buffer
	_ = formatSearchResults(&buf, &model.SearchResult{
		TotalCount: 2,
		Documents:  []model.DocumentHit{{}, {}},
	}, false)
	if !strings.Contains(buf.String(), "(untitled)") || !strings.Contains(buf.String(), "2 results") {
		t.Fatalf("unexpected: %s", buf.String())
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, map[string]string{"url": "https://x/y?a=b"}); err != nil {
		t.Fatal(err)
	}
	// HTML escaping is disabled, so the URL stays readable.
	if !strings.Contains(buf.String(), "https://x/y?a=b") {
		t.Fatalf("unexpected json: %s", buf.String())
	}
}
