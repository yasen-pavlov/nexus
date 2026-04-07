// Package search provides the OpenSearch client for document indexing and search.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
	opensearch "github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"go.uber.org/zap"
)

// ErrNotFound is returned when a document is not found.
var ErrNotFound = errors.New("document not found")

const defaultIndex = "nexus-documents"

// Client wraps the OpenSearch client for document operations.
type Client struct {
	os                 *opensearchapi.Client
	log                *zap.Logger
	index              string
	embeddingDimension int
}

// New creates a new OpenSearch client and verifies the connection.
func New(ctx context.Context, url string, log *zap.Logger) (*Client, error) {
	if log == nil {
		log = zap.NewNop()
	}
	osClient, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{url},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("search: create client: %w", err)
	}

	_, err = osClient.Info(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("search: connect to %s: %w", url, err)
	}

	log.Info("connected to OpenSearch", zap.String("url", url))
	return &Client{os: osClient, log: log, index: defaultIndex}, nil
}

// NewWithIndex creates a client with a custom index name (for testing).
func NewWithIndex(ctx context.Context, url string, index string, log *zap.Logger) (*Client, error) {
	c, err := New(ctx, url, log)
	if err != nil {
		return nil, err
	}
	c.index = index
	return c, nil
}

// EnsureIndex creates the search index with the appropriate mapping.
// Pass embeddingDimension > 0 to enable k-NN vector fields, or 0 for BM25-only.
func (c *Client) EnsureIndex(ctx context.Context, embeddingDimension int) error {
	c.embeddingDimension = embeddingDimension

	_, err := c.os.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{
		Indices: []string{c.index},
	})
	if err == nil {
		return nil // index already exists
	}

	mapping := indexMappingJSON(embeddingDimension)
	_, err = c.os.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index: c.index,
		Body:  strings.NewReader(mapping),
	})
	if err != nil {
		return fmt.Errorf("search: create index: %w", err)
	}

	c.log.Info("created search index", zap.String("index", c.index), zap.Int("embedding_dim", embeddingDimension))
	return nil
}

// docID returns the composite document ID for OpenSearch.
func docID(doc *model.Document) string {
	return doc.SourceType + ":" + doc.SourceName + ":" + doc.SourceID
}

// IndexDocument indexes a single document (BM25-only mode, no chunking).
func (c *Client) IndexDocument(ctx context.Context, doc *model.Document) error {
	if doc.ID == uuid.Nil {
		doc.ID = uuid.New()
	}
	doc.IndexedAt = time.Now()

	// Convert to a chunk (chunk 0 with full content)
	chunk := model.Chunk{
		ID:          docID(doc) + ":0",
		ParentID:    docID(doc),
		ChunkIndex:  0,
		Title:       doc.Title,
		Content:     doc.Content,
		FullContent: doc.Content,
		SourceType:  doc.SourceType,
		SourceName:  doc.SourceName,
		SourceID:    doc.SourceID,
		Metadata:    doc.Metadata,
		URL:         doc.URL,
		Visibility:  doc.Visibility,
		CreatedAt:   doc.CreatedAt,
		IndexedAt:   doc.IndexedAt,
	}

	body, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("search: marshal document: %w", err)
	}

	_, err = c.os.Index(ctx, opensearchapi.IndexReq{
		Index:      c.index,
		DocumentID: chunk.ID,
		Body:       bytes.NewReader(body),
	})
	if err != nil {
		return fmt.Errorf("search: index document: %w", err)
	}

	return nil
}

// IndexChunks indexes multiple chunks using the bulk API.
func (c *Client) IndexChunks(ctx context.Context, chunks []model.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for i := range chunks {
		chunks[i].IndexedAt = time.Now()

		action := map[string]any{
			"index": map[string]any{
				"_index": c.index,
				"_id":    chunks[i].ID,
			},
		}
		actionLine, _ := json.Marshal(action)
		buf.Write(actionLine)
		buf.WriteByte('\n')

		docLine, err := json.Marshal(chunks[i])
		if err != nil {
			return fmt.Errorf("search: marshal chunk %d: %w", i, err)
		}
		buf.Write(docLine)
		buf.WriteByte('\n')
	}

	_, err := c.os.Bulk(ctx, opensearchapi.BulkReq{
		Body: &buf,
	})
	if err != nil {
		return fmt.Errorf("search: bulk index: %w", err)
	}

	return nil
}

