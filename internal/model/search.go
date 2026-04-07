package model

type SearchRequest struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type SearchResult struct {
	Documents  []DocumentHit `json:"documents"`
	TotalCount int           `json:"total_count"`
	Query      string        `json:"query"`
}

type DocumentHit struct {
	Document
	Rank     float64 `json:"rank"`
	Headline string  `json:"headline"`
}
