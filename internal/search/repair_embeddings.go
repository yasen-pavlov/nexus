package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/muty/nexus/internal/embedding"
	"github.com/muty/nexus/internal/model"
)

// repairBatchSize is how many chunks one embedder call carries.
const repairBatchSize = 64

type repairHit struct {
	ID          string          `json:"_id"`
	SeqNo       int64           `json:"_seq_no"`
	PrimaryTerm int64           `json:"_primary_term"`
	Source      json.RawMessage `json:"_source"`
}

// repairSearchResult is the part of a _search response the repair reads. It
// is also the snapshot's on-disk shape, so the JSON tags are a file format.
type repairSearchResult struct {
	Hits repairHits `json:"hits"`
}

type repairHits struct {
	Total repairTotal `json:"total"`
	Hits  []repairHit `json:"hits"`
}

type repairTotal struct {
	Value int `json:"value"`
}

// RepairMissingEmbeddings repairs only missing vectors of one exact parent ID.
// It snapshots every matching chunk before writing and uses optimistic locking;
// no cursor, index, text, ID, metadata or existing vector is modified. Run while
// the server is stopped to exclude concurrent connector rewrites. The snapshot
// is private and exclusive-created so an earlier backup cannot be overwritten.
func (c *Client) RepairMissingEmbeddings(ctx context.Context, parentID, snapshot string, embedder embedding.Embedder, eligible func(string) bool) error {
	if parentID == "" || snapshot == "" || embedder == nil {
		return fmt.Errorf("parent ID, snapshot path and active embedder are required")
	}
	query := map[string]any{"size": 10000, "track_total_hits": true, "seq_no_primary_term": true, "query": map[string]any{"term": map[string]any{"parent_id": parentID}}, "sort": []any{map[string]any{"chunk_index": "asc"}}}
	var before repairSearchResult
	if err := c.repairJSON(ctx, "POST", "/_search", query, &before); err != nil {
		return err
	}
	hits := before.Hits.Hits
	if len(hits) == 0 || before.Hits.Total.Value != len(hits) {
		return fmt.Errorf("refusing incomplete snapshot: total=%d returned=%d", before.Hits.Total.Value, len(hits))
	}
	if err := writeRepairSnapshot(snapshot, before); err != nil {
		return err
	}
	missing, texts, err := missingEmbeddings(hits, parentID, eligible)
	if err != nil {
		return err
	}
	if err := c.writeMissingEmbeddings(ctx, hits, missing, texts, embedder); err != nil {
		return err
	}
	if err := c.repairJSON(ctx, "POST", "/_refresh", nil, nil); err != nil {
		return err
	}
	var after repairSearchResult
	if err := c.repairJSON(ctx, "POST", "/_search", query, &after); err != nil {
		return err
	}
	if err := verifyRepair(hits, after, missing, embedder.Dimension()); err != nil {
		return err
	}
	fmt.Printf("verified: chunks=%d repaired=%d dimension=%d IDs/text/metadata/existing-vectors unchanged\n", len(hits), len(missing), embedder.Dimension())
	return nil
}

// writeRepairSnapshot stores the pre-repair state in a new private file. It
// fails if the file exists, so an earlier backup is never overwritten.
func writeRepairSnapshot(path string, result repairSearchResult) error {
	backup, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(backup)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	fmt.Printf("snapshot: chunks=%d sha256=%x\n", len(result.Hits.Hits), sha256.Sum256(backup))
	return nil
}

// missingEmbeddings returns the positions in hits, and the texts, of the
// chunks to embed: visible, eligible and without a vector.
func missingEmbeddings(hits []repairHit, parentID string, eligible func(string) bool) (missing []int, texts []string, err error) {
	for i, h := range hits {
		var chunk model.Chunk
		if err := json.Unmarshal(h.Source, &chunk); err != nil {
			return nil, nil, err
		}
		if chunk.ParentID != parentID {
			return nil, nil, fmt.Errorf("unexpected parent in snapshot")
		}
		if len(chunk.Embedding) == 0 && !chunk.Hidden && eligible(chunk.Content) {
			missing = append(missing, i)
			texts = append(texts, chunk.Content)
		}
	}
	return missing, texts, nil
}

