package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Voyage implements Embedder using the Voyage AI API.
type Voyage struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
	log     *zap.Logger
}

// NewVoyage creates a Voyage embedding client.
func NewVoyage(apiKey, model string, log *zap.Logger) *Voyage {
	return &Voyage{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.voyageai.com",
		client:  &http.Client{Timeout: 30 * time.Second},
		log:     log,
	}
}

type voyageEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type voyageEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (v *Voyage) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(voyageEmbedRequest{Model: v.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("voyage: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("voyage: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+v.apiKey)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return nil, errorFromResponse(resp, "voyage")
	}

	var result voyageEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("voyage: decode response: %w", err)
	}

	embeddings := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		embeddings[i] = d.Embedding
	}
	return embeddings, nil
}

func (v *Voyage) Dimension() int {
	switch v.model {
	case "voyage-3-large":
		return 1024
	case "voyage-3":
		return 1024
	case "voyage-3-lite":
		return 512
	default:
		return 1024
	}
}
