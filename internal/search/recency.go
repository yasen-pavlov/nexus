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
const defaultHalfLife = 60

// sourceHalfLife maps source types to their recency half-life in days.
// After one half-life, the freshness component drops to 50%.
var sourceHalfLife = map[string]float64{
	"telegram":   14,  // chat is ephemeral
	"imap":       30,  // emails get stale
	"filesystem": 90,  // files stay relevant longer
	"paperless":  180, // documents are semi-permanent
}

const defaultRecencyFloor = 0.75

// sourceRecencyFloor maps source types to their minimum freshness factor.
// A floor of 0.9 means a very old document keeps at least 90% of its
// reranker score. Different sources have different permanence: a
// 2-year-old birth certificate in Paperless is just as relevant as a new
// one, while a 2-year-old Telegram message probably isn't.
var sourceRecencyFloor = map[string]float64{
	"telegram":   0.65, // chat is ephemeral — max 35% decay
	"imap":       0.75, // emails get stale — max 25% decay
	"filesystem": 0.85, // files stay relevant longer — max 15% decay
	"paperless":  0.90, // archived documents are semi-permanent — max 10% decay
}

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

		floor := sourceRecencyFloor[doc.SourceType]
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