// Search performs a BM25 full-text search (no vector search).
func (c *Client) Search(ctx context.Context, req model.SearchRequest) (*model.SearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 20
	}

	matchQuery := map[string]any{
		"multi_match": map[string]any{
			"query":  req.Query,
			"fields": []string{"title^2", "content"},
		},
	}
	filters := buildFilterClauses(req)

	query := map[string]any{
		"query": buildSearchQuery(matchQuery, filters),
		"highlight": map[string]any{
			"fields": map[string]any{
				"content": map[string]any{
					"fragment_size":       200,
					"number_of_fragments": 1,
				},
			},
			"pre_tags":  []string{"<mark>"},
			"post_tags": []string{"</mark>"},
		},
		"size":             req.Limit * 3, // over-fetch for dedup
		"track_total_hits": true,
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("search: marshal query: %w", err)
	}

	resp, err := c.os.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{c.index},
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		return nil, fmt.Errorf("search: query: %w", err)
	}

	return c.hitsToResult(resp, req, nil)
}

// HybridSearch combines BM25 text search with k-NN vector search using RRF.
func (c *Client) HybridSearch(ctx context.Context, req model.SearchRequest, queryEmbedding []float32) (*model.SearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 20
	}
	fetchSize := req.Limit * 3

	matchQuery := map[string]any{
		"multi_match": map[string]any{
			"query":  req.Query,
			"fields": []string{"title^2", "content"},
		},
	}
	filters := buildFilterClauses(req)

	// BM25 query with filters
	bm25Query := map[string]any{
		"query": buildSearchQuery(matchQuery, filters),
		"highlight": map[string]any{
			"fields": map[string]any{
				"content": map[string]any{
					"fragment_size":       200,
					"number_of_fragments": 1,
				},
			},
			"pre_tags":  []string{"<mark>"},
			"post_tags": []string{"</mark>"},
		},
		"size": fetchSize,
	}

	bm25Body, _ := json.Marshal(bm25Query)
	bm25Resp, err := c.os.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{c.index},
		Body:    bytes.NewReader(bm25Body),
	})
	if err != nil {
		return nil, fmt.Errorf("search: bm25 query: %w", err)
	}

	// k-NN query with minimum score threshold and filters
	knnMatchQuery := map[string]any{
		"knn": map[string]any{
			"embedding": map[string]any{
				"vector": queryEmbedding,
				"k":      fetchSize,
			},
		},
	}
	knnQuery := map[string]any{
		"query":     buildSearchQuery(knnMatchQuery, filters),
		"min_score": knnMinScore,
		"size":      fetchSize,
	}

	knnBody, _ := json.Marshal(knnQuery)
	knnResp, err := c.os.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{c.index},
		Body:    bytes.NewReader(knnBody),
	})
	if err != nil {
		return nil, fmt.Errorf("search: knn query: %w", err)
	}

	return c.hitsToResult(bm25Resp, req, knnResp)
}

