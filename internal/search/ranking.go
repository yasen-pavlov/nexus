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

	// DefaultSourceTrustEnabled controls whether per-source trust weights
	// are applied to reranker scores. When the Settings UI lands this
	// becomes a toggle + per-source weight sliders.
	DefaultSourceTrustEnabled = true
)

// SourceTrustWeight maps source types to a multiplicative weight applied
// to the reranker score before the rerank floor. Values > 1 boost,
// < 1 penalize. The intent is to express that some sources are
// intrinsically more authoritative for document-type queries:
// Paperless stores deliberately-archived documents, while IMAP is an
// unfiltered inbox and Telegram is ephemeral chat.
//
// Applied between the reranker stage and the rerank floor so that
// borderline inbox noise (reranker ~0.40) drops below the floor after
// the penalty, while genuinely relevant emails (reranker 0.45+) survive.
var SourceTrustWeight = map[string]float64{
	"paperless":  1.05, // archived documents — slight boost
	"filesystem": 1.00, // neutral
	"imap":       0.92, // unfiltered inbox — penalty
	"telegram":   0.92, // chat noise — penalty
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
