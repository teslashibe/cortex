package cortex

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fixtureClassifier struct {
	call func([]ClassificationInput) ([]ClassificationPrediction, error)
}

func (f fixtureClassifier) Model() string { return "fixture-v1" }
func (f fixtureClassifier) Classify(_ context.Context, in []ClassificationInput) ([]ClassificationPrediction, error) {
	return f.call(in)
}
func fixturePrediction(id string) ClassificationPrediction {
	return ClassificationPrediction{id, map[string]float64{"durable": .9, "temporary": .05, "ignore": .03, "uncertain": .02}, map[string]float64{"fact": .85, "behavior": .03, "preference": .03, "relationship": .03, "project": .03, "routine": .03}}
}

func TestClassificationShadowProvenanceAndValidation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "owner", "source")
	calls := 0
	classifier := fixtureClassifier{func(in []ClassificationInput) ([]ClassificationPrediction, error) {
		calls++
		if len(in) != 1 || in[0].Text != "evidence" || in[0].Context != "evidence source" {
			t.Fatalf("input %+v", in)
		}
		return []ClassificationPrediction{fixturePrediction(in[0].ID)}, nil
	}}
	e := NewEngine(s, nil, WithClassifier(classifier))
	c := []Candidate{{"c1", "source", 0, 8}}
	r, err := e.ClassifyCandidates(ctx, "owner", c)
	if err != nil || r.Mode != "shadow" || len(r.Decisions) != 1 || r.Decisions[0].Abstain || r.Decisions[0].Kind != KindFact {
		t.Fatalf("classification %+v %v", r, err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM memories`).Scan(&n); err != nil || n != 0 {
		t.Fatal("shadow wrote memory")
	}
	if _, err := e.ClassifyCandidates(ctx, "other", c); err == nil {
		t.Fatal("foreign source")
	}
	if calls != 1 {
		t.Fatal("foreign source reached provider")
	}
	c[0].EndRune = 200
	if _, err := e.ClassifyCandidates(ctx, "owner", c); err == nil {
		t.Fatal("invalid span")
	}
	for _, p := range []ClassificationPrediction{{ID: "wrong"}, fixturePrediction("c1")} {
		if p.ID == "c1" {
			p.Retention["durable"] = math.NaN()
		}
		e.classifier = fixtureClassifier{func([]ClassificationInput) ([]ClassificationPrediction, error) {
			return []ClassificationPrediction{p}, nil
		}}
		if _, err := e.ClassifyCandidates(ctx, "owner", []Candidate{{"c1", "source", 0, 8}}); err == nil {
			t.Fatal("invalid prediction")
		}
	}
}

func TestClassificationRejectsForgottenEvidence(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "owner", "source")
	e := NewEngine(s, nil)
	e.classifier = fixtureClassifier{func(in []ClassificationInput) ([]ClassificationPrediction, error) {
		if _, err := e.ForgetObservation(ctx, "owner", "source"); err != nil {
			t.Fatal(err)
		}
		return []ClassificationPrediction{fixturePrediction("c1")}, nil
	}}
	r, err := e.ClassifyCandidates(ctx, "owner", []Candidate{{"c1", "source", 0, 8}})
	if err == nil || len(r.Decisions) != 0 {
		t.Fatal("forgotten evidence returned")
	}
}

func TestHTTPClassifierContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing auth")
		}
		var in struct {
			Policy string                `json:"policy_version"`
			Model  string                `json:"model"`
			Inputs []ClassificationInput `json:"inputs"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Error("request")
		}
		json.NewEncoder(w).Encode(map[string]any{"model": in.Model, "policy_version": in.Policy, "predictions": []ClassificationPrediction{fixturePrediction(in.Inputs[0].ID)}})
	}))
	defer server.Close()
	c, err := NewHTTPClassifier(server.URL, "fixture-v1", "test")
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Classify(context.Background(), []ClassificationInput{{ID: "a", Text: "text"}})
	if err != nil || len(r) != 1 || r[0].ID != "a" {
		t.Fatal(r, err)
	}
	s := testStore(t)
	seedObservations(t, s, "owner", "source")
	e := NewEngine(s, nil, WithClassifier(c))
	rec := httptest.NewRecorder()
	NewAPI(e).Routes().ServeHTTP(rec, httptest.NewRequest("POST", "/v1/classify", strings.NewReader(`{"user_id":"owner","candidates":[{"id":"a","observation_id":"source","start_rune":0,"end_rune":8}]}`)))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	for _, raw := range []string{`{"a":1}`, `{}`, `{"a":-1,"b":2}`, `{"a":0.5,"b":0.4}`} {
		p := map[string]float64{}
		json.Unmarshal([]byte(raw), &p)
		if _, _, err := distribution(p, []string{"a", "b"}); err == nil {
			t.Fatal("invalid distribution accepted")
		}
	}
}
