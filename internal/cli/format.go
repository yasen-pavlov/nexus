package cli

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"

	"github.com/muty/nexus/internal/model"
)

// snippetMaxRunes bounds a one-line preview so a result row stays readable.
const snippetMaxRunes = 160

// fprintf writes formatted human-facing output, deliberately ignoring the write
// error. These are terminal writes; the codebase convention is to not thread
// stdout-write errors through every call site (cf. internal/api/response.go).
func fprintf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

// writeJSON pretty-prints v as indented JSON. HTML escaping is disabled so URLs
// and highlight markup stay readable.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// tagPattern matches the <mark> highlight wrappers (and any other markup)
// OpenSearch puts in the headline.
var tagPattern = regexp.MustCompile(`<[^>]+>`)

// stripTags removes markup so snippets render clean in a terminal.
func stripTags(s string) string {
	return tagPattern.ReplaceAllString(s, "")
}

// formatSearchResults renders a human-readable result list. explain adds the
// per-hit score breakdown when present.
func formatSearchResults(w io.Writer, result *model.SearchResult, explain bool) error {
	if result == nil || len(result.Documents) == 0 {
		query := ""
		if result != nil {
			query = result.Query
		}
		fprintf(w, "No results for %q.\n", query)
		return nil
	}

	for i := range result.Documents {
		hit := &result.Documents[i]
		title := strings.TrimSpace(hit.Title)
		if title == "" {
			title = "(untitled)"
		}
		fprintf(w, "%2d. %s\n", i+1, title)
		fprintf(w, "    %s · %s · %s\n", hit.SourceType, hit.SourceName, hit.CreatedAt.Format("2006-01-02"))
		if s := snippet(hit); s != "" {
			fprintf(w, "    %s\n", s)
		}
		if hit.URL != "" {
			fprintf(w, "    %s\n", hit.URL)
		}
		if explain && hit.ScoreDetails != nil {
			sd := hit.ScoreDetails
			fprintf(w, "    score: final=%.3f retrieval=%.3f rerank=%.3f recency=%.3f meta=%.3f\n",
				sd.Final, sd.Retrieval, sd.Reranker, sd.RecencyFactor, sd.MetadataBonus)
		}
		fprintf(w, "\n")
	}

	noun := "results"
	if result.TotalCount == 1 {
		noun = "result"
	}
	fprintf(w, "%d %s for %q\n", result.TotalCount, noun, result.Query)
	return nil
}

// snippet builds a one-line preview: the highlighted headline if present,
// otherwise the head of the content. Whitespace is collapsed and the text
// truncated on a rune boundary so multibyte content (e.g. Bulgarian) is not cut
// mid-character.
func snippet(hit *model.DocumentHit) string {
	text := stripTags(hit.Headline)
	if strings.TrimSpace(text) == "" {
		text = hit.Content
	}
	// Decode HTML entities (e.g. &#x2F;, &amp;) that some extracted content and
	// highlights carry, so the terminal preview reads naturally. --json output
	// stays raw/faithful.
	text = html.UnescapeString(text)
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > snippetMaxRunes {
		return string(runes[:snippetMaxRunes]) + "…"
	}
	return text
}
