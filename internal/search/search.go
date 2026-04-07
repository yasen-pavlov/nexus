// Package search provides the OpenSearch client for document indexing and search.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	os    *opensearchapi.Client
	log   *zap.Logger
	index string
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

	// Ping to verify connection
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

// EnsureIndex creates the search index if it does not exist.
func (c *Client) EnsureIndex(ctx context.Context) error {
	_, err := c.os.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{
		Indices: []string{c.index},
	})
	if err == nil {
		return nil // index already exists
	}

	_, err = c.os.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index: c.index,
		Body:  strings.NewReader(indexMapping),
	})
	if err != nil {
		return fmt.Errorf("search: create index: %w", err)
	}

	c.log.Info("created search index", zap.String("index", c.index))
	return nil
}

// docID returns the composite document ID for OpenSearch.
func docID(doc *model.Document) string {
	return doc.SourceType + ":" + doc.SourceName + ":" + doc.SourceID
}

// IndexDocument indexes a document in OpenSearch.
func (c *Client) IndexDocument(ctx context.Context, doc *model.Document) error {
	if doc.ID == uuid.Nil {
		doc.ID = uuid.New()
	}
	doc.IndexedAt = time.Now()

	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("search: marshal document: %w", err)
	}

	_, err = c.os.Index(ctx, opensearchapi.IndexReq{
		Index:      c.index,
		DocumentID: docID(doc),
		Body:       bytes.NewReader(body),
	})
	if err != nil {
		return fmt.Errorf("search: index document: %w", err)
	}

	return nil
}

// Search performs a full-text search across documents.
func (c *Client) Search(ctx context.Context, req model.SearchRequest) (*model.SearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 20
	}

	query := map[string]any{
		"query": map[string]any{
			"multi_match": map[string]any{
				"query":  req.Query,
				"fields": []string{"title^2", "content"},
			},
		},
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
		"from":             req.Offset,
		"size":             req.Limit,
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

	var hits []model.DocumentHit
	for _, hit := range resp.Hits.Hits {
		var doc model.Document
		raw, err := json.Marshal(hit.Source)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			continue
		}

		headline := ""
		if contentHL, ok := hit.Highlight["content"]; ok && len(contentHL) > 0 {
			headline = contentHL[0]
		}

		hits = append(hits, model.DocumentHit{
			Document: doc,
			Rank:     float64(hit.Score),
			Headline: headline,
		})
	}

	totalCount := resp.Hits.Total.Value

	return &model.SearchResult{
		Documents:  hits,
		TotalCount: totalCount,
		Query:      req.Query,
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

// GetDocument retrieves a single document by its composite ID.
func (c *Client) GetDocument(ctx context.Context, sourceType, sourceName, sourceID string) (*model.Document, error) {
	compositeID := sourceType + ":" + sourceName + ":" + sourceID

	resp, err := c.os.Document.Get(ctx, opensearchapi.DocumentGetReq{
		Index:      c.index,
		DocumentID: compositeID,
	})
	if err != nil {
		return nil, fmt.Errorf("search: get document: %w", err)
	}

	if !resp.Found {
		return nil, ErrNotFound
	}

	raw, err := json.Marshal(resp.Source)
	if err != nil {
		return nil, fmt.Errorf("search: marshal source: %w", err)
	}

	var doc model.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("search: unmarshal document: %w", err)
	}

	return &doc, nil
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
