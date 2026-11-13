package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/muty/nexus/internal/embedding"
	"github.com/muty/nexus/internal/model"
)

type repairHit struct {
	ID          string          `json:"_id"`
	SeqNo       int64           `json:"_seq_no"`
	PrimaryTerm int64           `json:"_primary_term"`
	Source      json.RawMessage `json:"_source"`
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
	var before struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []repairHit `json:"hits"`
		} `json:"hits"`
	}
	if err := c.repairJSON(ctx, "POST", "/_search", query, &before); err != nil {
		return err
	}
	hits := before.Hits.Hits
	if len(hits) == 0 || before.Hits.Total.Value != len(hits) {
		return fmt.Errorf("refusing incomplete snapshot: total=%d returned=%d", before.Hits.Total.Value, len(hits))
	}
	backup, err := json.MarshalIndent(before, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(snapshot, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(backup)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Printf("snapshot: chunks=%d sha256=%x\n", len(hits), sha256.Sum256(backup))
	var missing []int
	var texts []string
	for i, h := range hits {
		var chunk model.Chunk
		if err := json.Unmarshal(h.Source, &chunk); err != nil {
			return err
		}
		if chunk.ParentID != parentID {
			return fmt.Errorf("unexpected parent in snapshot")
		}
		if len(chunk.Embedding) == 0 && !chunk.Hidden && eligible(chunk.Content) {
			missing = append(missing, i)
			texts = append(texts, chunk.Content)
		}
	}
	for start := 0; start < len(texts); start += 64 {
		end := min(start+64, len(texts))
		vectors, err := embedder.Embed(ctx, texts[start:end], embedding.InputTypeDocument)
		if err != nil {
			return err
		}
		if len(vectors) != end-start {
			return fmt.Errorf("embedding count mismatch")
		}
		for i, vector := range vectors {
			if len(vector) != embedder.Dimension() {
				return fmt.Errorf("embedding dimension mismatch: %d", len(vector))
			}
			h := hits[missing[start+i]]
			path := fmt.Sprintf("/_update/%s?if_seq_no=%d&if_primary_term=%d", url.PathEscape(h.ID), h.SeqNo, h.PrimaryTerm)
			if err := c.repairJSON(ctx, "POST", path, map[string]any{"doc": map[string]any{"embedding": vector}}, nil); err != nil {
				return err
			}
		}
		fmt.Printf("repaired vectors: %d/%d dimension=%d\n", end, len(texts), embedder.Dimension())
	}
	if err := c.repairJSON(ctx, "POST", "/_refresh", nil, nil); err != nil {
		return err
	}
	var after struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []repairHit `json:"hits"`
		} `json:"hits"`
	}
	if err := c.repairJSON(ctx, "POST", "/_search", query, &after); err != nil {
		return err
	}
	if after.Hits.Total.Value != len(hits) || len(after.Hits.Hits) != len(hits) {
		return fmt.Errorf("chunk count changed during repair")
	}
	repaired := make(map[int]bool, len(missing))
	for _, i := range missing {
		repaired[i] = true
	}
	for i, h := range after.Hits.Hits {
		if h.ID != hits[i].ID {
			return fmt.Errorf("chunk IDs/order changed during repair")
		}
		var oldDoc, newDoc map[string]json.RawMessage
		if err := json.Unmarshal(hits[i].Source, &oldDoc); err != nil {
			return err
		}
		if err := json.Unmarshal(h.Source, &newDoc); err != nil {
			return err
		}
		if repaired[i] {
			var vector []float32
			if err := json.Unmarshal(newDoc["embedding"], &vector); err != nil {
				return err
			}
			if len(vector) != embedder.Dimension() {
				return fmt.Errorf("repaired vector has dimension %d", len(vector))
			}
			delete(oldDoc, "embedding")
			delete(newDoc, "embedding")
		}
		a, _ := json.Marshal(oldDoc)
		b, _ := json.Marshal(newDoc)
		if !bytes.Equal(a, b) {
			return fmt.Errorf("non-vector data or pre-existing vector changed for chunk %s", h.ID)
		}
	}
	fmt.Printf("verified: chunks=%d repaired=%d dimension=%d IDs/text/metadata/existing-vectors unchanged\n", len(hits), len(missing), embedder.Dimension())
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
