package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddingContract(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{{`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`, true}, {`{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`, false}, {`{"data":[{"index":0,"embedding":[0,0]},{"index":1,"embedding":[0,1]}]}`, false}} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer test-key" {
				t.Error("wrong embedding request")
			}
			fmt.Fprint(w, tc.body)
		}))
		c, err := NewEmbeddingClient(Config{BaseURL: s.URL + "/v1", Model: "test", APIKey: "test-key"})
		if err != nil {
			t.Fatal(err)
		}
		v, err := c.Embed(context.Background(), []string{"one", "two"})
		s.Close()
		if tc.valid {
			if err != nil || len(v) != 2 || v[0][0] != 1 {
				t.Fatalf("ordered embeddings %v %v", v, err)
			}
		} else if err == nil {
			t.Fatal("accepted invalid embeddings")
		}
	}
}

func TestEmbeddingPreservesWhitespaceSourceWindow(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Input) != 1 || body.Input[0] != " \n\t " {
			t.Errorf("source changed: %+v %v", body, err)
		}
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1,0]}]}`)
	}))
	defer s.Close()
	c, err := NewEmbeddingClient(Config{BaseURL: s.URL, Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Embed(context.Background(), []string{" \n\t "}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Embed(context.Background(), []string{""}); err == nil {
		t.Fatal("accepted empty source")
	}
	if calls != 1 {
		t.Fatalf("unexpected provider calls: %d", calls)
	}
}
