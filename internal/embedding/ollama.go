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

// Ollama implements Embedder using a local Ollama instance.
type Ollama struct {
	url    string
	model  string
	client *http.Client
	log    *zap.Logger
}

// NewOllama creates an Ollama embedding client.
func NewOllama(url, model string, log *zap.Logger) *Ollama {
	return &Ollama{
		url:    url,
		model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
		log:    log,
	}
}

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{Model: o.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: unexpected status %d", resp.StatusCode)
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}

	return result.Embeddings, nil
}

func (o *Ollama) Dimension() int {
	// nomic-embed-text: 768, other models vary
	switch o.model {
	case "nomic-embed-text":
		return 768
	case "mxbai-embed-large":
		return 1024
	case "all-minilm":
		return 384
	default:
		return 768
	}
}

// EnsureModel pulls the model if not already present.
func (o *Ollama) EnsureModel(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"name": o.model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: pull model: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	// Read through response (streaming JSON) until done
	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		var msg map[string]any
		if err := decoder.Decode(&msg); err != nil {
			break
		}
	}

	o.log.Info("ollama model ready", zap.String("model", o.model))
	return nil
}