func (c *Client) hitsToResult(bm25Resp *opensearchapi.SearchResp, req model.SearchRequest, knnResp *opensearchapi.SearchResp) (*model.SearchResult, error) {

	// Parse BM25 hits — these are the primary results
	chunkData := make(map[string]*rankedChunk)

	for rank, hit := range bm25Resp.Hits.Hits {
		var chunk model.Chunk
		raw, _ := json.Marshal(hit.Source)
		if err := json.Unmarshal(raw, &chunk); err != nil {
			continue
		}

		headline := ""
		if contentHL, ok := hit.Highlight["content"]; ok && len(contentHL) > 0 {
			headline = contentHL[0]
		}

		rrfScore := 1.0 / float64(rrfK+rank)
		if _, exists := chunkData[chunk.ParentID]; !exists {
			chunkData[chunk.ParentID] = &rankedChunk{
				parentID: chunk.ParentID,
				doc: model.Document{
					SourceType: chunk.SourceType,
					SourceName: chunk.SourceName,
					SourceID:   chunk.SourceID,
					Title:      chunk.Title,
					Content:    chunk.Content,
					Metadata:   chunk.Metadata,
					URL:        chunk.URL,
					Visibility: chunk.Visibility,
					CreatedAt:  chunk.CreatedAt,
					IndexedAt:  chunk.IndexedAt,
				},
				headline: headline,
				rrfScore: rrfScore,
			}
		}
	}

	// Parse k-NN hits with tiered scoring:
	// - Results also in BM25: full RRF contribution (boost)
	// - Results only in k-NN: heavily penalized (knnOnlyWeight)
	// Track which parents came from BM25 to correctly apply tiering
	// even when multiple chunks of the same parent appear in k-NN.
	bm25Parents := make(map[string]bool, len(chunkData))
	for pid := range chunkData {
		bm25Parents[pid] = true
	}

	if knnResp != nil {
		for rank, hit := range knnResp.Hits.Hits {
			var chunk model.Chunk
			raw, _ := json.Marshal(hit.Source)
			if err := json.Unmarshal(raw, &chunk); err != nil {
				continue
			}

			rrfScore := 1.0 / float64(rrfK+rank)
			inBM25 := bm25Parents[chunk.ParentID]

			if !inBM25 {
				rrfScore *= knnOnlyWeight
			}

			if existing, ok := chunkData[chunk.ParentID]; ok {
				existing.rrfScore += rrfScore
			} else {
				chunkData[chunk.ParentID] = &rankedChunk{
					parentID: chunk.ParentID,
					doc: model.Document{
						SourceType: chunk.SourceType,
						SourceName: chunk.SourceName,
						SourceID:   chunk.SourceID,
						Title:      chunk.Title,
						Content:    chunk.Content,
						Metadata:   chunk.Metadata,
						URL:        chunk.URL,
						Visibility: chunk.Visibility,
						CreatedAt:  chunk.CreatedAt,
						IndexedAt:  chunk.IndexedAt,
					},
					rrfScore: rrfScore,
				}
			}
		}
	}

	// Sort by RRF score
	var results []*rankedChunk
	for _, rc := range chunkData {
		results = append(results, rc)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].rrfScore > results[j].rrfScore
	})

	// Compute facets from the full deduped result set (before pagination)
	facets := computeFacets(results)

	// Apply offset and limit
	totalCount := len(results)
	if req.Offset > 0 && req.Offset < len(results) {
		results = results[req.Offset:]
	} else if req.Offset >= len(results) {
		results = nil
	}
	if len(results) > req.Limit {
		results = results[:req.Limit]
	}

	var hits []model.DocumentHit
	for _, rc := range results {
		hits = append(hits, model.DocumentHit{
			Document: rc.doc,
			Rank:     rc.rrfScore,
			Headline: rc.headline,
		})
	}

	return &model.SearchResult{
		Documents:  hits,
		TotalCount: totalCount,
		Query:      req.Query,
		Facets:     facets,
	}, nil
}

// DeleteBySource deletes all documents from a specific source.
func (c *Client) DeleteBySource(ctx context.Context, sourceType, sourceName string) error {
	query := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []map[string]any{
					{"term": map[string]any{"source_type": sourceType}},
					{"term": map[string]any{"source_name": sourceName}},
				},
			},
		},
	}

	body, err := json.Marshal(query)
	if err != nil {
		return fmt.Errorf("search: marshal delete query: %w", err)
	}

	_, err = c.os.Document.DeleteByQuery(ctx, opensearchapi.DocumentDeleteByQueryReq{
		Indices: []string{c.index},
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		return fmt.Errorf("search: delete by source: %w", err)
	}

	return nil
}

// DeleteIndex deletes the search index (for testing).
func (c *Client) DeleteIndex(ctx context.Context) error {
	_, err := c.os.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{
		Indices: []string{c.index},
	})
	return err
}

// Refresh forces a refresh of the index (for testing — makes indexed docs searchable immediately).
func (c *Client) Refresh(ctx context.Context) error {
	_, err := c.os.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{
		Indices: []string{c.index},
	})
	return err
}
