package embedding

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/muty/nexus/internal/providerhttp"
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
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	InputType string   `json:"input_type,omitempty"`
}

type voyageEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func (v *Voyage) Embed(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	// Voyage uses "document" / "query" — same strings as our InputType constants,
	// so no translation needed. Empty string is also accepted by Voyage (treated
	// as un-typed) but our callers should always pass one of the constants.
	reqBody := voyageEmbedRequest{
		Model:     v.model,
		Input:     texts,
		InputType: inputType,
	}

	var result voyageEmbedResponse
	if err := providerhttp.PostJSON(ctx, v.client, v.baseURL+"/v1/embeddings", v.apiKey, reqBody, &result,
		func(resp *http.Response) error { return errorFromResponse(resp, "voyage") }); err != nil {
		return nil, fmt.Errorf("voyage: embed: %w", err)
	}

	// Place each embedding at its response-reported index rather than trusting
	// positional order — the API contract returns an `index` field precisely
	// because order isn't guaranteed, and a mis-ordered vector would silently
	// attach to the wrong chunk. Fall back to positional when index is absent
	// or out of range.
	embeddings := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		pos := d.Index
		if pos < 0 || pos >= len(embeddings) {
			pos = i
		}
		embeddings[pos] = d.Embedding
	}
	return embeddings, nil
}

func (v *Voyage) Dimension() int {
	switch v.model {
	case "voyage-4-large":
		return 1024
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
