package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	opensearch "github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// fakeRepairIndex is a one-document index the repair command can be pointed
// at. Hooks let a test misbehave the way a live cluster can: report more
// chunks than it returns, reject a write, or change a chunk mid-repair.
type fakeRepairIndex struct {
	docs        []map[string]any
	reportTotal int                      // 0 = len(docs)
	updateCode  int                      // 0 = accept the write
	afterUpdate func(f *fakeRepairIndex) // runs after the first accepted write
	updates     int
}

func (f *fakeRepairIndex) client(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_search"):
			hits := make([]map[string]any, len(f.docs))
			for i, doc := range f.docs {
				hits[i] = map[string]any{"_id": string(rune('a' + i)), "_seq_no": 7, "_primary_term": 1, "_source": doc}
			}
			total := f.reportTotal
			if total == 0 {
				total = len(f.docs)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"total": map[string]any{"value": total}, "hits": hits}})
		case strings.Contains(r.URL.Path, "/_update/"):
			if f.updateCode != 0 {
				w.WriteHeader(f.updateCode)
				_, _ = w.Write([]byte(`{"error":{"type":"version_conflict_engine_exception"}}`))
				return
			}
			var body struct {
				Doc map[string]any `json:"doc"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			f.docs[int(id[0]-'a')]["embedding"] = body.Doc["embedding"]
			f.updates++
			if f.afterUpdate != nil && f.updates == 1 {
				f.afterUpdate(f)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	osClient, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{srv.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	return &Client{os: osClient, index: "nexus-documents"}
}

type scriptedEmbedder struct {
	dimension int
	vector    []float32
	err       error
}

func (e scriptedEmbedder) Dimension() int { return e.dimension }
func (e scriptedEmbedder) Embed(_ context.Context, texts []string, _ string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	vectors := make([][]float32, len(texts))
	for i := range texts {
		vectors[i] = e.vector
	}
	return vectors, nil
}

func missingVectorDocs() []map[string]any {
	return []map[string]any{
		{"parent_id": "parent", "content": "needs a vector", "chunk_index": 0},
		{"parent_id": "parent", "content": "already embedded", "chunk_index": 1, "embedding": []any{9.0, 8.0}},
	}
}

func anyText(string) bool { return true }

// The command runs against a live index holding the only copy of the vectors,
// so every way it can go wrong has to stop it before, or instead of, leaving
// the document in a state nobody asked for.
func TestRepairMissingEmbeddings_RefusesUnsafeRepairs(t *testing.T) {
	good := scriptedEmbedder{dimension: 2, vector: []float32{1, 2}}
	cases := []struct {
		name         string
		index        *fakeRepairIndex
		embedder     scriptedEmbedder
		wantErr      string
		wantUpdates  int
		wantSnapshot bool
	}{
		{
			// More chunks exist than the search returned: a snapshot of part
			// of the document would be a false sense of a backup.
			name:     "search returned only part of the document",
			index:    &fakeRepairIndex{docs: missingVectorDocs(), reportTotal: 3},
			embedder: good,
			wantErr:  "incomplete snapshot",
		},
		{
			name:     "no chunk has that parent id",
			index:    &fakeRepairIndex{},
			embedder: good,
			wantErr:  "incomplete snapshot",
		},
		{
			name:         "embedding provider fails",
			index:        &fakeRepairIndex{docs: missingVectorDocs()},
			embedder:     scriptedEmbedder{dimension: 2, err: errors.New("provider down")},
			wantErr:      "provider down",
			wantSnapshot: true,
		},
		{
			// A vector of the wrong size would be rejected by the index, or
			// worse, accepted by an index created for another model.
			name:         "embedder returns the wrong dimension",
			index:        &fakeRepairIndex{docs: missingVectorDocs()},
			embedder:     scriptedEmbedder{dimension: 2, vector: []float32{1, 2, 3}},
			wantErr:      "dimension mismatch",
			wantSnapshot: true,
		},
		{
			// The chunk changed since the snapshot (a sync is running): the
			// optimistic lock makes OpenSearch answer 409.
			name:         "chunk was modified since the snapshot",
			index:        &fakeRepairIndex{docs: missingVectorDocs(), updateCode: http.StatusConflict},
			embedder:     good,
			wantErr:      "HTTP 409",
			wantSnapshot: true,
		},
		{
			name: "text changed while the repair ran",
			index: &fakeRepairIndex{docs: missingVectorDocs(), afterUpdate: func(f *fakeRepairIndex) {
				f.docs[1]["content"] = "rewritten by a concurrent sync"
			}},
			embedder:     good,
			wantErr:      "changed for chunk b",
			wantUpdates:  1,
			wantSnapshot: true,
		},
		{
			name: "a chunk was added while the repair ran",
			index: &fakeRepairIndex{docs: missingVectorDocs(), afterUpdate: func(f *fakeRepairIndex) {
				f.docs = append(f.docs, map[string]any{"parent_id": "parent", "content": "new chunk", "chunk_index": 2})
			}},
			embedder:     good,
			wantErr:      "chunk count changed",
			wantUpdates:  1,
			wantSnapshot: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := filepath.Join(t.TempDir(), "before.json")
			err := tc.index.client(t).RepairMissingEmbeddings(context.Background(), "parent", snapshot, tc.embedder, anyText)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
			if tc.index.updates != tc.wantUpdates {
				t.Errorf("wrote %d vectors, want %d", tc.index.updates, tc.wantUpdates)
			}
			if _, statErr := os.Stat(snapshot); (statErr == nil) != tc.wantSnapshot {
				t.Errorf("snapshot written = %v, want %v", statErr == nil, tc.wantSnapshot)
			}
		})
	}
}

func TestRepairMissingEmbeddings_RequiresParentSnapshotAndEmbedder(t *testing.T) {
	index := &fakeRepairIndex{docs: missingVectorDocs()}
	client := index.client(t)
	snapshot := filepath.Join(t.TempDir(), "before.json")
	good := scriptedEmbedder{dimension: 2, vector: []float32{1, 2}}

	if err := client.RepairMissingEmbeddings(context.Background(), "", snapshot, good, anyText); err == nil {
		t.Error("ran without a parent id")
	}
	if err := client.RepairMissingEmbeddings(context.Background(), "parent", "", good, anyText); err == nil {
		t.Error("ran without a snapshot path")
	}
	if err := client.RepairMissingEmbeddings(context.Background(), "parent", snapshot, nil, anyText); err == nil {
		t.Error("ran without an embedder")
	}
	if index.updates != 0 {
		t.Errorf("wrote %d vectors despite missing arguments", index.updates)
	}
}
