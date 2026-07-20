package pipeline

import (
	"context"
	"fmt"
	"testing"

	"github.com/muty/nexus/internal/embedding"
	"github.com/muty/nexus/internal/model"
	"go.uber.org/zap"
)

// batchRecordingEmbedder records the largest batch it was asked to embed and
// can be told to fail on a specific 1-based call, so tests can assert both the
// sub-batch cap and partial-result retention.
type batchRecordingEmbedder struct {
	dim       int
	maxBatch  int
	calls     int
	errAtCall int // 1-based; 0 = never
}

func (m *batchRecordingEmbedder) Embed(_ context.Context, texts []string, _ string) ([][]float32, error) {
	m.calls++
	if len(texts) > m.maxBatch {
		m.maxBatch = len(texts)
	}
	if m.errAtCall != 0 && m.calls == m.errAtCall {
		return nil, fmt.Errorf("simulated provider error (over cap)")
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, m.dim)
	}
	return out, nil
}

func (m *batchRecordingEmbedder) Dimension() int { return m.dim }

type staticEmbedderProvider struct{ e embedding.Embedder }

func (s staticEmbedderProvider) Get() embedding.Embedder { return s.e }

// gatedContent has >= minEmbeddingAlphabeticTokens (10) alphabetic tokens so it
// passes the noise gate.
const gatedContent = "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda"

func TestPopulateChunkEmbeddings_SplitsIntoSubBatches(t *testing.T) {
	emb := &batchRecordingEmbedder{dim: 3}
	p := &Pipeline{embeddings: staticEmbedderProvider{emb}, log: zap.NewNop()}

	doc := &model.Document{SourceID: "big"}
	n := embedBatchSize + 30 // spans two windows
	chunks := make([]model.Chunk, n+1)
	for i := range n {
		chunks[i] = model.Chunk{Content: gatedContent}
	}
	// One low-information chunk that must stay vector-free (noise gate).
	chunks[n] = model.Chunk{Content: "ok"}

	p.populateChunkEmbeddings(context.Background(), doc, chunks)

	if emb.maxBatch > embedBatchSize {
		t.Errorf("a single Embed call had %d texts, exceeding cap %d", emb.maxBatch, embedBatchSize)
	}
	if emb.calls < 2 {
		t.Errorf("expected the batch to be split across >=2 calls, got %d", emb.calls)
	}
	for i := range n {
		if chunks[i].Embedding == nil {
			t.Fatalf("gated chunk %d has no embedding (all should be populated across sub-batches)", i)
		}
	}
	if chunks[n].Embedding != nil {
		t.Errorf("low-info chunk should remain vector-free (noise gate)")
	}
}

func TestPopulateChunkEmbeddings_KeepsPartialOnBatchError(t *testing.T) {
	emb := &batchRecordingEmbedder{dim: 3, errAtCall: 1} // fail only the FIRST window
	p := &Pipeline{embeddings: staticEmbedderProvider{emb}, log: zap.NewNop()}

	doc := &model.Document{SourceID: "big"}
	n := embedBatchSize + 30 // two windows
	chunks := make([]model.Chunk, n)
	for i := range chunks {
		chunks[i] = model.Chunk{Content: gatedContent}
	}

	p.populateChunkEmbeddings(context.Background(), doc, chunks)

	// First window (0..embedBatchSize-1) failed → no embeddings there.
	if chunks[0].Embedding != nil {
		t.Errorf("first-window chunk should have no embedding after that batch errored")
	}
	// Second window still embedded — the old all-or-nothing return discarded it.
	if chunks[n-1].Embedding == nil {
		t.Errorf("second-window chunk must still be embedded (partial results retained)")
	}
}
