package cortex

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Embedder is an optional provider-neutral boundary. Model must identify both
// weights and preprocessing; changing either requires a new model identifier.
type Embedder interface {
	Model() string
	Embed(context.Context, []string) ([][]float64, error)
}

const MaxRecallMemories = 10000

var ErrRecallCapacity = errors.New("active memory set exceeds exact retrieval capacity; archive memories before recalling")

type RecallOptions struct {
	Limit       int
	LexicalOnly bool
}
type RecallResult struct {
	Hits       []Hit  `json:"hits"`
	Mode       string `json:"mode"`
	Indexed    int    `json:"indexed"`
	Candidates int    `json:"candidates"`
	Incomplete bool   `json:"incomplete"`
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	stop := map[string]bool{"the": true, "a": true, "an": true, "is": true, "are": true, "was": true, "were": true, "to": true, "of": true, "and": true, "or": true, "for": true, "in": true, "on": true, "at": true, "with": true, "by": true, "from": true, "my": true, "me": true, "i": true, "user": true, "users": true, "what": true, "which": true, "how": true, "do": true, "does": true, "should": true}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if w != "" && !stop[w] {
			out[w] = true
		}
	}
	return out
}
func overlap(a, b map[string]bool) float64 {
	if len(a) == 0 {
		return 0
	}
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return float64(n) / float64(len(a))
}

// selectContext only limits what the model sees, never deletes a memory. The
// caller reports ContextLimited so consumers can distinguish partial context.
func selectContext(obs []Observation, memories []Memory, limit int) []Memory {
	query := ""
	for _, o := range obs {
		query += " " + o.Content
	}
	terms := words(query)
	sort.SliceStable(memories, func(i, j int) bool {
		a, b := memories[i], memories[j]
		sa, sb := overlap(terms, words(a.Summary)), overlap(terms, words(b.Summary))
		if sa != sb {
			return sa > sb
		}
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if !a.LastSeenAt.Equal(b.LastSeenAt) {
			return a.LastSeenAt.After(b.LastSeenAt)
		}
		return a.ID < b.ID
	})
	if len(memories) > limit {
		return memories[:limit]
	}
	return memories
}

// RecallWithOptions fuses lexical and exact cosine ranks. The bounded exact
// scan avoids a mandatory vector extension or another database. No writes or
// automatic feedback occur during recall. Embedding failure is an error; an
// application may explicitly retry with LexicalOnly.
func (e *Engine) RecallWithOptions(ctx context.Context, user, query string, opts RecallOptions) (RecallResult, error) {
	out := RecallResult{Hits: []Hit{}, Mode: "lexical"}
	if strings.TrimSpace(user) == "" {
		return out, errors.New("user_id required")
	}
	if err := e.policy.validate(); err != nil {
		return out, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 200 {
		limit = 200
	}
	memories, err := e.store.retrievalMemories(ctx, user)
	if err != nil {
		return out, err
	}
	if len(memories) > MaxRecallMemories {
		return out, ErrRecallCapacity
	}
	out.Candidates = len(memories)
	type scored struct {
		id    string
		score float64
	}
	var lexical, semantic []scored
	terms := words(query)
	for _, m := range memories {
		v := overlap(terms, words(m.Summary))
		if strings.TrimSpace(query) == "" {
			v = m.Confidence
		}
		if v > 0 {
			lexical = append(lexical, scored{m.ID, v})
		}
	}
	order := func(xs []scored) {
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].score != xs[j].score {
				return xs[i].score > xs[j].score
			}
			return xs[i].id < xs[j].id
		})
	}
	order(lexical)
	if e.embedder != nil && !opts.LexicalOnly && strings.TrimSpace(query) != "" && len(memories) > 0 {
		out.Mode = "hybrid"
		vectors, err := e.embedder.Embed(ctx, []string{query})
		if err != nil {
			return out, errors.New("query embedding failed; explicitly select lexical-only recall to fall back")
		}
		if len(vectors) != 1 || !validVector(vectors[0]) {
			return out, errors.New("invalid query embedding")
		}
		stored, err := e.store.embeddings(ctx, user, e.embedder.Model())
		if err != nil {
			return out, err
		}
		for _, m := range memories {
			v, ok := stored[m.ID]
			if !ok || v.Hash != contentHash(m.Summary) || len(v.Values) != len(vectors[0]) {
				continue
			}
			out.Indexed++
			similarity := cosine(v.Values, vectors[0])
			if similarity > 0 {
				semantic = append(semantic, scored{m.ID, similarity})
			}
		}
		out.Incomplete = out.Indexed < len(memories)
		order(semantic)
	}
	fused := map[string]float64{}
	for _, channel := range [][]scored{lexical, semantic} {
		for i, v := range channel {
			if i >= limit*4 {
				break
			}
			fused[v.id] += 1 / (60 + float64(i+1))
		}
	}
	now := time.Now().UTC()
	for _, m := range memories {
		if score, ok := fused[m.ID]; ok {
			// Relevance remains primary. Multiplying reciprocal ranks by age can
			// bury the best semantic match beneath unrelated reinforced facts.
			score += 0.00001 * (Activation(m, now, e.policy) + m.Utility + m.Confidence)
			out.Hits = append(out.Hits, Hit{Memory: m, Score: score})
		}
	}
	sort.SliceStable(out.Hits, func(i, j int) bool {
		if out.Hits[i].Score != out.Hits[j].Score {
			return out.Hits[i].Score > out.Hits[j].Score
		}
		return out.Hits[i].Memory.ID < out.Hits[j].Memory.ID
	})
	if len(out.Hits) > limit {
		out.Hits = out.Hits[:limit]
	}
	// A network embedding call may have overlapped a forget or archive. Recheck
	// active rows before returning evidence retained in the caller's memory.
	if out.Mode == "hybrid" {
		current, err := e.store.retrievalMemories(ctx, user)
		if err != nil {
			return RecallResult{}, err
		}
		active := map[string]string{}
		for _, m := range current {
			active[m.ID] = m.Summary
		}
		filtered := out.Hits[:0]
		for _, hit := range out.Hits {
			if summary, ok := active[hit.Memory.ID]; ok && summary == hit.Memory.Summary {
				filtered = append(filtered, hit)
			}
		}
		out.Hits = filtered
	}
	return out, nil
}

func validVector(v []float64) bool {
	if len(v) == 0 || len(v) > 4096 {
		return false
	}
	norm := 0.0
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
		norm += x * x
	}
	return norm > 0 && !math.IsInf(norm, 0)
}
func cosine(a, b []float64) float64 {
	dot, an, bn := 0.0, 0.0, 0.0
	for i, x := range a {
		dot += x * b[i]
		an += x * x
		bn += b[i] * b[i]
	}
	if an == 0 || bn == 0 {
		return 0
	}
	return dot / math.Sqrt(an*bn)
}
