package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
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

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("cohere: marshal rerank request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v2/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cohere: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cohere: rerank request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return nil, &RerankError{StatusCode: resp.StatusCode, Provider: "cohere"}
	}

	var result cohereRerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("cohere: decode rerank response: %w", err)
	}

	return result.Results, nil
}
