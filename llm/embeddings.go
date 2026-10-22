package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
)

// EmbeddingClient uses the standard non-streaming /embeddings contract.
// A separate endpoint may run locally; no embedding provider is required.
type EmbeddingClient struct {
	client   *Client
	endpoint string
}

func NewEmbeddingClient(cfg Config) (*EmbeddingClient, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("embedding model required")
	}
	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return &EmbeddingClient{client: c, endpoint: strings.TrimSuffix(c.endpoint, "/chat/completions") + "/embeddings"}, nil
}
func (c *EmbeddingClient) Model() string { return c.client.Model() }
func (c *EmbeddingClient) Embed(ctx context.Context, input []string) ([][]float64, error) {
	if len(input) == 0 || len(input) > 32 {
		return nil, errors.New("embedding batch must contain 1 to 32 inputs")
	}
	for _, s := range input {
		// A source window can consist entirely of whitespace. Preserve those
		// nonempty spans so complete source indexing does not fail mid-batch.
		if s == "" {
			return nil, errors.New("empty embedding input")
		}
	}
	body, err := json.Marshal(struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{c.Model(), input})
	if err != nil {
		return nil, err
	}
	if len(body) > maxRequestSize {
		return nil, errors.New("embedding request exceeds 1 MiB")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.client.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.client.apiKey)
	}
	resp, err := c.client.http.Do(req)
	if err != nil {
		return nil, errors.New("embedding request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("embedding endpoint returned an error")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil || len(raw) > maxResponseSize {
		return nil, errors.New("invalid embedding response size")
	}
	var result struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Data) != len(input) {
		return nil, errors.New("invalid embedding response")
	}
	out := make([][]float64, len(input))
	dimension := 0
	for _, d := range result.Data {
		if d.Index < 0 || d.Index >= len(out) || out[d.Index] != nil || len(d.Embedding) == 0 || len(d.Embedding) > 4096 {
			return nil, errors.New("invalid embedding index or dimension")
		}
		if dimension == 0 {
			dimension = len(d.Embedding)
		}
		if len(d.Embedding) != dimension {
			return nil, errors.New("inconsistent embedding dimensions")
		}
		norm := 0.0
		for _, x := range d.Embedding {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, errors.New("non-finite embedding")
			}
			norm += x * x
		}
		if norm == 0 || math.IsInf(norm, 0) {
			return nil, errors.New("invalid embedding norm")
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}
