package search

import "testing"

// TestDefaultMinShouldMatch_AndForTwoTerms pins the conditional spec. Plain
// "75%" rounds DOWN (75% of 2 = 1 required), letting two-term queries match
// single-term documents. The "2<75%" spec forces all terms for queries up to
// two terms while keeping the 75% floor above that.
func TestDefaultMinShouldMatch_AndForTwoTerms(t *testing.T) {
	if DefaultMinShouldMatch != "2<75%" {
		t.Errorf("DefaultMinShouldMatch = %q, want %q", DefaultMinShouldMatch, "2<75%")
	}
}
