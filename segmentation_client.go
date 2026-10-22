package cortex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPSegmenter struct {
	endpoint, id, key string
	limit             int
	client            *http.Client
}

func NewHTTPSegmenter(endpoint, id, key string, maxTokens int) (*HTTPSegmenter, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(id) == "" || len(id) > 512 || maxTokens < 1 || maxTokens > 65536 {
		return nil, errors.New("valid segmenter URL, identity and token limit required")
	}
	return &HTTPSegmenter{endpoint, id, key, maxTokens, &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *HTTPSegmenter) ID() string     { return c.id }
func (c *HTTPSegmenter) MaxTokens() int { return c.limit }
func (c *HTTPSegmenter) Split(ctx context.Context, inputs []SegmentInput) ([]SegmentedText, error) {
	if len(inputs) < 1 || len(inputs) > 32 {
		return nil, errors.New("segmentation requires 1..32 inputs")
	}
	body, err := json.Marshal(struct {
		ID        string         `json:"segmenter"`
		MaxTokens int            `json:"max_tokens"`
		Inputs    []SegmentInput `json:"inputs"`
	}{c.id, c.limit, inputs})
	if err != nil {
		return nil, err
	}
	if len(body) > 1<<20 {
		return nil, errors.New("segmentation request exceeds 1 MiB")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.New("segmentation request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("segmentation endpoint returned an error")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 {
		return nil, errors.New("invalid segmentation response size")
	}
	var out struct {
		ID        string          `json:"segmenter"`
		MaxTokens int             `json:"max_tokens"`
		Results   []SegmentedText `json:"results"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || out.ID != c.id || out.MaxTokens != c.limit {
		return nil, errors.New("segmentation contract mismatch")
	}
	return out.Results, nil
}
