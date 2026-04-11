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
// downstream is the correct place to enforce relevance, via RerankMinScore.
type rankedChunk struct {
	parentID string
	doc      model.Document
	headline string
	score    float64
}

// Ranking thresholds. These are starting values tuned empirically — they live
// here as named constants so the future "Search quality settings in UI" backlog
// item can promote them to DB-backed configurable values without changing the
// code paths that consume them.
const (
	// RerankMinScore is the minimum reranker score for a doc to survive the
	// rerank stage. Voyage rerank-2 scores are 0-1 with 0.5+ meaning "actually
	// related". 0.4 is a starting point: empirically the gap between
	// signal (~0.6+) and noise (~0.27-0.45) for our corpus puts the cleanest
	// cut around 0.5, but 0.4 is more permissive to allow cross-language
	// matches (e.g. English "invoice" → German "Rechnung" scores ~0.45-0.49).
	// Tune higher if noise leaks through.
	//
	// Only applied when a reranker is configured. Without one we have no
	// principled noise filter — searches that hit kNN-only matches will have
	// some hub noise in the long tail. Add a reranker if that's a problem.
	RerankMinScore = 0.4
)
