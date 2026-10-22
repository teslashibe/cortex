package cortex

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const ClassificationPolicyVersion = "cortex-candidate-v1"
const MaxClassificationCandidates = 128

// A candidate identifies an exact, preferably atomic, source span. Extraction
// and review belong to the caller. Offsets count Unicode code points.
type Candidate struct {
	ID            string `json:"id"`
	ObservationID string `json:"observation_id"`
	StartRune     int    `json:"start_rune"`
	EndRune       int    `json:"end_rune"`
}

type ClassificationInput struct {
	ID         string         `json:"id"`
	Text       string         `json:"text"`
	Context    string         `json:"context"`
	Source     string         `json:"source"`
	Refs       map[string]any `json:"refs"`
	ObservedAt string         `json:"observed_at"`
}

// Probabilities are model estimates, not evidence confidence. Calibration must
// be measured on independent labels before setting an operational threshold.
type ClassificationPrediction struct {
	ID        string             `json:"id"`
	Retention map[string]float64 `json:"retention"`
	Kind      map[string]float64 `json:"kind"`
}

type Classifier interface {
	Model() string
	Classify(context.Context, []ClassificationInput) ([]ClassificationPrediction, error)
}

type CandidateDecision struct {
	Candidate   Candidate                `json:"candidate"`
	ContentHash string                   `json:"content_hash"`
	Retention   string                   `json:"retention"`
	Kind        Kind                     `json:"kind"`
	Abstain     bool                     `json:"abstain"`
	Prediction  ClassificationPrediction `json:"prediction"`
}

type ClassificationResult struct {
	Mode          string              `json:"mode"`
	Model         string              `json:"model"`
	PolicyVersion string              `json:"policy_version"`
	Decisions     []CandidateDecision `json:"decisions"`
}

// ClassifyCandidates is deliberately shadow-only: no operation is applied and
// no training data is stored. The host may keep this report in a private journal.
// Deleted, changed, or ineligible source evidence invalidates the whole response.
func (e *Engine) ClassifyCandidates(ctx context.Context, user string, candidates []Candidate) (ClassificationResult, error) {
	out := ClassificationResult{Mode: "shadow", PolicyVersion: ClassificationPolicyVersion, Decisions: []CandidateDecision{}}
	if strings.TrimSpace(user) == "" || len(candidates) == 0 || len(candidates) > MaxClassificationCandidates {
		return out, errors.New("user and 1..128 candidates required")
	}
	if e.classifier == nil || strings.TrimSpace(e.classifier.Model()) == "" {
		return out, errors.New("no classifier configured")
	}
	out.Model = e.classifier.Model()
	obs, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return out, err
	}
	byID := map[string]Observation{}
	for _, o := range obs {
		byID[o.ID] = o
	}
	inputs := make([]ClassificationInput, len(candidates))
	seen := map[string]bool{}
	for i, c := range candidates {
		o, ok := byID[c.ObservationID]
		if !ok || c.ID == "" || seen[c.ID] || !utf8.ValidString(o.Content) {
			return out, errors.New("invalid candidate or unavailable source")
		}
		seen[c.ID] = true
		runes := []rune(o.Content)
		if c.StartRune < 0 || c.EndRune <= c.StartRune || c.EndRune > len(runes) || c.EndRune-c.StartRune > ObservationWindowSize {
			return out, errors.New("candidate span must contain 1..512 code points")
		}
		text := string(runes[c.StartRune:c.EndRune])
		if strings.TrimSpace(text) == "" {
			return out, errors.New("empty candidate text")
		}
		inputs[i] = ClassificationInput{c.ID, text, string(runes[max(0, c.StartRune-256):min(len(runes), c.EndRune+256)]), o.Source, o.Refs, o.At.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
	}
	callCtx, cancel := context.WithTimeout(ctx, ModelTimeout)
	defer cancel()
	predictions, err := e.classifier.Classify(callCtx, inputs)
	if err != nil {
		return out, errors.New("classification failed")
	}
	if len(predictions) != len(candidates) {
		return out, errors.New("classification count mismatch")
	}
	predicted := map[string]ClassificationPrediction{}
	for _, p := range predictions {
		if !seen[p.ID] {
			return out, errors.New("unknown prediction ID")
		}
		if _, ok := predicted[p.ID]; ok {
			return out, errors.New("duplicate prediction ID")
		}
		predicted[p.ID] = p
	}
	current, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return out, err
	}
	valid := map[string]bool{}
	for _, o := range current {
		before, ok := byID[o.ID]
		if ok && before.Content == o.Content {
			valid[o.ID] = true
		}
	}
	for _, c := range candidates {
		if !valid[c.ObservationID] {
			return ClassificationResult{}, errors.New("source changed during classification")
		}
		p := predicted[c.ID]
		r, rp, err := distribution(p.Retention, []string{"durable", "temporary", "ignore", "uncertain"})
		if err != nil {
			return ClassificationResult{}, err
		}
		k, kp, err := distribution(p.Kind, []string{"fact", "behavior", "preference", "relationship", "project", "routine"})
		if err != nil {
			return ClassificationResult{}, err
		}
		out.Decisions = append(out.Decisions, CandidateDecision{c, contentHash(byID[c.ObservationID].Content), r, Kind(k), r == "uncertain" || rp < 0.8 || ((r == "durable" || r == "temporary") && kp < 0.8), p})
	}
	return out, nil
}

func distribution(p map[string]float64, labels []string) (string, float64, error) {
	if len(p) != len(labels) {
		return "", 0, errors.New("incomplete probability distribution")
	}
	total := 0.0
	best := ""
	score := -1.0
	for _, label := range labels {
		v, ok := p[label]
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return "", 0, fmt.Errorf("invalid probability for %s", label)
		}
		total += v
		if v > score {
			best, score = label, v
		}
	}
	if math.Abs(total-1) > 1e-6 {
		return "", 0, errors.New("probabilities must sum to one")
	}
	return best, score, nil
}
