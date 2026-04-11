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

// Cohere implements Embedder using the Cohere API.
type Cohere struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
	log     *zap.Logger
}

// NewCohere creates a Cohere embedding client.
func NewCohere(apiKey, model string, log *zap.Logger) *Cohere {
	return &Cohere{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.cohere.com",
		client:  &http.Client{Timeout: 30 * time.Second},
		log:     log,
	}
}

type cohereEmbedRequest struct {
	Model          string   `json:"model"`
	Texts          []string `json:"texts"`
	InputType      string   `json:"input_type"`
	EmbeddingTypes []string `json:"embedding_types"`
}

type cohereEmbedResponse struct {
	Embeddings struct {
		Float [][]float32 `json:"float"`
	} `json:"embeddings"`
}

// Embed implements Embedder. The inputType parameter is mapped to Cohere's
// own input_type constants: "document"→"search_document", "query"→"search_query".
// Anything else falls back to "search_document" for backward compatibility.
func (c *Cohere) Embed(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	cohereInputType := "search_document"
	switch inputType {
	case InputTypeQuery:
		cohereInputType = "search_query"
	case InputTypeDocument:
		cohereInputType = "search_document"
	}
	body, err := json.Marshal(cohereEmbedRequest{
		Model:          c.model,
		Texts:          texts,
		InputType:      cohereInputType,
		EmbeddingTypes: []string{"float"},
	})
	if err != nil {
		return nil, fmt.Errorf("cohere: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v2/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cohere: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cohere: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return nil, errorFromResponse(resp, "cohere")
	}

	var result cohereEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("cohere: decode response: %w", err)
	}

	return result.Embeddings.Float, nil
}

func (c *Cohere) Dimension() int {
	switch c.model {
	case "embed-v4.0":
		return 1024
	case "embed-english-v3.0":
		return 1024
	case "embed-english-light-v3.0":
		return 384
	case "embed-multilingual-v3.0":
		return 1024
	default:
		return 1024
	}
}
