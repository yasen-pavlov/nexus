// Package search provides the OpenSearch client for document indexing and search.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/lang"
	"github.com/muty/nexus/internal/model"
	opensearch "github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"go.uber.org/zap"
)

// ErrNotFound is returned when a document is not found.
var ErrNotFound = errors.New("document not found")

const (
	defaultIndex    = "nexus-documents"
	hybridPipeline  = "nexus-hybrid"
	rrfRankConstant = 60
)

// Client wraps the OpenSearch client for document operations.
type Client struct {
	os                 *opensearchapi.Client
	log                *zap.Logger
	index              string
	embeddingDimension int
	// languages drives per-field language analyzers on title/content and
	// the set of fields multi_match searches against. Empty is allowed and
	// falls back to standard-analyzer-only (pre-stemming) behavior.
	languages []lang.Language
	// minShouldMatch controls how many query terms must appear in a single
	// field for a document to match in BM25. When the Settings UI lands
	// this becomes a DB-backed tunable alongside RerankMinScore.
	minShouldMatch string
}

// New creates a new OpenSearch client and verifies the connection.
// languages configures the per-field language analyzers on text fields;
// pass lang.Default() in production and nil in tests that don't care.
func New(ctx context.Context, url string, log *zap.Logger, languages []lang.Language) (*Client, error) {
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
	return &Client{os: osClient, log: log, index: defaultIndex, languages: languages, minShouldMatch: DefaultMinShouldMatch}, nil
}

// NewWithIndex creates a client with a custom index name (for testing).
func NewWithIndex(ctx context.Context, url string, index string, log *zap.Logger, languages []lang.Language) (*Client, error) {
	c, err := New(ctx, url, log, languages)
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

	mapping := indexMappingJSON(embeddingDimension, c.languages)
	_, err = c.os.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index: c.index,
		Body:  strings.NewReader(mapping),
	})
	if err != nil {
		return fmt.Errorf("search: create index: %w", err)
	}

	c.log.Info("created search index", zap.String("index", c.index), zap.Int("embedding_dim", embeddingDimension))

	if embeddingDimension > 0 {
		if err := c.ensureHybridPipeline(ctx); err != nil {
			return err
		}
	}

	return nil
}

