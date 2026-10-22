package cortex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type segmentFixture struct{ spans []SegmentSpan }

func (s segmentFixture) ID() string     { return "fixture-v1" }
func (s segmentFixture) MaxTokens() int { return 20 }
func (s segmentFixture) Split(_ context.Context, inputs []SegmentInput) ([]SegmentedText, error) {
	out := make([]SegmentedText, len(inputs))
	for i, v := range inputs {
		out[i] = SegmentedText{v.ID, s.spans}
	}
	return out, nil
}

func TestSegmenterValidatesFullSource(t *testing.T) {
	valid := []SegmentSpan{{0, 4, 10}, {2, 6, 10}}
	for _, spans := range [][]SegmentSpan{valid, {{1, 6, 10}}, {{0, 4, 21}, {2, 6, 10}}, {{0, 2, 4}, {3, 6, 5}}, {{0, 3, 5}}, {{0, 5, 5}, {2, 4, 5}}, {{0, 6, 0}}} {
		e := NewEngine(nil, nil, WithWindowSegmenter(segmentFixture{spans}))
		got, err := e.splitObservations(context.Background(), []Observation{{ID: "o", Content: "a界🙂def"}})
		if reflect.DeepEqual(spans, valid) {
			if err != nil || len(got) != 2 || got[0].Text != "a界🙂d" {
				t.Fatalf("%+v %v", got, err)
			}
		} else if err == nil {
			t.Fatalf("accepted %+v", spans)
		}
	}
}

func TestHTTPTokenContract(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b struct {
				ID     string         `json:"segmenter"`
				Limit  int            `json:"max_tokens"`
				Inputs []SegmentInput `json:"inputs"`
			}
			if json.NewDecoder(r.Body).Decode(&b) != nil || b.ID != "s" || b.Limit != 20 || len(b.Inputs) != 1 {
				t.Error("request contract")
			}
			id := "s"
			if mismatch {
				id = "other"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"segmenter": id, "max_tokens": 20, "results": []SegmentedText{{"o", []SegmentSpan{{0, 1, 3}}}}})
		}))
		c, err := NewHTTPSegmenter(server.URL, "s", "", 20)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Split(context.Background(), []SegmentInput{{"o", "x"}})
		server.Close()
		if (err != nil) != mismatch {
			t.Fatalf("mismatch=%v err=%v", mismatch, err)
		}
	}
}

func TestSegmenterNamespaceAndForget(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "owner", "a")
	e := NewEngine(s, nil, WithEmbedder(vectorFixture{}))
	if _, err := e.IndexObservationWindows(ctx, "owner", 1); err != nil {
		t.Fatal(err)
	}
	old := e.windowModelKey()
	e.segmenter = segmentFixture{[]SegmentSpan{{0, len([]rune("evidence a")), 12}}}
	if e.windowModelKey() == old {
		t.Fatal("mixed preprocessing namespaces")
	}
	if r, err := e.RecallObservationWindows(ctx, "owner", "evidence", RecallOptions{}); err != nil || r.Indexed != 0 || !r.Incomplete {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := e.IndexObservationWindows(ctx, "owner", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ForgetObservation(ctx, "owner", "a"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM observation_window_embeddings`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("retained vectors %d %v", n, err)
	}
}
