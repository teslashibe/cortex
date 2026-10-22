package cortex

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// These are Unicode code-point bounds, not tokenizer limits. An embedding
// provider must accept 512 code points without truncation or use a compatible
// preprocessing contract identified by Embedder.Model().
const ObservationWindowSize = 512
const ObservationWindowOverlap = 128
const MaxObservationWindows = 100000

var ErrWindowCapacity = errors.New("observation window capacity exceeded")

type ObservationWindow struct {
	ObservationID string `json:"observation_id"`
	StartRune     int    `json:"start_rune"`
	EndRune       int    `json:"end_rune"`
	Text          string `json:"text"`
}

// SplitObservation covers the entire input with deterministic overlapping
// windows. Offsets are half-open Unicode code-point offsets in original text.
func SplitObservation(o Observation) ([]ObservationWindow, error) {
	if !utf8.ValidString(o.Content) {
		return nil, errors.New("invalid observation UTF-8")
	}
	if utf8.RuneCountInString(o.Content) > ObservationWindowSize+(MaxObservationWindows-1)*(ObservationWindowSize-ObservationWindowOverlap) {
		return nil, ErrWindowCapacity
	}
	runes := []rune(o.Content)
	out := []ObservationWindow{}
	for start := 0; start < len(runes); start += ObservationWindowSize - ObservationWindowOverlap {
		end := min(start+ObservationWindowSize, len(runes))
		out = append(out, ObservationWindow{o.ID, start, end, string(runes[start:end])})
		if end == len(runes) {
			break
		}
	}
	return out, nil
}

type WindowHit struct {
	Window     ObservationWindow `json:"window"`
	Source     string            `json:"source"`
	Refs       map[string]any    `json:"refs"`
	ObservedAt string            `json:"observed_at"`
	Score      float64           `json:"score"`
}

type WindowRecallResult struct {
	Hits         []WindowHit `json:"hits"`
	Mode         string      `json:"mode"`
	Indexed      int         `json:"indexed"`
	Candidates   int         `json:"candidates"`
	Incomplete   bool        `json:"incomplete"`
	Segmentation string      `json:"segmentation,omitempty"`
	Matched      int         `json:"matched"`
	Truncated    bool        `json:"truncated"`
}

type storedWindow struct {
	end int
	storedEmbedding
}

func windowKey(id string, start int) string { return fmt.Sprintf("%s:%d", id, start) }

func (s *Store) windowEmbeddings(ctx context.Context, user, model string) (map[string]storedWindow, error) {
	rows, err := s.pool.Query(ctx, `SELECT v.observation_id,v.start_rune,v.end_rune,v.content_hash,v.embedding
		FROM observation_window_embeddings v JOIN observations o ON o.id=v.observation_id
		WHERE o.user_id=$1 AND v.model=$2 AND `+eligibleObservation+` LIMIT $3`, user, model, MaxObservationWindows+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]storedWindow{}
	for rows.Next() {
		var id string
		var start int
		var v storedWindow
		if err := rows.Scan(&id, &start, &v.end, &v.Hash, &v.Values); err != nil {
			return nil, err
		}
		out[windowKey(id, start)] = v
		if len(out) > MaxObservationWindows {
			return nil, ErrWindowCapacity
		}
	}
	return out, rows.Err()
}

func observationWindows(obs []Observation) ([]ObservationWindow, error) {
	out := []ObservationWindow{}
	for _, o := range obs {
		w, err := SplitObservation(o)
		if err != nil {
			return nil, err
		}
		if len(out)+len(w) > MaxObservationWindows {
			return nil, ErrWindowCapacity
		}
		out = append(out, w...)
	}
	return out, nil
}

// IndexObservationWindows is a separate explicit consent boundary for sending
// raw evidence to an embedder. A complete observation is published atomically.
// Indexed counts observations; limit never slices an observation's windows.
func (e *Engine) IndexObservationWindows(ctx context.Context, user string, limit int) (IndexResult, error) {
	var out IndexResult
	if strings.TrimSpace(user) == "" {
		return out, errors.New("user required")
	}
	if e.embedder == nil || strings.TrimSpace(e.embedder.Model()) == "" {
		return out, errors.New("no embedder configured")
	}
	if limit <= 0 {
		limit = 1000
	}
	if limit > MaxRecallObservations {
		return out, ErrObservationCapacity
	}
	obs, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return out, err
	}
	allWindows, err := e.splitObservations(ctx, obs)
	if err != nil {
		return out, err
	}
	byID := map[string][]ObservationWindow{}
	for _, w := range allWindows {
		byID[w.ObservationID] = append(byID[w.ObservationID], w)
	}
	stored, err := e.store.windowEmbeddings(ctx, user, e.windowModelKey())
	if err != nil {
		return out, err
	}
	processed := 0
	for _, o := range obs {
		windows := byID[o.ID]
		hash := contentHash(o.Content)
		complete := true
		for _, w := range windows {
			v, ok := stored[windowKey(o.ID, w.StartRune)]
			if !ok || v.Hash != hash || v.end != w.EndRune || !validVector(v.Values) {
				complete = false
				break
			}
		}
		if complete {
			continue
		}
		if processed == limit {
			out.Incomplete = true
			break
		}
		processed++
		vectors := [][]float64{}
		dimension := 0
		for start := 0; start < len(windows); start += 32 {
			batch := windows[start:min(start+32, len(windows))]
			texts := make([]string, len(batch))
			for i, w := range batch {
				texts[i] = w.Text
			}
			v, err := e.embedder.Embed(ctx, texts)
			if err != nil {
				out.Incomplete = true
				return out, errors.New("window embedding failed")
			}
			if len(v) != len(batch) {
				return out, errors.New("embedding count mismatch")
			}
			for _, x := range v {
				if !validVector(x) {
					return out, errors.New("invalid window embedding")
				}
				if dimension == 0 {
					dimension = len(x)
				}
				if len(x) != dimension {
					return out, errors.New("embedding dimension mismatch")
				}
			}
			vectors = append(vectors, v...)
		}
		published, err := e.store.publishWindows(ctx, user, o, windows, vectors, e.windowModelKey())
		if err != nil {
			return out, err
		}
		if published {
			out.Indexed++
		} else {
			out.Incomplete = true
		}
	}
	return out, nil
}

