package search

import "github.com/muty/nexus/internal/model"

// rankedChunk is a deduped search result used during result processing.
type rankedChunk struct {
	parentID string
	doc      model.Document
	headline string
	score    float64
}

const (
	// minHybridScore is the minimum RRF score for a result to be included in hybrid search.
	// Results below this threshold are noise from low-confidence kNN matches.
	// Based on observed scores: relevant results score 0.025+, noise scores 0.010-0.015.
	// TODO: make configurable via settings UI
	minHybridScore = 0.02
)
