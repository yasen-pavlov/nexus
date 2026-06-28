package cliclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/muty/nexus/internal/model"
)

// SearchParams are the query + filter inputs for Search, mirroring the
// /api/search query parameters.
type SearchParams struct {
	Query       string
	Limit       int
	Offset      int
	Sources     []string // source types (filesystem, imap, telegram, paperless, ical)
	SourceNames []string // specific connector names
	DateFrom    string   // YYYY-MM-DD, inclusive
	DateTo      string   // YYYY-MM-DD, inclusive
	Explain     bool     // request the per-hit score breakdown
}

// query renders the params as the /api/search query string. Empty fields are
// omitted so the server applies its own defaults.
func (p SearchParams) query() url.Values {
	q := url.Values{}
	q.Set("q", p.Query)
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Offset > 0 {
		q.Set("offset", strconv.Itoa(p.Offset))
	}
	if len(p.Sources) > 0 {
		q.Set("sources", strings.Join(p.Sources, ","))
	}
	if len(p.SourceNames) > 0 {
		q.Set("source_names", strings.Join(p.SourceNames, ","))
	}
	if p.DateFrom != "" {
		q.Set("date_from", p.DateFrom)
	}
	if p.DateTo != "" {
		q.Set("date_to", p.DateTo)
	}
	if p.Explain {
		q.Set("score_details", "true")
	}
	return q
}

// Search runs a hybrid search (BM25 + vector when embeddings are enabled) and
// returns the ranked documents. Results are scoped server-side to the token's
// owning user (plus shared connectors); the client never sends an owner id.
func (c *Client) Search(ctx context.Context, p SearchParams) (*model.SearchResult, error) {
	var res model.SearchResult
	if err := c.do(ctx, http.MethodGet, "/api/search", p.query(), nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
