package embedding

import (
	"context"
	"encoding/json"
	"errors"
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
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

func (v *Voyage) Embed(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	// Voyage's Qwen2 byte-level BPE tokenizer starts from UTF-8 bytes;
	// merges can only reduce their count. Reserve 64 additional tokens per
	// input: even byte-tokenizing the documented query/document instruction
	// (under 60 ASCII bytes) leaves room for special tokens.
	// Unlike words*constant, this bounds dense code, numbers and non-Latin text.
	// Keep this conservative for all models; an authoritative provider limit
	// error below also handles tokenizer/instruction changes without dropping
	// any input. Voyage documents 120K aggregate tokens for voyage-4-large.
	const tokenBudget = 120000
	const inputOverhead = 64
	const maxInputs = 1000
	embeddings := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); {
		end, tokens := start, 0
		for end < len(texts) && end-start < maxInputs {
			bound := len(texts[end]) + inputOverhead
			if end > start && bound > tokenBudget-tokens {
				break
			}
			// A conservative bound cannot prove a single input is too large.
			// Submit it alone and let the provider decide; never truncate it
			// locally or recurse forever on an unsplittable failure.
			tokens += bound
			end++
		}
		batch, err := v.embedBatch(ctx, texts[start:end], inputType)
		if err != nil {
			return nil, err
		}
		embeddings = append(embeddings, batch...)
		start = end
	}
	return embeddings, nil
}

// embedBatch splits only the provider's typed aggregate-token rejection.
// Other errors retain their normal retry semantics; an oversized singleton
// fails explicitly. Concatenating left then right preserves every input index.
func (v *Voyage) embedBatch(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
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
		var apiErr *EmbedError
		var detail struct {
			Code string `json:"error_code"`
		}
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest &&
			json.Unmarshal([]byte(apiErr.Body), &detail) == nil &&
			detail.Code == "TOO_MANY_TOKENS_IN_BATCH" && len(texts) > 1 {
			mid := len(texts) / 2
			left, err := v.embedBatch(ctx, texts[:mid], inputType)
			if err != nil {
				return nil, err
			}
			right, err := v.embedBatch(ctx, texts[mid:], inputType)
			if err != nil {
				return nil, err
			}
			return append(left, right...), nil
		}
		return nil, fmt.Errorf("voyage: embed (%d inputs): %w", len(texts), err)
	}

	// Reject malformed responses instead of silently losing or misassigning
	// vectors. Indices are batch-local and may arrive out of order.
	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("voyage: embedding count mismatch: got %d, want %d", len(result.Data), len(texts))
	}
	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < 0 || d.Index >= len(texts) || embeddings[d.Index] != nil || len(d.Embedding) == 0 {
			return nil, fmt.Errorf("voyage: invalid or duplicate embedding index %d", d.Index)
		}
		embeddings[d.Index] = d.Embedding
	}
	v.log.Debug("voyage embedding batch completed", zap.Int("inputs", len(texts)),
		zap.Int("tokens", result.Usage.TotalTokens))
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
