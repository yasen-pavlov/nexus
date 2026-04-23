package search

import (
	"testing"

	"github.com/muty/nexus/internal/model"
)

// TestApplyMetadataBonus_NoDocsIsNoop covers the empty-result
// short-circuit — no docs → function returns without touching
// anything.
func TestApplyMetadataBonus_NoDocsIsNoop(t *testing.T) {
	result := &model.SearchResult{}
	ApplyMetadataBonus(result, "anything")
	if result.Documents != nil {
		t.Errorf("expected nil docs slice to stay nil, got %v", result.Documents)
	}
}

// TestApplyMetadataBonus_EmptyQueryIsNoop covers the empty-query
// early-return.
func TestApplyMetadataBonus_EmptyQueryIsNoop(t *testing.T) {
	doc := model.DocumentHit{Document: model.Document{SourceType: "imap", Metadata: map[string]any{"from": "Alice"}}, Rank: 1.0}
	result := &model.SearchResult{Documents: []model.DocumentHit{doc}}
	ApplyMetadataBonus(result, "")
	if result.Documents[0].Rank != 1.0 {
		t.Errorf("empty query should not alter rank, got %v", result.Documents[0].Rank)
	}
}

// TestApplyMetadataBonus_NoMatchingTermsKeepsRank covers the
// zero-match branch — metadata is present but none of the query
// terms hit, so bonus is 0 and rank is unchanged.
func TestApplyMetadataBonus_NoMatchingTermsKeepsRank(t *testing.T) {
	doc := model.DocumentHit{
		Document: model.Document{
			SourceType: "imap",
			Metadata:   map[string]any{"from": "alice@example.com"},
		},
		Rank: 2.0,
	}
	result := &model.SearchResult{Documents: []model.DocumentHit{doc}}
	ApplyMetadataBonus(result, "xyznonexistent")
	if result.Documents[0].Rank != 2.0 {
		t.Errorf("no-match should leave rank alone, got %v", result.Documents[0].Rank)
	}
}

// TestApplyMetadataBonus_MatchAddsBonusAndReSorts covers the
// happy path — a query that matches a metadata field bumps rank
// and the result slice ends up sorted rank-descending.
func TestApplyMetadataBonus_MatchAddsBonusAndReSorts(t *testing.T) {
	hit1 := model.DocumentHit{
		Document: model.Document{SourceType: "imap", Metadata: map[string]any{"from": "bob@example.com"}},
		Rank:     1.0,
	}
	hit2 := model.DocumentHit{
		Document: model.Document{SourceType: "imap", Metadata: map[string]any{"from": "alice@example.com"}},
		Rank:     2.0,
	}
	// alice query: hit2 matches, hit1 doesn't. Post-bonus, hit2
	// should still be first (both match/no-match or rank ordering
	// keeps hit2 on top).
	result := &model.SearchResult{Documents: []model.DocumentHit{hit1, hit2}}
	ApplyMetadataBonus(result, "alice")
	if result.Documents[0].SourceID != hit2.SourceID {
		t.Errorf("expected matching hit to sort first; got %+v", result.Documents[0])
	}
	if result.Documents[0].Rank <= 2.0 {
		t.Errorf("expected bonus to bump rank past 2.0, got %v", result.Documents[0].Rank)
	}
}

// TestCalcMetadataBonus_NilMetadata returns 0 immediately.
func TestCalcMetadataBonus_NilMetadata(t *testing.T) {
	doc := &model.DocumentHit{Document: model.Document{SourceType: "imap"}}
	if got := calcMetadataBonus(doc, []string{"term"}); got != 0 {
		t.Errorf("nil metadata bonus = %v, want 0", got)
	}
}

// TestCalcMetadataBonus_UnknownSourceTypeReturnsZero covers the
// branch where metadataFields has no entry for the doc's source
// type — we don't know which fields to check, so we skip.
func TestCalcMetadataBonus_UnknownSourceTypeReturnsZero(t *testing.T) {
	doc := &model.DocumentHit{Document: model.Document{
		SourceType: "unknown-source",
		Metadata:   map[string]any{"foo": "bar"},
	}}
	if got := calcMetadataBonus(doc, []string{"bar"}); got != 0 {
		t.Errorf("unknown source bonus = %v, want 0", got)
	}
}
