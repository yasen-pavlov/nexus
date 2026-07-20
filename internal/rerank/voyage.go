package rerank

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/muty/nexus/internal/providerhttp"
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

	var result voyageRerankResponse
	if err := providerhttp.PostJSON(ctx, v.client, v.baseURL+"/v1/rerank", v.apiKey, reqBody, &result,
		func(resp *http.Response) error { return errorFromResponse(resp, "voyage") }); err != nil {
		return nil, fmt.Errorf("voyage: rerank: %w", err)
	}

	return result.Data, nil
}
