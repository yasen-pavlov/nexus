package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/muty/nexus/internal/model"
)

// ConversationMessagesOptions parameterizes GetConversationMessages.
// Before and After bound the created_at range; Limit caps the returned
// slice (callers enforce their own upper bound).
//
// Pagination direction is derived from which cursor is set:
//
//   - After set: "forward" — return the N oldest messages strictly after
//     the cursor, already ASC.
//   - Before set OR neither set: "backward" — return the N newest
//     messages strictly before the cursor (or the tail when neither is
//     set), then reverse to ASC. This is the chat-UI-native behavior
//     where the initial load shows the latest page.
type ConversationMessagesOptions struct {
	SourceType   string
	Conversation string
	Before       time.Time // exclusive upper bound (optional, zero = unbounded)
	After        time.Time // exclusive lower bound (optional, zero = unbounded)
	Limit        int
}

// GetConversationMessages returns Hidden=true per-message chunks for a
// (source_type, conversation_id) pair in chronological (ASC) order.
// Always sorts the returned slice ascending so callers can render
// without caring about the cursor direction.
//
// Hidden=true is enforced so this can't accidentally leak the parent
// window docs (they'd otherwise match the source_type + conversation_id
// filter too).
func (c *Client) GetConversationMessages(ctx context.Context, opts ConversationMessagesOptions) ([]model.Chunk, error) {
	filters := []map[string]any{
		{"term": map[string]any{"source_type": opts.SourceType}},
		{"term": map[string]any{"conversation_id": opts.Conversation}},
		{"term": map[string]any{"hidden": true}},
	}
	if !opts.Before.IsZero() || !opts.After.IsZero() {
		rng := map[string]any{}
		if !opts.Before.IsZero() {
			rng["lt"] = opts.Before.UTC().Format(time.RFC3339Nano)
		}
		if !opts.After.IsZero() {
			rng["gt"] = opts.After.UTC().Format(time.RFC3339Nano)
		}
		filters = append(filters, map[string]any{"range": map[string]any{"created_at": rng}})
	}
	// Forward paging (after cursor) → ASC. Backward paging or "give me
	// the tail" (neither cursor or only `before`) → DESC + reverse.
	direction := "desc"
	if !opts.After.IsZero() {
		direction = "asc"
	}
	query := map[string]any{
		"size":  opts.Limit,
		"sort":  []map[string]any{{"created_at": map[string]any{"order": direction}}},
		"query": map[string]any{"bool": map[string]any{"filter": filters}},
		"collapse": map[string]any{
			"field": "doc_id",
			"inner_hits": map[string]any{
				"name": "first_chunk",
				"size": 1,
				"sort": []map[string]any{{"chunk_index": map[string]any{"order": "asc"}}},
			},
		},
	}
	chunks, err := c.runChunkQuery(ctx, query, "conversation-messages")
	if err != nil {
		return nil, err
	}
	if direction == "desc" {
		// Reverse in place so the returned slice is always chronological.
		for i, j := 0, len(chunks)-1; i < j; i, j = i+1, j-1 {
			chunks[i], chunks[j] = chunks[j], chunks[i]
		}
	}
	return chunks, nil
}

// FindChunksByTerm returns one chunk per matching document, where the
// given keyword field equals value. Used to resolve source-level
// pointers (IMAP `imap_message_id`, Telegram `chatID:msgID:msg`) into
// full documents for the `/related` endpoint.
//
// The field must be indexed as a keyword in the mapping — passing a
// non-keyword field yields empty results without erroring. Callers
// check for empty results, not errors, to detect missing targets.
func (c *Client) FindChunksByTerm(ctx context.Context, field, value string) ([]model.Chunk, error) {
	if field == "" || value == "" {
		return nil, nil
	}
	query := map[string]any{
		"size":  100,
		"query": map[string]any{"term": map[string]any{field: value}},
		"collapse": map[string]any{
			"field": "doc_id",
			"inner_hits": map[string]any{
				"name": "first_chunk",
				"size": 1,
				"sort": []map[string]any{{"chunk_index": map[string]any{"order": "asc"}}},
			},
		},
	}
	return c.runChunkQuery(ctx, query, "find-by-term")
}

