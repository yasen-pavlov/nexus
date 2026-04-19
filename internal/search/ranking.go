package search

import "github.com/muty/nexus/internal/model"

// rankedChunk is a deduped search result used during result processing.
//
// The score field carries the OpenSearch hit score — for hybrid search this is
// the RRF rank-fusion score, for plain BM25 it's the Lucene score. We do NOT
// filter results by an absolute score floor on RRF: RRF scores encode rank
// position (1/(k+rank)), not relevance, so an absolute threshold would
// arbitrarily drop strong single-phase matches (e.g., a doc that ranks #1 in
// BM25 but doesn't semantically embed near the query). The reranker stage
// downstream is the correct place to enforce relevance, via RankingConfig.RerankerMinScore.
type rankedChunk struct {
	parentID string
	doc      model.Document
	headline string
	score    float64
}

// RankingConfig bundles the knobs that shape per-query result ranking.
// Persisted in the `settings` table, loaded + hot-swapped by
// api.RankingManager, and read by the search path on every query.
//
// Callers must treat the embedded maps as immutable — the manager hands
// out shared references for zero-copy reads.
type RankingConfig struct {
	// SourceHalfLifeDays: recency decay half-life per source_type. After
	// one half-life, the freshness multiplier drops to 50%.
	SourceHalfLifeDays map[string]float64
	// SourceRecencyFloor: minimum freshness multiplier per source_type.
	// 0.9 means the oldest doc still keeps 90% of its reranker score.
	SourceRecencyFloor map[string]float64
	// SourceTrustWeight: multiplicative weight applied to reranker scores
	// before the rerank floor. >1 boosts, <1 penalizes.
	SourceTrustWeight map[string]float64
	// RerankerMinScore: docs with a reranker score below this are dropped.
	// Only applied when a reranker is configured.
	RerankerMinScore float64
	// MetadataBonusEnabled: when true, ApplyMetadataBonus runs at stage 6.
	MetadataBonusEnabled bool
	// SourceTrustEnabled: when true, SourceTrustWeight is applied at stage 3b.
	SourceTrustEnabled bool
}

// DefaultRankingConfig returns the compiled-in defaults. Used when the
// settings table is empty and as the source of truth for values the admin
// hasn't persisted.
func DefaultRankingConfig() RankingConfig {
	return RankingConfig{
		SourceHalfLifeDays: map[string]float64{
			"telegram":   14,  // chat is ephemeral
			"imap":       30,  // emails get stale
			"filesystem": 90,  // files stay relevant longer
			"paperless":  180, // documents are semi-permanent
		},
		SourceRecencyFloor: map[string]float64{
			"telegram":   0.65,
			"imap":       0.75,
			"filesystem": 0.85,
			"paperless":  0.90,
		},
		SourceTrustWeight: map[string]float64{
			"paperless":  1.05,
			"filesystem": 1.00,
			"imap":       0.92,
			"telegram":   0.92,
		},
		// 0.4 is the empirical cleanest cut between signal (~0.6+) and
		// noise (~0.27-0.45) on this corpus, with a small allowance for
		// cross-language matches that tend to score 0.45-0.49.
		RerankerMinScore:     0.4,
		MetadataBonusEnabled: true,
		SourceTrustEnabled:   true,
	}
}

const (
	// DefaultMinShouldMatch is the default minimum_should_match value for
	// multi_match BM25 queries. Controls how many query terms must appear
	// in a single field for a document to match:
	//
	//   1 term  → 1 required (unchanged)
	//   2 terms → 2 required (effectively AND — "ID card" won't match
	//             documents that only contain "ID")
	//   4 terms → 3 required (allows 1 miss for longer queries)
	//
	// Cross-language matches (e.g. "ID card" → "Personalausweis") still
	// come through kNN and are unaffected by this BM25 threshold.
	DefaultMinShouldMatch = "75%"
)
