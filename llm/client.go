// Package llm provides a small client for an OpenAI-compatible chat endpoint.
// It has no knowledge of Cortex observations, memory operations, or prompts.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultBaseURL  = "http://127.0.0.1:8088/v1"
	DefaultModel    = "gpt-5.6-luna-max"
	DefaultTimeout  = 10 * time.Minute
	maxRequestSize  = 1 << 20
	maxResponseSize = 4 << 20
)

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Response struct {
	Text  string
	Model string
	Usage Usage
}

// Client is safe for concurrent use. Requests are not automatically retried:
// a timeout does not prove the upstream model did not run.
type Client struct {
	endpoint string
	apiKey   string
	model    string
	http     *http.Client
}

func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Timeout < 0 {
		return nil, errors.New("llm: timeout must be positive")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("llm: model must not be blank")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("llm: base URL must be HTTP(S) without credentials, query, or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	u.RawPath = ""
	return &Client{
		endpoint: u.String(), apiKey: cfg.APIKey, model: cfg.Model,
		http: &http.Client{
			Timeout: cfg.Timeout,
			// Do not forward prompts or credentials to a redirect target.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *Client) Model() string { return c.model }

// ValidateRequest checks the complete wire request, including prompt and JSON
// escaping overhead, without contacting the model.
func ValidateRequest(model string, messages []Message) error {
	_, err := encodeRequest(model, messages)
	return err
}

func encodeRequest(model string, messages []Message) ([]byte, error) {
	if len(messages) == 0 {
		return nil, errors.New("llm: at least one message is required")
	}
	for _, message := range messages {
		if message.Role != "system" && message.Role != "user" && message.Role != "assistant" {
			return nil, errors.New("llm: unsupported message role")
		}
		if strings.TrimSpace(message.Content) == "" {
			return nil, errors.New("llm: message content must not be blank")
		}
	}
	body, err := json.Marshal(struct {
		Model    string    `json:"model"`
		Messages []Message `json:"messages"`
		Stream   bool      `json:"stream"`
		Faithful bool      `json:"faithful"`
		Prewarm  bool      `json:"prewarm"`
	}{Model: model, Messages: messages})
	if err != nil {
		return nil, fmt.Errorf("llm: encode request: %w", err)
	}
	if len(body) > maxRequestSize {
		return nil, errors.New("llm: request exceeds 1 MiB; reduce the input chunk")
	}
	return body, nil
}

func (c *Client) Complete(ctx context.Context, messages []Message) (Response, error) {
	body, err := encodeRequest(c.model, messages)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("llm: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("llm: complete: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Upstream error bodies may echo credentials or private observations.
		return Response{}, fmt.Errorf("llm: unexpected HTTP status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return Response{}, fmt.Errorf("llm: read response: %w", err)
	}
	if len(data) > maxResponseSize {
		return Response{}, errors.New("llm: response exceeds 4 MiB")
	}
	var result struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role      string            `json:"role"`
				Content   string            `json:"content"`
				ToolCalls []json.RawMessage `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return Response{}, errors.New("llm: invalid chat response JSON")
	}
	if len(result.Choices) != 1 {
		return Response{}, errors.New("llm: expected exactly one completion")
	}
	choice := result.Choices[0]
	if choice.FinishReason != "stop" || choice.Message.Role != "assistant" || len(choice.Message.ToolCalls) != 0 {
		return Response{}, errors.New("llm: response is not a completed assistant text turn")
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		return Response{}, errors.New("llm: empty completion")
	}
	if result.Usage.PromptTokens < 0 || result.Usage.CompletionTokens < 0 || result.Usage.TotalTokens < 0 {
		return Response{}, errors.New("llm: negative token usage")
	}
	return Response{Text: choice.Message.Content, Model: result.Model, Usage: result.Usage}, nil
}
