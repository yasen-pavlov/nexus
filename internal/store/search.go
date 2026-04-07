package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/muty/nexus/internal/model"
)

func (s *Store) Search(ctx context.Context, req model.SearchRequest) (*model.SearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = 20
	}

	// Count total matches
	countQuery := `
		SELECT count(*)
		FROM documents, plainto_tsquery('english', $1) query
		WHERE content_ts @@ query
	`
	var totalCount int
	if err := s.pool.QueryRow(ctx, countQuery, req.Query).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("store: search count: %w", err)
	}

	// Fetch ranked results with highlighted snippets
	searchQuery := `
		SELECT id, source_type, source_name, source_id, title, content, metadata, url, visibility,
			created_at, indexed_at,
			ts_rank_cd(content_ts, query) AS rank,
			ts_headline('english', content, query, 'MaxWords=40, MinWords=20, StartSel=<mark>, StopSel=</mark>') AS headline
		FROM documents, plainto_tsquery('english', $1) query
		WHERE content_ts @@ query
		ORDER BY rank DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.pool.Query(ctx, searchQuery, req.Query, req.Limit, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("store: search query: %w", err)
	}
	defer rows.Close()

	var hits []model.DocumentHit
	for rows.Next() {
		var hit model.DocumentHit
		var metadata []byte
		err := rows.Scan(
			&hit.ID, &hit.SourceType, &hit.SourceName, &hit.SourceID,
			&hit.Title, &hit.Content, &metadata, &hit.URL,
			&hit.Visibility, &hit.CreatedAt, &hit.IndexedAt,
			&hit.Rank, &hit.Headline,
		)
		if err != nil {
			return nil, fmt.Errorf("store: scan search result: %w", err)
		}
		if err := json.Unmarshal(metadata, &hit.Metadata); err != nil {
			return nil, fmt.Errorf("store: unmarshal metadata: %w", err)
		}
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: search rows: %w", err)
	}

	return &model.SearchResult{
		Documents:  hits,
		TotalCount: totalCount,
		Query:      req.Query,
	}, nil
}