func (s *Store) publishWindows(ctx context.Context, user string, o Observation, windows []ObservationWindow, vectors [][]float64, model string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `SELECT o.id FROM observations o WHERE o.user_id=$1 AND o.id=$2 AND o.content=$3 AND `+eligibleObservation+` FOR UPDATE OF o`, user, o.ID, o.Content).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM observation_window_embeddings WHERE observation_id=$1 AND model=$2`, id, model); err != nil {
		return false, err
	}
	hash := contentHash(o.Content)
	for i, w := range windows {
		if _, err = tx.Exec(ctx, `INSERT INTO observation_window_embeddings(observation_id,model,content_hash,start_rune,end_rune,embedding) VALUES($1,$2,$3,$4,$5,$6)`, id, model, hash, w.StartRune, w.EndRune, vectors[i]); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

// RecallObservationWindows returns excerpts with original offsets and source
// attribution. ObservedAt is message time, never an inferred event date.
// Lifecycle eligibility is rechecked after external inference, as in raw recall.
func (e *Engine) RecallObservationWindows(ctx context.Context, user, query string, opts RecallOptions) (WindowRecallResult, error) {
	out := WindowRecallResult{Hits: []WindowHit{}, Mode: "lexical"}
	if strings.TrimSpace(user) == "" || strings.TrimSpace(query) == "" {
		return out, errors.New("user and query required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	limit = min(limit, 200)
	obs, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return out, err
	}
	windows, err := e.splitObservations(ctx, obs)
	if err != nil {
		return out, err
	}
	out.Candidates = len(windows)
	if e.segmenter != nil {
		out.Segmentation = e.segmenter.ID()
	}
	stored := map[string]storedWindow{}
	var vector []float64
	if e.embedder != nil && !opts.LexicalOnly && len(windows) > 0 {
		out.Mode = "hybrid"
		v, err := e.embedder.Embed(ctx, []string{query})
		if err != nil {
			return out, errors.New("query embedding failed; explicitly select lexical-only recall to fall back")
		}
		if len(v) != 1 || !validVector(v[0]) {
			return out, errors.New("invalid query embedding")
		}
		vector = v[0]
		stored, err = e.store.windowEmbeddings(ctx, user, e.windowModelKey())
		if err != nil {
			return out, err
		}
	}
	type ranked struct {
		key   string
		score float64
	}
	var lexical, semantic []ranked
	original := map[string]Observation{}
	hashes := map[string]string{}
	for _, o := range obs {
		original[o.ID] = o
		hashes[o.ID] = contentHash(o.Content)
	}
	terms := words(query)
	for _, w := range windows {
		key := windowKey(w.ObservationID, w.StartRune)
		if score := overlap(terms, words(w.Text)); score > 0 {
			lexical = append(lexical, ranked{key, score})
		}
		if v, ok := stored[key]; ok && v.end == w.EndRune && v.Hash == hashes[w.ObservationID] && len(v.Values) == len(vector) && validVector(v.Values) {
			out.Indexed++
			if score := cosine(v.Values, vector); score > 0 {
				semantic = append(semantic, ranked{key, score})
			}
		}
	}
	out.Incomplete = out.Mode == "hybrid" && out.Indexed < len(windows)
	fused := map[string]float64{}
	for _, channel := range [][]ranked{lexical, semantic} {
		sort.Slice(channel, func(i, j int) bool {
			if channel[i].score != channel[j].score {
				return channel[i].score > channel[j].score
			}
			return channel[i].key < channel[j].key
		})
		for i, c := range channel[:min(len(channel), limit*4)] {
			fused[c.key] += 1 / (60 + float64(i+1))
		}
	}
	current, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return out, err
	}
	eligible := map[string]Observation{}
	for _, o := range current {
		if before, ok := original[o.ID]; ok && before.Content == o.Content {
			eligible[o.ID] = o
		}
	}
	for _, w := range windows {
		if o, ok := eligible[w.ObservationID]; ok {
			if score, ok := fused[windowKey(w.ObservationID, w.StartRune)]; ok {
				out.Hits = append(out.Hits, WindowHit{w, o.Source, o.Refs, o.At.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), score})
			}
		}
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
	out.Matched = len(out.Hits)
	out.Truncated = out.Matched > limit
	if out.Truncated {
		out.Hits = out.Hits[:limit]
	}
	return out, nil
}
