package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Voyage implements Reranker using the Voyage AI rerank API.
type Voyage struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewVoyage creates a Voyage reranker.
func NewVoyage(apiKey, model string) *Voyage {
	return &Voyage{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.voyageai.com",
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

type voyageRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type voyageRerankResponse struct {
	Data []Result `json:"data"`
}

func (v *Voyage) Rerank(ctx context.Context, query string, documents []string) ([]Result, error) {
	reqBody := voyageRerankRequest{
		Model:     v.model,
		Query:     query,
		Documents: documents,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("voyage: marshal rerank request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.baseURL+"/v1/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("voyage: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+v.apiKey)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage: rerank request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return nil, &RerankError{StatusCode: resp.StatusCode, Provider: "voyage"}
	}

	var result voyageRerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("voyage: decode rerank response: %w", err)
	}

	return result.Data, nil
}
