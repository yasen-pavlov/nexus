package search

import (
	"testing"

	"github.com/muty/nexus/internal/model"
)

// TestMergeRankedChunk_FirstChunkCreates covers the
// not-yet-in-map branch — a new ParentID produces a fresh entry.
func TestMergeRankedChunk_FirstChunkCreates(t *testing.T) {
	chunks := map[string]*rankedChunk{}
	mergeRankedChunk(chunks, &model.Chunk{
		ParentID: "parent-1", SourceType: "fs", SourceName: "a", SourceID: "x",
		Title: "First", Content: "body",
	}, "highlight", 1.0)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(chunks))
	}
	rc := chunks["parent-1"]
	if rc.score != 1.0 {
		t.Errorf("score = %v, want 1.0", rc.score)
	}
	if rc.headline != "highlight" {
		t.Errorf("headline = %q, want 'highlight'", rc.headline)
	}
	if rc.doc.Title != "First" {
		t.Errorf("doc.Title = %q, want 'First'", rc.doc.Title)
	}
}

// TestMergeRankedChunk_HigherScoreReplaces covers the
// score-comparison path — a better-scoring chunk for the same
// parent replaces the stored headline and score.
func TestMergeRankedChunk_HigherScoreReplaces(t *testing.T) {
	chunks := map[string]*rankedChunk{}
	mergeRankedChunk(chunks, &model.Chunk{ParentID: "p", Title: "T"}, "lo", 0.5)
	mergeRankedChunk(chunks, &model.Chunk{ParentID: "p", Title: "T"}, "hi", 1.5)
	if chunks["p"].score != 1.5 {
		t.Errorf("score not updated: %v", chunks["p"].score)
	}
	if chunks["p"].headline != "hi" {
		t.Errorf("headline not updated: %q", chunks["p"].headline)
	}
}

// TestMergeRankedChunk_LowerScoreIgnored guards against the
// inverse — the first chunk's stronger match must not be clobbered
// by a later, weaker chunk.
func TestMergeRankedChunk_LowerScoreIgnored(t *testing.T) {
	chunks := map[string]*rankedChunk{}
	mergeRankedChunk(chunks, &model.Chunk{ParentID: "p", Title: "T"}, "best", 2.0)
	mergeRankedChunk(chunks, &model.Chunk{ParentID: "p", Title: "T"}, "worse", 0.1)
	if chunks["p"].score != 2.0 {
		t.Errorf("score regressed to %v", chunks["p"].score)
	}
	if chunks["p"].headline != "best" {
		t.Errorf("headline regressed to %q", chunks["p"].headline)
	}
}