// FindChunksReferencing returns the chunks whose `relations` nested
// array contains an entry pointing at any of the given target IDs or
// source IDs. Used for the reverse-edge half of the `/related`
// endpoint ("what references this doc?").
//
// The nested bool/should lets a single query cover both
// target_id (UUID) and target_source_id (string) matches without two
// round-trips. Matching is dedup'd to one chunk per document.
func (c *Client) FindChunksReferencing(ctx context.Context, targetIDs, targetSourceIDs []string) ([]model.Chunk, error) {
	if len(targetIDs) == 0 && len(targetSourceIDs) == 0 {
		return nil, nil
	}
	var shoulds []map[string]any
	if len(targetIDs) > 0 {
		shoulds = append(shoulds, map[string]any{"terms": map[string]any{"relations.target_id": targetIDs}})
	}
	if len(targetSourceIDs) > 0 {
		shoulds = append(shoulds, map[string]any{"terms": map[string]any{"relations.target_source_id": targetSourceIDs}})
	}
	query := map[string]any{
		"size": 100,
		"query": map[string]any{
			"nested": map[string]any{
				"path": "relations",
				"query": map[string]any{
					"bool": map[string]any{
						"should":               shoulds,
						"minimum_should_match": 1,
					},
				},
			},
		},
		"collapse": map[string]any{
			"field": "doc_id",
			"inner_hits": map[string]any{
				"name": "first_chunk",
				"size": 1,
				"sort": []map[string]any{{"chunk_index": map[string]any{"order": "asc"}}},
			},
		},
	}
	return c.runChunkQuery(ctx, query, "find-referencing")
}

// CountIncomingEdges returns a map of source_id → number of distinct parent
// documents that have at least one relation pointing at it. Used by the
// search path so each hit can carry a `related_count` upfront, letting the
// frontend hide the "Related" toggle for docs with no inbound references
// without fanning out a /related request per result.
//
// Incoming is counted per unique parent_id to avoid double-counting when
// multiple chunks of the same doc point at the same target.
func (c *Client) CountIncomingEdges(ctx context.Context, targetSourceIDs []string) (map[string]int, error) {
	if len(targetSourceIDs) == 0 {
		return map[string]int{}, nil
	}

	// Aggregation tree:
	//   nested(relations) → terms(target_source_id) → reverse_nested → cardinality(parent_id)
	// The outer `nested` aggregation scopes bucket keys to the relation
	// sub-documents. `reverse_nested` climbs back to the root doc so we can
	// count distinct parents, avoiding double-counting when multiple chunks
	// of the same doc point at the same target.
	query := map[string]any{
		"size": 0,
		"query": map[string]any{
			"nested": map[string]any{
				"path": "relations",
				"query": map[string]any{
					"terms": map[string]any{
						"relations.target_source_id": targetSourceIDs,
					},
				},
			},
		},
		"aggs": map[string]any{
			"relations": map[string]any{
				"nested": map[string]any{"path": "relations"},
				"aggs": map[string]any{
					"by_target": map[string]any{
						"terms": map[string]any{
							"field":   "relations.target_source_id",
							"include": targetSourceIDs,
							"size":    len(targetSourceIDs),
						},
						"aggs": map[string]any{
							"parents": map[string]any{
								"reverse_nested": map[string]any{},
								"aggs": map[string]any{
									"distinct_parents": map[string]any{
										"cardinality": map[string]any{"field": "parent_id"},
									},
								},
							},
						},
					},
				},
			},
		},
		"_source": false,
	}

	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("search: marshal count-incoming: %w", err)
	}
	resp, err := c.os.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{c.index},
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		return nil, fmt.Errorf("search: count-incoming: %w", err)
	}

	// Walk the aggregation tree: relations → by_target → [buckets] → parents → distinct_parents.
	counts := make(map[string]int, len(targetSourceIDs))
	raw, err := json.Marshal(resp.Aggregations)
	if err != nil {
		return counts, nil
	}
	var parsed struct {
		Relations struct {
			ByTarget struct {
				Buckets []struct {
					Key     string `json:"key"`
					Parents struct {
						DistinctParents struct {
							Value int `json:"value"`
						} `json:"distinct_parents"`
					} `json:"parents"`
				} `json:"buckets"`
			} `json:"by_target"`
		} `json:"relations"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return counts, nil
	}
	for _, b := range parsed.Relations.ByTarget.Buckets {
		counts[b.Key] = b.Parents.DistinctParents.Value
	}
	return counts, nil
}

// runChunkQuery marshals + runs a chunks query and unmarshals the hits
// into model.Chunk. Centralized so the three `/related` helpers don't
// duplicate boilerplate.
func (c *Client) runChunkQuery(ctx context.Context, query map[string]any, opName string) ([]model.Chunk, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("search: marshal %s: %w", opName, err)
	}
	resp, err := c.os.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{c.index},
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		return nil, fmt.Errorf("search: %s: %w", opName, err)
	}
	out := make([]model.Chunk, 0, len(resp.Hits.Hits))
	for _, hit := range resp.Hits.Hits {
		var chunk model.Chunk
		raw, _ := json.Marshal(hit.Source)
		if err := json.Unmarshal(raw, &chunk); err != nil {
			continue
		}
		out = append(out, chunk)
	}
	return out, nil
}