// writeMissingEmbeddings embeds texts in batches and writes each vector to
// its chunk, conditional on the chunk being unchanged since the snapshot.
func (c *Client) writeMissingEmbeddings(ctx context.Context, hits []repairHit, missing []int, texts []string, embedder embedding.Embedder) error {
	for start := 0; start < len(texts); start += repairBatchSize {
		end := min(start+repairBatchSize, len(texts))
		vectors, err := embedder.Embed(ctx, texts[start:end], embedding.InputTypeDocument)
		if err != nil {
			return err
		}
		if len(vectors) != end-start {
			return fmt.Errorf("embedding count mismatch")
		}
		for i, vector := range vectors {
			if err := c.writeEmbedding(ctx, hits[missing[start+i]], vector, embedder.Dimension()); err != nil {
				return err
			}
		}
		fmt.Printf("repaired vectors: %d/%d dimension=%d\n", end, len(texts), embedder.Dimension())
	}
	return nil
}

func (c *Client) writeEmbedding(ctx context.Context, h repairHit, vector []float32, dimension int) error {
	if len(vector) != dimension {
		return fmt.Errorf("embedding dimension mismatch: %d", len(vector))
	}
	path := fmt.Sprintf("/_update/%s?if_seq_no=%d&if_primary_term=%d", url.PathEscape(h.ID), h.SeqNo, h.PrimaryTerm)
	return c.repairJSON(ctx, "POST", path, map[string]any{"doc": map[string]any{"embedding": vector}}, nil)
}

// verifyRepair checks the post-repair state against the snapshot: same chunks
// in the same order, a vector of the right dimension on every repaired chunk,
// and nothing else changed.
func verifyRepair(before []repairHit, after repairSearchResult, missing []int, dimension int) error {
	if after.Hits.Total.Value != len(before) || len(after.Hits.Hits) != len(before) {
		return fmt.Errorf("chunk count changed during repair")
	}
	repaired := make(map[int]bool, len(missing))
	for _, i := range missing {
		repaired[i] = true
	}
	for i, h := range after.Hits.Hits {
		if h.ID != before[i].ID {
			return fmt.Errorf("chunk IDs/order changed during repair")
		}
		if err := verifyRepairedChunk(before[i], h, repaired[i], dimension); err != nil {
			return err
		}
	}
	return nil
}

func verifyRepairedChunk(before, after repairHit, repaired bool, dimension int) error {
	var oldDoc, newDoc map[string]json.RawMessage
	if err := json.Unmarshal(before.Source, &oldDoc); err != nil {
		return err
	}
	if err := json.Unmarshal(after.Source, &newDoc); err != nil {
		return err
	}
	if repaired {
		var vector []float32
		if err := json.Unmarshal(newDoc["embedding"], &vector); err != nil {
			return err
		}
		if len(vector) != dimension {
			return fmt.Errorf("repaired vector has dimension %d", len(vector))
		}
		delete(oldDoc, "embedding")
		delete(newDoc, "embedding")
	}
	a, _ := json.Marshal(oldDoc)
	b, _ := json.Marshal(newDoc)
	if !bytes.Equal(a, b) {
		return fmt.Errorf("non-vector data or pre-existing vector changed for chunk %s", after.ID)
	}
	return nil
}

func (c *Client) repairJSON(ctx context.Context, method, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, "/"+c.index+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Stream hands back the raw response (we own and close the body); it
	// replaces Perform, deprecated since opensearch-go v4.7.
	resp, err := c.os.Client.Stream(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // response cleanup
	if resp.StatusCode >= 300 {
		return fmt.Errorf("OpenSearch repair request failed: HTTP %d", resp.StatusCode)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