// ensureHybridPipeline creates the RRF search pipeline if it doesn't exist.
func (c *Client) ensureHybridPipeline(ctx context.Context) error {
	pipeline := fmt.Sprintf(`{
		"phase_results_processors": [{
			"score-ranker-processor": {
				"combination": {
					"technique": "rrf",
					"parameters": {
						"rank_constant": %d
					}
				}
			}
		}]
	}`, rrfRankConstant)

	// Use raw HTTP PUT — the Go client doesn't have a typed search pipeline API
	path := fmt.Sprintf("/_search/pipeline/%s", hybridPipeline)
	httpReq, err := opensearch.BuildRequest(http.MethodPut, path, strings.NewReader(pipeline), nil, nil)
	if err != nil {
		return fmt.Errorf("search: build pipeline request: %w", err)
	}
	httpReq = httpReq.WithContext(ctx)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.os.Client.Perform(httpReq)
	if err != nil {
		return fmt.Errorf("search: create hybrid pipeline: %w", err)
	}
	resp.Body.Close() //nolint:errcheck // best-effort

	c.log.Info("created hybrid search pipeline", zap.String("pipeline", hybridPipeline))
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

// highlightConfig returns the standard highlight configuration.
func highlightConfig() map[string]any {
	return map[string]any{
		"fields": map[string]any{
			"content": map[string]any{
				"fragment_size":       200,
				"number_of_fragments": 1,
			},
		},
		"pre_tags":  []string{"<mark>"},
		"post_tags": []string{"</mark>"},
	}
}

// textSearchFields returns the list of fields a multi_match query should
// target on title/content. The base "title^2"/"content" pair matches
// standard-analyzed tokens; one pair per configured language targets the
// language-specific sub-field produced by indexMappingJSON. With
// multi_match type=most_fields this accumulates scores across every
// analyzer that recognizes the query terms.
func (c *Client) textSearchFields() []string {
	fields := []string{"title^2", "content"}
	for _, l := range c.languages {
		fields = append(fields,
			"title."+l.Name+"^2",
			"content."+l.Name,
		)
	}
	return fields
}

// CheckMappingCurrent compares the existing index's title/content
// mappings against the ones this client would create. Returns (true, nil)
// when every configured language has a corresponding sub-field on both
// title and content; (false, nil) when sub-fields are missing or the
// base analyzer has drifted. A (false, nil) result means the user should
// run POST /api/reindex to pick up the new mapping.
//
// Callers should treat a non-nil error as non-fatal — this is a
// diagnostic, not a gate.
func (c *Client) CheckMappingCurrent(ctx context.Context) (bool, error) {
	resp, err := c.os.Indices.Mapping.Get(ctx, &opensearchapi.MappingGetReq{
		Indices: []string{c.index},
	})
	if err != nil {
		return false, fmt.Errorf("search: get mapping: %w", err)
	}

	// The response shape is { "<index>": { "mappings": { "properties": {...} } } }
	idx, ok := resp.Indices[c.index]
	if !ok {
		return false, fmt.Errorf("search: index %q not in mapping response", c.index)
	}
	raw, err := json.Marshal(idx.Mappings)
	if err != nil {
		return false, fmt.Errorf("search: marshal mapping: %w", err)
	}
	var parsed struct {
		Properties map[string]struct {
			Type     string `json:"type"`
			Analyzer string `json:"analyzer"`
			Fields   map[string]struct {
				Type     string `json:"type"`
				Analyzer string `json:"analyzer"`
			} `json:"fields"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false, fmt.Errorf("search: parse mapping: %w", err)
	}

	for _, field := range []string{"title", "content"} {
		f, ok := parsed.Properties[field]
		if !ok {
			return false, nil
		}
		for _, l := range c.languages {
			sub, ok := f.Fields[l.Name]
			if !ok || sub.Analyzer != l.OpenSearchAnalyzer {
				return false, nil
			}
		}
	}
	// New top-level fields introduced for the document-relations work —
	// missing them means the index predates the feature and should be
	// rebuilt via POST /api/reindex.
	for _, field := range []string{"hidden", "conversation_id", "imap_message_id", "relations"} {
		if _, ok := parsed.Properties[field]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// Search performs a BM25 full-text search (no vector search).
func (c *Client) Search(ctx context.Context, req model.SearchRequest) (*model.SearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 20
	}

	matchQuery := map[string]any{
		"multi_match": map[string]any{
			"query":                req.Query,
			"fields":               c.textSearchFields(),
			"type":                 "most_fields",
			"lenient":              true,
			"minimum_should_match": c.minShouldMatch,
		},
	}
	filters := buildFilterClauses(req)

	query := map[string]any{
		"query":            buildSearchQuery(matchQuery, filters),
		"highlight":        highlightConfig(),
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

	return c.hitsToResult(ctx, resp, req)
}

// HybridSearch combines BM25 text search with k-NN vector search using
// OpenSearch's native hybrid query and RRF search pipeline.
func (c *Client) HybridSearch(ctx context.Context, req model.SearchRequest, queryEmbedding []float32) (*model.SearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 20
	}
	fetchSize := req.Limit * 3

	// BM25 sub-query
	bm25Query := map[string]any{
		"multi_match": map[string]any{
			"query":                req.Query,
			"fields":               c.textSearchFields(),
			"type":                 "most_fields",
			"lenient":              true,
			"minimum_should_match": c.minShouldMatch,
		},
	}
	filters := buildFilterClauses(req)
	if len(filters) > 0 {
		bm25Query = map[string]any{
			"bool": map[string]any{
				"must":   bm25Query,
				"filter": filters,
			},
		}
	}

	// k-NN sub-query with filters applied
	knnParams := map[string]any{
		"vector": queryEmbedding,
		"k":      fetchSize,
	}
	if len(filters) > 0 {
		knnParams["filter"] = map[string]any{
			"bool": map[string]any{
				"filter": filters,
			},
		}
	}
	knnQuery := map[string]any{
		"knn": map[string]any{
			"embedding": knnParams,
		},
	}

	query := map[string]any{
		"query": map[string]any{
			"hybrid": map[string]any{
				"queries": []map[string]any{bm25Query, knnQuery},
			},
		},
		"highlight": highlightConfig(),
		"size":      fetchSize,
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("search: marshal hybrid query: %w", err)
	}

	resp, err := c.os.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{c.index},
		Body:    bytes.NewReader(body),
		Params: opensearchapi.SearchParams{
			SearchPipeline: hybridPipeline,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("search: hybrid query: %w", err)
	}

	return c.hitsToResult(ctx, resp, req)
}

// hitsToResult converts OpenSearch hits into a SearchResult with deduplication
// by parent document. Returns the FULL deduped result set without applying
// offset/limit pagination — pagination lives in the handler, after the
// rerank/decay/bonus stages, so the reranker sees the full candidate pool.
func (c *Client) hitsToResult(ctx context.Context, resp *opensearchapi.SearchResp, req model.SearchRequest) (*model.SearchResult, error) {
	chunkData := make(map[string]*rankedChunk)

	for _, hit := range resp.Hits.Hits {
		var chunk model.Chunk
		raw, _ := json.Marshal(hit.Source)
		if err := json.Unmarshal(raw, &chunk); err != nil {
			continue
		}

		headline := ""
		if contentHL, ok := hit.Highlight["content"]; ok && len(contentHL) > 0 {
			headline = contentHL[0]
		}

		score := float64(hit.Score)

		// Keep the highest-scored chunk per parent document
		if existing, ok := chunkData[chunk.ParentID]; ok {
			if score > existing.score {
				existing.headline = headline
				existing.score = score
			}
		} else {
			chunkData[chunk.ParentID] = &rankedChunk{
				parentID: chunk.ParentID,
				doc: model.Document{
					ID:             model.DocumentID(chunk.SourceType, chunk.SourceName, chunk.SourceID),
					SourceType:     chunk.SourceType,
					SourceName:     chunk.SourceName,
					SourceID:       chunk.SourceID,
					Title:          chunk.Title,
					Content:        chunk.Content,
					MimeType:       chunk.MimeType,
					Size:           chunk.Size,
					Metadata:       chunk.Metadata,
					Relations:      chunk.Relations,
					ConversationID: chunk.ConversationID,
					URL:            chunk.URL,
					Visibility:     chunk.Visibility,
					CreatedAt:      chunk.CreatedAt,
					IndexedAt:      chunk.IndexedAt,
				},
				headline: headline,
				score:    score,
			}
		}
	}

	// Collect all deduped results — no score-floor filtering here (would be
	// filtering on RRF rank-fusion scores, which isn't meaningful — see the
	// rankedChunk comment in ranking.go).
	results := make([]*rankedChunk, 0, len(chunkData))
	for _, rc := range chunkData {
		results = append(results, rc)
	}

	// Compute facets from the full deduped result set
	facets := computeFacets(results)

	hits := make([]model.DocumentHit, 0, len(results))
	sourceIDs := make([]string, 0, len(results))
	for _, rc := range results {
		hits = append(hits, model.DocumentHit{
			Document:     rc.doc,
			Rank:         rc.score,
			Headline:     rc.headline,
			RelatedCount: len(rc.doc.Relations), // outgoing, incoming added below
		})
		sourceIDs = append(sourceIDs, rc.doc.SourceID)
	}

	// Annotate each hit with incoming-edge counts so the frontend can hide
	// the "Related" toggle for hits with no inbound refs without fanning
	// out a /related request per result. On error we log and continue with
	// outgoing-only counts — the footer just shows a toggle that reveals
	// "nothing" instead of being correctly hidden; better than failing the
	// whole search.
	incoming, err := c.CountIncomingEdges(ctx, sourceIDs)
	if err == nil {
		for i := range hits {
			hits[i].RelatedCount += incoming[hits[i].SourceID]
		}
	}

	return &model.SearchResult{
		Documents:  hits,
		TotalCount: len(hits),
		Query:      req.Query,
		Facets:     facets,
	}, nil
}

// UpdateOwnershipBySource sets the owner_id and shared fields on every chunk
// belonging to the given source. Used when a connector's ownership flips —
// e.g., flipping shared from false to true must propagate to chunks already
// indexed before the flip.
func (c *Client) UpdateOwnershipBySource(ctx context.Context, sourceType, sourceName, ownerID string, shared bool) error {
	query := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []map[string]any{
					{"term": map[string]any{"source_type": sourceType}},
					{"term": map[string]any{"source_name": sourceName}},
				},
			},
		},
		"script": map[string]any{
			"source": "ctx._source.owner_id = params.owner_id; ctx._source.shared = params.shared;",
			"lang":   "painless",
			"params": map[string]any{
				"owner_id": ownerID,
				"shared":   shared,
			},
		},
	}

	body, err := json.Marshal(query)
	if err != nil {
		return fmt.Errorf("search: marshal update query: %w", err)
	}

	refresh := true
	_, err = c.os.UpdateByQuery(ctx, opensearchapi.UpdateByQueryReq{
		Indices: []string{c.index},
		Body:    bytes.NewReader(body),
		Params: opensearchapi.UpdateByQueryParams{
			Refresh: &refresh,
		},
	})
	if err != nil {
		return fmt.Errorf("search: update ownership by source: %w", err)
	}

	return nil
}

// GetChunkByDocID returns the first chunk (by chunk_index) belonging to the
// document identified by docID. Used by the download endpoint to resolve a
// document UUID into the source triple + ownership/visibility metadata
// needed to dispatch to a connector's BinaryFetcher.
func (c *Client) GetChunkByDocID(ctx context.Context, docID string) (*model.Chunk, error) {
	query := map[string]any{
		"size": 1,
		"sort": []map[string]any{
			{"chunk_index": map[string]any{"order": "asc"}},
		},
		"query": map[string]any{
			"term": map[string]any{
				"doc_id": docID,
			},
		},
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("search: marshal get-by-doc-id query: %w", err)
	}

	resp, err := c.os.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{c.index},
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		return nil, fmt.Errorf("search: get-by-doc-id: %w", err)
	}

	if len(resp.Hits.Hits) == 0 {
		return nil, ErrNotFound
	}

	var chunk model.Chunk
	raw, _ := json.Marshal(resp.Hits.Hits[0].Source)
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return nil, fmt.Errorf("search: unmarshal chunk: %w", err)
	}
	return &chunk, nil
}

// DeleteBySourceIDs removes every chunk for the given (source_type,
// source_name) whose source_id is in the provided list. Used by the
// pipeline's deletion-sync pass to remove docs that disappeared
// upstream. No-op on an empty input slice — callers don't need to
// pre-check before calling.
func (c *Client) DeleteBySourceIDs(ctx context.Context, sourceType, sourceName string, sourceIDs []string) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	query := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []map[string]any{
					{"term": map[string]any{"source_type": sourceType}},
					{"term": map[string]any{"source_name": sourceName}},
					{"terms": map[string]any{"source_id": sourceIDs}},
				},
			},
		},
	}
	body, err := json.Marshal(query)
	if err != nil {
		return fmt.Errorf("search: marshal delete-by-source-ids query: %w", err)
	}
	if _, err = c.os.Document.DeleteByQuery(ctx, opensearchapi.DocumentDeleteByQueryReq{
		Indices: []string{c.index},
		Body:    bytes.NewReader(body),
	}); err != nil {
		return fmt.Errorf("search: delete by source ids: %w", err)
	}
	return nil
}

// listIndexedSourceIDsPageSize is the per-request page size for the
// composite aggregation that enumerates source_ids. 1000 keeps each
// round-trip well under OpenSearch's default search.max_buckets while
// staying small enough that a paged request returns quickly. With this
// page size, a 100K-doc connector enumerates in 100 sequential
// round-trips — fast on a local cluster, harmless on a remote one.
const listIndexedSourceIDsPageSize = 1000

// ListIndexedSourceIDs returns every distinct source_id currently
// indexed for the given (source_type, source_name). Used by the
// pipeline to compute the deletion set against
// FetchResult.CurrentSourceIDs.
//
// Implemented as a composite aggregation paginated by `after_key`,
// which has no upper bound on result size — important for connectors
// like IMAP where a single account routinely holds tens of thousands
// of messages. The simpler `terms` aggregation has a fixed `size`
// cap that would silently truncate (or warn) above some threshold;
// composite trades a slight per-request cost for unbounded
// completeness.
func (c *Client) ListIndexedSourceIDs(ctx context.Context, sourceType, sourceName string) ([]string, error) {
	var out []string
	var afterKey map[string]any
	for {
		composite := map[string]any{
			"size":    listIndexedSourceIDsPageSize,
			"sources": []map[string]any{{"sid": map[string]any{"terms": map[string]any{"field": "source_id"}}}},
		}
		if afterKey != nil {
			composite["after"] = afterKey
		}
		query := map[string]any{
			"size": 0,
			"query": map[string]any{
				"bool": map[string]any{
					"filter": []map[string]any{
						{"term": map[string]any{"source_type": sourceType}},
						{"term": map[string]any{"source_name": sourceName}},
					},
				},
			},
			"aggs": map[string]any{"source_ids": map[string]any{"composite": composite}},
		}
		body, err := json.Marshal(query)
		if err != nil {
			return nil, fmt.Errorf("search: marshal list-source-ids query: %w", err)
		}
		resp, err := c.os.Search(ctx, &opensearchapi.SearchReq{
			Indices: []string{c.index},
			Body:    bytes.NewReader(body),
		})
		if err != nil {
			return nil, fmt.Errorf("search: list source ids: %w", err)
		}
		if len(resp.Aggregations) == 0 {
			return out, nil
		}
		var aggs struct {
			SourceIDs struct {
				AfterKey map[string]any `json:"after_key"`
				Buckets  []struct {
					Key map[string]string `json:"key"`
				} `json:"buckets"`
			} `json:"source_ids"`
		}
		if err := json.Unmarshal(resp.Aggregations, &aggs); err != nil {
			return nil, fmt.Errorf("search: parse source_ids agg: %w", err)
		}
		for _, b := range aggs.SourceIDs.Buckets {
			if sid, ok := b.Key["sid"]; ok {
				out = append(out, sid)
			}
		}
		// after_key is omitted on the final page, signaling we've seen
		// every bucket. Returning an empty buckets array is also a
		// valid termination — guard both.
		if aggs.SourceIDs.AfterKey == nil || len(aggs.SourceIDs.Buckets) == 0 {
			return out, nil
		}
		afterKey = aggs.SourceIDs.AfterKey
	}
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

// RecreateIndex deletes the existing index and creates a new one with the given embedding dimension.
func (c *Client) RecreateIndex(ctx context.Context, embeddingDimension int) error {
	_ = c.DeleteIndex(ctx) // ignore error if index doesn't exist

	c.embeddingDimension = embeddingDimension
	mapping := indexMappingJSON(embeddingDimension, c.languages)
	_, err := c.os.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index: c.index,
		Body:  strings.NewReader(mapping),
	})
	if err != nil {
		return fmt.Errorf("search: recreate index: %w", err)
	}

	if embeddingDimension > 0 {
		if err := c.ensureHybridPipeline(ctx); err != nil {
			return err
		}
	}

	c.log.Info("recreated search index", zap.String("index", c.index), zap.Int("embedding_dim", embeddingDimension))
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
