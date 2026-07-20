// Package providerhttp holds the small authenticated-JSON-POST scaffolding
// shared by the embedding and rerank provider adapters (Voyage, Cohere, …).
//
// Every one of those adapters performs the same request: marshal a request
// struct, POST it as JSON with a Bearer token, check the status, and decode the
// 200 body. Keeping that sequence in one place means a change to timeouts,
// headers, or error framing happens once instead of in four near-identical
// copies. Each caller keeps its own typed error by supplying an onHTTPError
// callback for the non-2xx case.
package providerhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// PostJSON marshals reqBody to JSON, POSTs it to url with a
// Content-Type: application/json and Authorization: Bearer <apiKey> header,
// and decodes a 200 response body into out.
//
// On a non-200 status it returns onHTTPError(resp) verbatim, so each caller
// keeps its own error type (e.g. *rerank.RerankError, *embedding.EmbedError)
// and the status code survives errors.As unwrapping. The response body is
// always closed. Marshal / request-construction / transport / decode failures
// are wrapped with a short prefix; callers add their own provider prefix.
func PostJSON(ctx context.Context, client *http.Client, url, apiKey string, reqBody, out any, onHTTPError func(*http.Response) error) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body

	if resp.StatusCode != http.StatusOK {
		return onHTTPError(resp)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
