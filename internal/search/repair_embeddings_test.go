package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

// repairFake serves the three OpenSearch endpoints the repair uses, over four
// chunks of parent "parent" of which the first and third lack a vector. Its
// fields let a test break one step.
type repairFake struct {
	docs []map[string]any
	// failPath and failCode fail every request whose path contains failPath.
	failPath string
	failCode int
	// firstTotal overrides the hit total reported by the pre-repair search.
	firstTotal int
	// rewriteAfter alters the hits of the post-repair search.
	rewriteAfter func(hits []map[string]any) []map[string]any
	searches     int
	updates      int
}

func newRepairFake() *repairFake {
	return &repairFake{docs: []map[string]any{
		{"parent_id": "parent", "content": "missing", "chunk_index": 0},
		{"parent_id": "parent", "content": "existing", "chunk_index": 1, "embedding": []any{9.0, 8.0}},
		{"parent_id": "parent", "content": "also missing", "chunk_index": 2},
		{"parent_id": "parent", "content": "last", "chunk_index": 3, "embedding": []any{7.0, 6.0}},
	}}
}

func (f *repairFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.failPath != "" && strings.Contains(r.URL.Path, f.failPath) {
		w.WriteHeader(f.failCode)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/_search"):
		f.searches++
		hits := make([]map[string]any, len(f.docs))
		for i, doc := range f.docs {
			hits[i] = map[string]any{"_id": "chunk-" + strconv.Itoa(i), "_seq_no": 7, "_primary_term": 1, "_source": doc}
		}
		total := len(hits)
		if f.searches == 1 && f.firstTotal != 0 {
			total = f.firstTotal
		}
		if f.searches > 1 && f.rewriteAfter != nil {
			hits = f.rewriteAfter(hits)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"total": map[string]any{"value": total}, "hits": hits}})
	case strings.Contains(r.URL.Path, "/_update/"):
		f.updates++
		var body struct {
			Doc map[string]any `json:"doc"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		i, _ := strconv.Atoi(r.URL.Path[strings.LastIndexByte(r.URL.Path, '-')+1:])
		f.docs[i]["embedding"] = body.Doc["embedding"]
		_, _ = w.Write([]byte(`{}`))
	default:
		_, _ = w.Write([]byte(`{}`))
	}
}

// repairEmbedderFunc is a 2-dimensional embedder with a per-test Embed.
type repairEmbedderFunc func(texts []string) ([][]float32, error)

func (repairEmbedderFunc) Dimension() int { return 2 }
func (f repairEmbedderFunc) Embed(_ context.Context, texts []string, _ string) ([][]float32, error) {
	return f(texts)
}

// withSource returns hits with the _source of hits[i] replaced by a copy
// that has key set to value, leaving the fake's stored documents alone.
func withSource(hits []map[string]any, i int, key string, value any) []map[string]any {
	source := map[string]any{}
	for k, v := range hits[i]["_source"].(map[string]any) {
		source[k] = v
	}
	source[key] = value
	hits[i] = map[string]any{"_id": hits[i]["_id"], "_seq_no": 7, "_primary_term": 1, "_source": source}
	return hits
}

// The repair writes to the live index, so each guard has to stop it: before
// the first write when the input is not what it expects, and with an error
// rather than "verified" when the result is not what it wrote.
func TestRepairMissingEmbeddingsRefusesAndReportsFailures(t *testing.T) {
	tests := []struct {
		name        string
		arrange     func(f *repairFake)
		embed       repairEmbedderFunc
		wantErr     string
		wantUpdates int
	}{
		{
			name:    "search fails",
			arrange: func(f *repairFake) { f.failPath, f.failCode = "/_search", http.StatusInternalServerError },
			wantErr: "HTTP 500",
		},
		{
			name:    "search returned fewer chunks than exist",
			arrange: func(f *repairFake) { f.firstTotal = 12000 },
			wantErr: "refusing incomplete snapshot: total=12000 returned=4",
		},
		{
			name:    "chunk of another parent",
			arrange: func(f *repairFake) { f.docs[3]["parent_id"] = "parent-2" },
			wantErr: "unexpected parent",
		},
		{
			name:    "chunk source is not a document",
			arrange: func(f *repairFake) { f.docs[2] = map[string]any{"parent_id": 5} },
			wantErr: "cannot unmarshal",
		},
		{
			name:    "embedder fails",
			embed:   func([]string) ([][]float32, error) { return nil, errors.New("voyage is down") },
			wantErr: "voyage is down",
		},
		{
			name:    "embedder returns too few vectors",
			embed:   func([]string) ([][]float32, error) { return [][]float32{{1, 2}}, nil },
			wantErr: "embedding count mismatch",
		},
		{
			name:    "embedder returns another dimension",
			embed:   func([]string) ([][]float32, error) { return [][]float32{{1, 2, 3}, {1, 2, 3}}, nil },
			wantErr: "embedding dimension mismatch: 3",
		},
		{
			name:    "chunk changed since the snapshot",
			arrange: func(f *repairFake) { f.failPath, f.failCode = "/_update/", http.StatusConflict },
			wantErr: "HTTP 409",
		},
		{
			name:        "refresh fails",
			arrange:     func(f *repairFake) { f.failPath, f.failCode = "/_refresh", http.StatusInternalServerError },
			wantErr:     "HTTP 500",
			wantUpdates: 2,
		},
		{
			name: "chunk disappeared during repair",
			arrange: func(f *repairFake) {
				f.rewriteAfter = func(hits []map[string]any) []map[string]any { return hits[:3] }
			},
			wantErr:     "chunk count changed",
			wantUpdates: 2,
		},
		{
			name: "chunk order changed during repair",
			arrange: func(f *repairFake) {
				f.rewriteAfter = func(hits []map[string]any) []map[string]any {
					hits[1], hits[3] = hits[3], hits[1]
					return hits
				}
			},
			wantErr:     "chunk IDs/order changed",
			wantUpdates: 2,
		},
		{
			name: "text changed during repair",
			arrange: func(f *repairFake) {
				f.rewriteAfter = func(hits []map[string]any) []map[string]any {
					return withSource(hits, 2, "content", "edited")
				}
			},
			wantErr:     "changed for chunk chunk-2",
			wantUpdates: 2,
		},
		{
			name: "existing vector changed during repair",
			arrange: func(f *repairFake) {
				f.rewriteAfter = func(hits []map[string]any) []map[string]any {
					return withSource(hits, 1, "embedding", []any{0.0, 0.0})
				}
			},
			wantErr:     "changed for chunk chunk-1",
			wantUpdates: 2,
		},
		{
			name: "written vector did not land",
			arrange: func(f *repairFake) {
				f.rewriteAfter = func(hits []map[string]any) []map[string]any {
					return withSource(hits, 0, "embedding", []any{1.0})
				}
			},
			wantErr:     "repaired vector has dimension 1",
			wantUpdates: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newRepairFake()
			if tt.arrange != nil {
				tt.arrange(fake)
			}
			embed := tt.embed
			if embed == nil {
				embed = func(texts []string) ([][]float32, error) {
					return repairEmbedder{}.Embed(context.Background(), texts, "")
				}
			}
			srv := httptest.NewServer(fake)
			defer srv.Close()
			osClient, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{srv.URL}}})
			if err != nil {
				t.Fatal(err)
			}
			client := &Client{os: osClient, index: "nexus-documents"}

			err = client.RepairMissingEmbeddings(context.Background(), "parent", filepath.Join(t.TempDir(), "before.json"), embed, func(string) bool { return true })
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
			}
			if fake.updates != tt.wantUpdates {
				t.Errorf("wrote %d vectors, want %d", fake.updates, tt.wantUpdates)
			}
		})
	}
}

func TestRepairMissingEmbeddingsRequiresParentSnapshotAndEmbedder(t *testing.T) {
	client := &Client{index: "nexus-documents"}
	ctx := context.Background()
	eligible := func(string) bool { return true }
	for name, err := range map[string]error{
		"no parent":   client.RepairMissingEmbeddings(ctx, "", "before.json", repairEmbedder{}, eligible),
		"no snapshot": client.RepairMissingEmbeddings(ctx, "parent", "", repairEmbedder{}, eligible),
		"no embedder": client.RepairMissingEmbeddings(ctx, "parent", "before.json", nil, eligible),
	} {
		if err == nil {
			t.Errorf("%s: repair started", name)
		}
	}
}

// More chunks than one embedder call carries: every chunk gets its own
// vector, in order, across the batch boundary.
func TestRepairMissingEmbeddingsBatches(t *testing.T) {
	const chunks = repairBatchSize + 6
	fake := &repairFake{}
	for i := range chunks {
		fake.docs = append(fake.docs, map[string]any{"parent_id": "parent", "content": fmt.Sprintf("chunk %d", i), "chunk_index": i})
	}
	var calls []int
	embed := repairEmbedderFunc(func(texts []string) ([][]float32, error) {
		calls = append(calls, len(texts))
		vectors := make([][]float32, len(texts))
		for i, text := range texts {
			var n float32
			_, _ = fmt.Sscanf(text, "chunk %f", &n)
			vectors[i] = []float32{n, 1}
		}
		return vectors, nil
	})
	srv := httptest.NewServer(fake)
	defer srv.Close()
	osClient, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{srv.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{os: osClient, index: "nexus-documents"}

	if err := client.RepairMissingEmbeddings(context.Background(), "parent", filepath.Join(t.TempDir(), "before.json"), embed, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != repairBatchSize || calls[1] != 6 {
		t.Errorf("embedder calls = %v, want [%d 6]", calls, repairBatchSize)
	}
	for i, doc := range fake.docs {
		vector, _ := doc["embedding"].([]any)
		if len(vector) != 2 || vector[0] != float64(i) {
			t.Fatalf("chunk %d got vector %v", i, doc["embedding"])
		}
	}
}
