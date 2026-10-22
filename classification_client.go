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

// HTTPClassifier adapts an optional host-supplied classifier service. It does
// not load weights, train, retry, or send data until ClassifyCandidates is called.
type HTTPClassifier struct {
	endpoint, model, key string
	client               *http.Client
}

func NewHTTPClassifier(endpoint, model, key string) (*HTTPClassifier, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(model) == "" {
		return nil, errors.New("classifier URL and model required")
	}
	return &HTTPClassifier{endpoint, model, key, &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *HTTPClassifier) Model() string { return c.model }
func (c *HTTPClassifier) Classify(ctx context.Context, inputs []ClassificationInput) ([]ClassificationPrediction, error) {
	body, err := json.Marshal(struct {
		Policy string                `json:"policy_version"`
		Model  string                `json:"model"`
		Inputs []ClassificationInput `json:"inputs"`
	}{ClassificationPolicyVersion, c.model, inputs})
	if err != nil {
		return nil, err
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
		return nil, errors.New("classifier request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("classifier returned an unsuccessful status")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, errors.New("classifier response too large")
	}
	var out struct {
		Model       string                     `json:"model"`
		Policy      string                     `json:"policy_version"`
		Predictions []ClassificationPrediction `json:"predictions"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || out.Model != c.model || out.Policy != ClassificationPolicyVersion {
		return nil, errors.New("invalid classifier response")
	}
	return out.Predictions, nil
}
