package cortex

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// EvidenceRecallResult describes retrieved evidence, not an exhaustive event
// inventory. Multiple windows or messages may describe the same real event.
type EvidenceRecallResult struct {
	Hits              []WindowHit `json:"hits"`
	Queries           int         `json:"queries"`
	Sources           int         `json:"sources"`
	Truncated         bool        `json:"truncated"`
	IndexIncomplete   bool        `json:"index_incomplete"`
	ChangedDuringRead bool        `json:"changed_during_read"`
	Coverage          string      `json:"coverage"`
}

// RecallEvidence merges up to four caller-supplied query variants. The host
// owns query generation and event identity. It does not infer either here.
func (e *Engine) RecallEvidence(ctx context.Context, user string, queries []string, opts RecallOptions) (EvidenceRecallResult, error) {
	out := EvidenceRecallResult{Hits: []WindowHit{}, Coverage: "retrieved_evidence_only"}
	if len(queries) < 1 || len(queries) > 4 {
		return out, errors.New("1..4 evidence queries required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 40
	}
	if limit > 200 {
		return out, errors.New("evidence limit exceeds 200")
	}
	seen := map[string]bool{}
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if q == "" || len(q) > 4096 || seen[q] {
			return out, errors.New("distinct nonempty queries of at most 4096 bytes required")
		}
		seen[q] = true
	}
	hits := map[string]WindowHit{}
	before, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return out, err
	}
	hashes := map[string]string{}
	for _, o := range before {
		hashes[o.ID] = contentHash(o.Content)
	}
	for _, q := range queries {
		r, err := e.RecallObservationWindows(ctx, user, q, RecallOptions{Limit: limit, LexicalOnly: opts.LexicalOnly})
		if err != nil {
			return out, err
		}
		out.Queries++
		out.Truncated = out.Truncated || r.Truncated
		out.IndexIncomplete = out.IndexIncomplete || r.Incomplete
		for i, h := range r.Hits {
			key := windowKey(h.Window.ObservationID, h.Window.StartRune)
			old := hits[key]
			h.Score = old.Score + 1/(60+float64(i+1))
			hits[key] = h
		}
	}
	current, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return out, err
	}
	eligible := map[string]Observation{}
	for _, o := range current {
		eligible[o.ID] = o
	}
	for _, h := range hits {
		o, ok := eligible[h.Window.ObservationID]
		runes := []rune(o.Content)
		if !ok || hashes[o.ID] != contentHash(o.Content) || h.Window.StartRune < 0 || h.Window.EndRune > len(runes) || string(runes[h.Window.StartRune:h.Window.EndRune]) != h.Window.Text {
			out.ChangedDuringRead = true
			continue
		}
		out.Hits = append(out.Hits, h)
	}
	sort.Slice(out.Hits, func(i, j int) bool {
		a, b := out.Hits[i], out.Hits[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Window.ObservationID != b.Window.ObservationID {
			return a.Window.ObservationID < b.Window.ObservationID
		}
		return a.Window.StartRune < b.Window.StartRune
	})
	if len(out.Hits) > limit {
		out.Truncated = true
		out.Hits = out.Hits[:limit]
	}
	sources := map[string]bool{}
	for _, h := range out.Hits {
		sources[h.Window.ObservationID] = true
	}
	out.Sources = len(sources)
	return out, nil
}
