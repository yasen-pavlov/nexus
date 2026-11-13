package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	opensearch "github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

type repairEmbedder struct{}

func (repairEmbedder) Dimension() int { return 2 }
func (repairEmbedder) Embed(_ context.Context, texts []string, _ string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range texts {
		vectors[i] = []float32{1, 2}
	}
	return vectors, nil
}

func TestRepairMissingEmbeddingsOnlyChangesEligibleMissingVectors(t *testing.T) {
	snapshot := filepath.Join(t.TempDir(), "before.json")
	docs := []map[string]any{
		{"parent_id": "parent", "content": "eligible", "chunk_index": 0},
		{"parent_id": "parent", "content": "existing", "chunk_index": 1, "embedding": []any{9.0, 8.0}},
		{"parent_id": "parent", "content": "noise", "chunk_index": 2},
		{"parent_id": "parent", "content": "hidden", "chunk_index": 3, "hidden": true},
	}
	updates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_search"):
			var query map[string]any
			_ = json.NewDecoder(r.Body).Decode(&query)
			term := query["query"].(map[string]any)["term"].(map[string]any)
			if term["parent_id"] != "parent" {
				t.Error("repair is not exact-parent scoped")
			}
			hits := make([]map[string]any, len(docs))
			for i, doc := range docs {
				hits[i] = map[string]any{"_id": string(rune('a' + i)), "_seq_no": 7, "_primary_term": 1, "_source": doc}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"total": map[string]any{"value": len(docs)}, "hits": hits}})
		case strings.Contains(r.URL.Path, "/_update/"):
			if _, err := os.Stat(snapshot); err != nil {
				t.Error("update preceded snapshot")
			}
			if !strings.HasSuffix(r.URL.Path, "/a") || r.URL.Query().Get("if_seq_no") != "7" || r.URL.Query().Get("if_primary_term") != "1" {
				t.Error("unexpected target or missing optimistic lock")
			}
			var body struct {
				Doc map[string]any `json:"doc"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Doc) != 1 || body.Doc["embedding"] == nil {
				t.Error("update changes more than vector")
			}
			docs[0]["embedding"] = body.Doc["embedding"]
			updates++
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/_refresh"):
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	osClient, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{srv.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{os: osClient, index: "nexus-documents"}
	if err := client.RepairMissingEmbeddings(context.Background(), "parent", snapshot, repairEmbedder{}, func(text string) bool { return text != "noise" }); err != nil {
		t.Fatal(err)
	}
	if updates != 1 {
		t.Fatalf("updated %d chunks", updates)
	}
	info, err := os.Stat(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot permissions %o", info.Mode().Perm())
	}
	if err := client.RepairMissingEmbeddings(context.Background(), "parent", snapshot, repairEmbedder{}, func(string) bool { return true }); err == nil {
		t.Fatal("overwrote existing snapshot")
	}
	if updates != 1 {
		t.Fatal("second repair wrote after snapshot failure")
	}
}
