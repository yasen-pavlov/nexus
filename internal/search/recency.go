package search

import (
	"math"
	"sort"
	"time"

	"github.com/muty/nexus/internal/model"
)

// Recency decay configuration.
// Recency is multiplicative — it modulates relevance rather than adding to it.
// An irrelevant recent document scores 0, not 0.3.
const (
	// recencyFloor is the minimum freshness factor for very old documents.
	// Old documents keep at least this fraction of their relevance score.
	recencyFloor = 0.7
)

// sourceHalfLife maps source types to their recency half-life in days.
// After one half-life, the freshness component drops to 50%.
var sourceHalfLife = map[string]float64{
	"telegram":   14,  // chat is ephemeral
	"imap":       30,  // emails get stale
	"filesystem": 90,  // files stay relevant longer
	"paperless":  180, // documents are semi-permanent
}

const defaultHalfLife = 60

// ApplyRecencyDecay adjusts document scores based on age, then re-sorts.
// The formula is: final_score = relevance × (floor + (1-floor) × freshness)
// where freshness = 0.5^(age_days / half_life).
func ApplyRecencyDecay(result *model.SearchResult) {
	if len(result.Documents) == 0 {
		return
	}

	now := time.Now()

	for i := range result.Documents {
		doc := &result.Documents[i]
		if doc.CreatedAt.IsZero() {
			continue
		}

		ageDays := now.Sub(doc.CreatedAt).Hours() / 24
		if ageDays < 0 {
			ageDays = 0
		}

		halfLife := sourceHalfLife[doc.SourceType]
		if halfLife == 0 {
			halfLife = defaultHalfLife
		}

		freshness := math.Pow(0.5, ageDays/halfLife)
		factor := recencyFloor + (1-recencyFloor)*freshness
		doc.Rank *= factor
	}

	sort.Slice(result.Documents, func(i, j int) bool {
		return result.Documents[i].Rank > result.Documents[j].Rank
	})
}
