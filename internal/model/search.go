package model

import "time"

// SearchRequest contains search parameters including filters.
type SearchRequest struct {
	Query       string   `json:"query"`
	Limit       int      `json:"limit"`
	Offset      int      `json:"offset"`
	Sources     []string `json:"sources,omitempty"`      // filter by source_type
	SourceNames []string `json:"source_names,omitempty"` // filter by source_name
	DateFrom    string   `json:"date_from,omitempty"`    // ISO date (e.g., 2025-01-01)
	DateTo      string   `json:"date_to,omitempty"`      // ISO date
	OwnerID     string   `json:"-"`                      // set from auth context, not from request
}

// SearchResult contains search results with optional facets.
type SearchResult struct {
	Documents  []DocumentHit      `json:"documents"`
	TotalCount int                `json:"total_count"`
	Query      string             `json:"query"`
	Facets     map[string][]Facet `json:"facets,omitempty"`
}

// Facet represents a single aggregation bucket.
type Facet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// DocumentHit is a search result with ranking info.
type DocumentHit struct {
	Document
	Rank         float64       `json:"rank"`
	Headline     string        `json:"headline"`
	ScoreDetails *ScoreDetails `json:"score_details,omitempty"`

	// RelatedCount is the total number of relations — outgoing (on this doc's
	// `relations`) plus incoming (other docs referencing this one). The
	// frontend uses this to hide the "Related" toggle for docs with nothing
	// to expand, without triggering an extra /related call per hit.
	RelatedCount int `json:"related_count,omitempty"`

	// Match* fields pinpoint the specific message inside a
	// conversation-window document that matched the query. Populated
	// by the search pipeline when a BM25 highlight can be mapped back
	// to a message_lines entry (telegram window hits today). Empty
	// for semantic-only hits and for non-window documents — the
	// frontend renders a bookended window preview in those cases
	// instead of a pinpoint message card.
	MatchSourceID   string     `json:"match_source_id,omitempty"`
	MatchMessageID  int64      `json:"match_message_id,omitempty"`
	MatchCreatedAt  *time.Time `json:"match_created_at,omitempty"`
	MatchSenderID   int64      `json:"match_sender_id,omitempty"`
	MatchSenderName string     `json:"match_sender_name,omitempty"`
	MatchAvatarKey  string     `json:"match_avatar_key,omitempty"`
}

// ScoreDetails provides a breakdown of how the final rank was computed.
// Only populated when ?explain=true is passed.
type ScoreDetails struct {
	Retrieval     float64 `json:"retrieval"`
	Reranker      float64 `json:"reranker,omitempty"`
	RecencyFactor float64 `json:"recency_factor"`
	MetadataBonus float64 `json:"metadata_bonus"`
	Final         float64 `json:"final"`
}
