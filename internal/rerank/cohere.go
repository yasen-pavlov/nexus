package rerank

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/muty/nexus/internal/providerhttp"
)

// Cohere implements Reranker using the Cohere rerank API.
type Cohere struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewCohere creates a Cohere reranker.
func NewCohere(apiKey, model string) *Cohere {
	return &Cohere{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.cohere.com",
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

type cohereRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type cohereRerankResponse struct {
	Results []Result `json:"results"`
}

func (c *Cohere) Rerank(ctx context.Context, query string, documents []string) ([]Result, error) {
	reqBody := cohereRerankRequest{
		Model:     c.model,
		Query:     query,
		Documents: documents,
	}

	var result cohereRerankResponse
	if err := providerhttp.PostJSON(ctx, c.client, c.baseURL+"/v2/rerank", c.apiKey, reqBody, &result,
		func(resp *http.Response) error { return errorFromResponse(resp, "cohere") }); err != nil {
		return nil, fmt.Errorf("cohere: rerank: %w", err)
	}

	return result.Results, nil
}
