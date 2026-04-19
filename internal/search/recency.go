package search

import (
	"math"
	"sort"
	"time"

	"github.com/muty/nexus/internal/model"
)

// Recency decay fallback constants. Used when a source_type isn't present
// in the RankingConfig maps — prevents surprises for future source types
// that haven't been added to the curated knobs yet.
const (
	defaultHalfLife     = 60
	defaultRecencyFloor = 0.75
)

// ApplyRecencyDecay adjusts document scores based on age, then re-sorts.
// The formula is: final_score = relevance × (floor + (1-floor) × freshness)
// where freshness = 0.5^(age_days / half_life).
//
// Recency is multiplicative — it modulates relevance rather than adding to
// it. An irrelevant recent document scores 0, not 0.3.
func ApplyRecencyDecay(result *model.SearchResult, cfg RankingConfig) {
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

		halfLife := cfg.SourceHalfLifeDays[doc.SourceType]
		if halfLife == 0 {
			halfLife = defaultHalfLife
		}

		floor := cfg.SourceRecencyFloor[doc.SourceType]
		if floor == 0 {
			floor = defaultRecencyFloor
		}

		freshness := math.Pow(0.5, ageDays/halfLife)
		factor := floor + (1-floor)*freshness
		doc.Rank *= factor

		if doc.ScoreDetails != nil {
			doc.ScoreDetails.RecencyFactor = factor
		}
	}

	sort.Slice(result.Documents, func(i, j int) bool {
		return result.Documents[i].Rank > result.Documents[j].Rank
	})
}
