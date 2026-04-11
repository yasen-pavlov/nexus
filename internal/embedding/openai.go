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

// OpenAI implements Embedder using the OpenAI API.
type OpenAI struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
	log     *zap.Logger
}

// NewOpenAI creates an OpenAI embedding client.
func NewOpenAI(apiKey, model string, log *zap.Logger) *OpenAI {
	return &OpenAI{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.openai.com",
		client:  &http.Client{Timeout: 30 * time.Second},
		log:     log,
	}
}

type openAIEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed implements Embedder. The inputType parameter is ignored — OpenAI's
// embedding API doesn't have a query/document distinction.
func (o *OpenAI) Embed(ctx context.Context, texts []string, _ string) ([][]float32, error) {
	body, err := json.Marshal(openAIEmbedRequest{Model: o.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return nil, errorFromResponse(resp, "openai")
	}

	var result openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}

	embeddings := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		embeddings[i] = d.Embedding
	}
	return embeddings, nil
}

func (o *OpenAI) Dimension() int {
	switch o.model {
	case "text-embedding-3-small":
		return 1536
	case "text-embedding-3-large":
		return 3072
	case "text-embedding-ada-002":
		return 1536
	default:
		return 1536
	}
}
