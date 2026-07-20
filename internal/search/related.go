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

// targetSourceIDField is the keyword field on nested relation docs that
// holds the source-level pointer (IMAP Message-ID, Telegram
// chatID:msgID:msg, etc.).
const targetSourceIDField = "relations.target_source_id"

// incomingEdgeDistinctParents holds the cardinality aggregation that
// counts distinct parent documents referencing a target via `parent_id`.
type incomingEdgeDistinctParents struct {
	Value int `json:"value"`
}

// incomingEdgeVisible is the optional ownership-filtered sub-aggregation
// wrapping distinct_parents. Present only when CountIncomingEdges is given
// an ownerID; otherwise distinct_parents sits directly under parents.
type incomingEdgeVisible struct {
	DistinctParents incomingEdgeDistinctParents `json:"distinct_parents"`
}

// incomingEdgeParents is the `reverse_nested` sub-aggregation that climbs
// from a relation document back to its parent and counts distinct parents.
// DistinctParents is populated in the unfiltered case; Visible is populated
// when an ownership filter is applied. parentCount() picks whichever holds.
type incomingEdgeParents struct {
	DistinctParents incomingEdgeDistinctParents `json:"distinct_parents"`
	Visible         *incomingEdgeVisible        `json:"visible"`
}

// parentCount returns the distinct-parent count, preferring the
// ownership-filtered `visible` sub-agg when present.
func (p incomingEdgeParents) parentCount() int {
	if p.Visible != nil {
		return p.Visible.DistinctParents.Value
	}
	return p.DistinctParents.Value
}

// incomingEdgeBucket is one bucket in the terms aggregation keyed by
// target_source_id, with the parents sub-aggregation attached.
type incomingEdgeBucket struct {
	Key     string              `json:"key"`
	Parents incomingEdgeParents `json:"parents"`
}

// incomingEdgeByTarget wraps the terms aggregation buckets.
type incomingEdgeByTarget struct {
	Buckets []incomingEdgeBucket `json:"buckets"`
}

// incomingEdgeRelations wraps the `by_target` aggregation under the outer
// `relations` nested aggregation.
type incomingEdgeRelations struct {
	ByTarget incomingEdgeByTarget `json:"by_target"`
}

// incomingEdgeAggs is the top-level JSON shape parsed out of
// resp.Aggregations for CountIncomingEdges.
type incomingEdgeAggs struct {
	Relations incomingEdgeRelations `json:"relations"`
}

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
		shoulds = append(shoulds, map[string]any{"terms": map[string]any{targetSourceIDField: targetSourceIDs}})
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
//
// ownerID is variadic so the existing call site can opt in without a
// rippling signature change: when a non-empty owner UUID is supplied, the
// parent (root) documents counted are constrained to those the requester
// can see (owner_id == me OR shared = true), mirroring
// buildFilterClauses. Without it, the count spans the whole index — which
// can inflate related_count with documents the user can't actually view.
// Pass the requesting user's UUID (e.g. req.OwnerID) to scope it.
func (c *Client) CountIncomingEdges(ctx context.Context, targetSourceIDs []string, ownerID ...string) (map[string]int, error) {
	if len(targetSourceIDs) == 0 {
		return map[string]int{}, nil
	}

	// Ownership filter applied to the ROOT (parent) document under
	// reverse_nested, so only parents the requester may see are counted.
	// owner_id/shared live on the root doc, not the nested relation, which
	// is exactly why this filter belongs in the reverse_nested sub-agg
	// rather than the outer nested query.
	var ownerFilter map[string]any
	if len(ownerID) > 0 && ownerID[0] != "" {
		ownerFilter = map[string]any{
			"bool": map[string]any{
				"should": []map[string]any{
					{"term": map[string]any{"owner_id": ownerID[0]}},
					{"term": map[string]any{"shared": true}},
				},
				"minimum_should_match": 1,
			},
		}
	}

	// reverse_nested climbs from the relation sub-doc back to its root
	// parent. When an ownership filter is present we wrap the
	// distinct_parents cardinality in a `filtered` aggregation so only
	// visible parents contribute to the count.
	var parentsAggs map[string]any
	if ownerFilter != nil {
		parentsAggs = map[string]any{
			"visible": map[string]any{
				"filter": ownerFilter,
				"aggs": map[string]any{
					"distinct_parents": map[string]any{
						"cardinality": map[string]any{"field": "parent_id"},
					},
				},
			},
		}
	} else {
		parentsAggs = map[string]any{
			"distinct_parents": map[string]any{
				"cardinality": map[string]any{"field": "parent_id"},
			},
		}
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
						targetSourceIDField: targetSourceIDs,
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
							"field":   targetSourceIDField,
							"include": targetSourceIDs,
							"size":    len(targetSourceIDs),
						},
						"aggs": map[string]any{
							"parents": map[string]any{
								"reverse_nested": map[string]any{},
								"aggs":           parentsAggs,
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
	var parsed incomingEdgeAggs
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return counts, nil
	}
	for _, b := range parsed.Relations.ByTarget.Buckets {
		counts[b.Key] = b.Parents.parentCount()
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
		// Prefer the first_chunk inner_hit (sorted chunk_index asc, so index 0).
		// The collapse representative in hit.Source is chosen by the main sort,
		// which ties arbitrarily across a doc's chunks (they share created_at /
		// _score) — so it can be an arbitrary mid-document chunk, making
		// Document.Content start mid-document. The inner_hits are already
		// round-tripped from OpenSearch on every call; use them. Fall back to
		// hit.Source for any future caller that omits inner_hits.
		src := hit.Source
		if inner, ok := hit.InnerHits["first_chunk"]; ok && len(inner.Hits.Hits) > 0 {
			src = inner.Hits.Hits[0].Source
		}
		var chunk model.Chunk
		if err := json.Unmarshal(src, &chunk); err != nil {
			continue
		}
		out = append(out, chunk)
	}
	return out, nil
}
