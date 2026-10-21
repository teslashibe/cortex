package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const successfulResponse = `{"model":"resolved-model","choices":[{"message":{"role":"assistant","content":"{\"ops\":[]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`

func TestComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing request headers")
		}
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if string(request["model"]) != `"gpt-6-luna-medium"` {
			t.Errorf("model: %s", request["model"])
		}
		for _, key := range []string{"stream", "faithful", "prewarm"} {
			if string(request[key]) != "false" {
				t.Errorf("%s must be explicitly false", key)
			}
		}
		for _, key := range []string{"tools", "response_format", "max_tokens", "temperature"} {
			if _, ok := request[key]; ok {
				t.Errorf("unsupported field %s", key)
			}
		}
		var messages []Message
		if err := json.Unmarshal(request["messages"], &messages); err != nil {
			t.Fatal(err)
		}
		if len(messages) != 2 || messages[1].Content != "preserve 私の記憶" {
			t.Errorf("messages: %+v", messages)
		}
		_, _ = w.Write([]byte(successfulResponse))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL + "/v1/", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Complete(context.Background(), []Message{{Role: "system", Content: "instructions"}, {Role: "user", Content: "preserve 私の記憶"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != `{"ops":[]}` || response.Model != "resolved-model" || response.Usage.TotalTokens != 14 {
		t.Errorf("response: %+v", response)
	}
	if client.Model() != DefaultModel {
		t.Errorf("configured model: %s", client.Model())
	}
}

func TestRejectInvalidResponse(t *testing.T) {
	cases := map[string]string{
		"malformed":      "not-json-private-text",
		"missing choice": `{}`,
		"empty":          `{"choices":[{"message":{"role":"assistant","content":" "},"finish_reason":"stop"}]}`,
		"truncated":      strings.Replace(successfulResponse, `"stop"`, `"length"`, 1),
		"tool call":      strings.Replace(successfulResponse, `"role":"assistant"`, `"tool_calls":[{}],"role":"assistant"`, 1),
		"wrong role":     strings.Replace(successfulResponse, `"assistant"`, `"user"`, 1),
		"negative usage": strings.Replace(successfulResponse, `"total_tokens":14`, `"total_tokens":-1`, 1),
		"trailing JSON":  successfulResponse + `{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "test"}})
			if err == nil {
				t.Fatal("expected rejection")
			}
			if strings.Contains(err.Error(), "private-text") {
				t.Fatal("response leaked into error")
			}
		})
	}
}

func TestHTTPErrorIsNotRetriedOrEchoed(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("private response text"))
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	_, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "test"}})
	if err == nil || !strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "private") {
		t.Fatalf("error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("requests: %d", calls)
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwarded = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, APIKey: "secret"})
	_, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "private"}})
	if err == nil || forwarded {
		t.Fatalf("redirect followed=%v err=%v", forwarded, err)
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, Timeout: 20 * time.Millisecond})
	_, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "test"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Complete(ctx, []Message{{Role: "user", Content: "test"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestBounds(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseSize+1)))
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL})
	_, err := client.Complete(context.Background(), []Message{{Role: "user", Content: strings.Repeat("x", maxRequestSize)}})
	if err == nil || calls != 0 {
		t.Fatalf("oversized request: calls=%d err=%v", calls, err)
	}
	_, err = client.Complete(context.Background(), []Message{{Role: "user", Content: "test"}})
	if err == nil || !strings.Contains(err.Error(), "4 MiB") {
		t.Fatalf("oversized response: %v", err)
	}
}

func TestConfigAndInputValidation(t *testing.T) {
	for _, base := range []string{"file:///tmp/model", "http://", "http://user:secret@localhost", "http://localhost?token=x", "http://localhost#fragment", ":bad"} {
		if _, err := New(Config{BaseURL: base}); err == nil {
			t.Errorf("accepted URL %q", base)
		}
	}
	if _, err := New(Config{Timeout: -time.Second}); err == nil {
		t.Error("accepted negative timeout")
	}
	client, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, messages := range [][]Message{nil, {{Role: "tool", Content: "test"}}, {{Role: "user", Content: " "}}} {
		if _, err := client.Complete(context.Background(), messages); err == nil {
			t.Errorf("accepted %+v", messages)
		}
	}
}
