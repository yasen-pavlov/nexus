package search

// Ranking configuration for hybrid search.
// These constants control how BM25 and k-NN results are merged.
// Adjust these values to tune search quality.
const (
	// rrfK is the RRF constant. Higher values reduce the impact of rank differences.
	rrfK = 60

	// knnMinScore is the minimum cosine similarity for k-NN results.
	// Results below this threshold are discarded before ranking.
	knnMinScore = 0.3

	// knnOnlyWeight is the RRF weight multiplier for results found only by k-NN
	// (not in BM25 results). This heavily penalizes semantically vague matches
	// that don't contain the query keywords.
	// 0.0 = discard k-NN-only results entirely
	// 0.1 = allow but heavily penalize (recommended)
	// 1.0 = equal weight (original behavior)
	knnOnlyWeight = 0.1
)
