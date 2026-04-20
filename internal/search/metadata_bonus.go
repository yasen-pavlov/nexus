package search

import (
	"sort"
	"strings"

	"github.com/muty/nexus/internal/model"
)

// metadataBonus is the maximum additive bonus for a full metadata match.
const metadataBonus = 0.15

// metadataFields defines which metadata fields to check per source type.
// These are fields the reranker can't see (it only gets title + content text).
var metadataFields = map[string][]string{
	"imap":       {"from", "attachment_filenames"},
	"paperless":  {"original_file_name", "correspondent", "tags"},
	"filesystem": {"path"},
	"telegram":   {"chat_name"},
}

// ApplyMetadataBonus adds a score bonus when query terms match structured
// metadata fields that the reranker doesn't see. Then re-sorts by score.
func ApplyMetadataBonus(result *model.SearchResult, query string) {
	if len(result.Documents) == 0 || query == "" {
		return
	}

	terms := tokenize(query)
	if len(terms) == 0 {
		return
	}

	for i := range result.Documents {
		doc := &result.Documents[i]
		bonus := calcMetadataBonus(doc, terms)
		doc.Rank += bonus

		if doc.ScoreDetails != nil {
			doc.ScoreDetails.MetadataBonus = bonus
			doc.ScoreDetails.Final = doc.Rank
		}
	}

	sort.Slice(result.Documents, func(i, j int) bool {
		return result.Documents[i].Rank > result.Documents[j].Rank
	})
}

// calcMetadataBonus checks metadata fields for query term matches.
// Returns a bonus proportional to the fraction of terms that matched.
func calcMetadataBonus(doc *model.DocumentHit, terms []string) float64 {
	if doc.Metadata == nil {
		return 0
	}

	fields := metadataFields[doc.SourceType]
	if len(fields) == 0 {
		return 0
	}

	combined := collectMetadataText(doc.Metadata, fields)
	if combined == "" {
		return 0
	}

	// Count how many query terms match
	matched := 0
	for _, term := range terms {
		if strings.Contains(combined, term) {
			matched++
		}
	}

	if matched == 0 {
		return 0
	}

	// Bonus proportional to fraction of terms matched
	return metadataBonus * float64(matched) / float64(len(terms))
}

// collectMetadataText flattens the named metadata fields into a single
// space-separated lowercase string suitable for substring matching. Supports
// string and []any shapes (strings inside the slice); other shapes are
// silently ignored.
func collectMetadataText(metadata map[string]any, fields []string) string {
	var sb strings.Builder
	for _, field := range fields {
		appendMetadataValue(&sb, metadata[field])
	}
	return sb.String()
}

// appendMetadataValue writes the lowercase, space-padded form of val to sb for
// recognised scalar/slice shapes, skipping unknown types.
func appendMetadataValue(sb *strings.Builder, val any) {
	switch v := val.(type) {
	case string:
		sb.WriteString(strings.ToLower(v))
		sb.WriteByte(' ')
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				sb.WriteString(strings.ToLower(s))
				sb.WriteByte(' ')
			}
		}
	}
}

// tokenize splits a query into lowercase terms, stripping punctuation.
func tokenize(query string) []string {
	words := strings.Fields(strings.ToLower(query))
	var terms []string
	for _, w := range words {
		w = strings.TrimFunc(w, func(r rune) bool {
			// keep letters, digits, accented chars
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r < 0x00C0
		})
		if len(w) > 1 { // skip single-char terms
			terms = append(terms, w)
		}
	}
	return terms
}
