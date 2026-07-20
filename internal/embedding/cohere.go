package embedding

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/muty/nexus/internal/providerhttp"
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
	Model           string   `json:"model"`
	Texts           []string `json:"texts"`
	InputType       string   `json:"input_type"`
	EmbeddingTypes  []string `json:"embedding_types"`
	OutputDimension int      `json:"output_dimension,omitempty"`
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
	// embed-v4.0 defaults to 1536-dim output when output_dimension is omitted,
	// but Dimension() (and thus the knn index mapping) reports 1024 — the
	// mismatch makes the pipeline drop every vector. Pin the request to 1024 so
	// the wire dimension matches the index. v3 models reject output_dimension,
	// so only send it for v4 (omitempty drops the 0 for everything else).
	outputDim := 0
	if c.model == "embed-v4.0" {
		outputDim = 1024
	}
	reqBody := cohereEmbedRequest{
		Model:           c.model,
		Texts:           texts,
		InputType:       cohereInputType,
		EmbeddingTypes:  []string{"float"},
		OutputDimension: outputDim,
	}

	var result cohereEmbedResponse
	if err := providerhttp.PostJSON(ctx, c.client, c.baseURL+"/v2/embed", c.apiKey, reqBody, &result,
		func(resp *http.Response) error { return errorFromResponse(resp, "cohere") }); err != nil {
		return nil, fmt.Errorf("cohere: embed: %w", err)
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
