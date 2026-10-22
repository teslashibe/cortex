package cortex

import (
	"context"
	"errors"
	"sort"
	"strings"
)

const MaxRecallObservations = 20000

var ErrObservationCapacity = errors.New("observation set exceeds exact retrieval capacity")

// Raw observations are evidence, not validated current facts. Exclude any source
// of a superseded, archived or expired memory, even when it supports other facts.
// This conservative rule prevents raw retrieval from bypassing memory lifecycle.
const eligibleObservation = `NOT EXISTS (
	SELECT 1 FROM memories m WHERE m.user_id=o.user_id AND m.sources ? o.id
	AND (m.contradicted_by IS NOT NULL OR m.archived_at IS NOT NULL OR m.expires_at<=now())
)`

type ObservationHit struct {
	Observation Observation `json:"observation"`
	Score       float64     `json:"score"`
}

type ObservationRecallResult struct {
	Hits       []ObservationHit `json:"hits"`
	Mode       string           `json:"mode"`
	Indexed    int              `json:"indexed"`
	Candidates int              `json:"candidates"`
	Incomplete bool             `json:"incomplete"`
}

func (s *Store) retrievalObservations(ctx context.Context, user string) ([]Observation, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id,o.user_id,o.source,o.content,o.refs,o.salience,o.at,
		COALESCE(o.idempotency_key,''),COALESCE(o.tool_name,''),COALESCE(o.intent_id,''),COALESCE(o.outcome_class,''),o.turn_index
		FROM observations o WHERE o.user_id=$1 AND `+eligibleObservation+` ORDER BY o.id LIMIT $2`, user, MaxRecallObservations+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Observation{}
	for rows.Next() {
		var o Observation
		if err := rows.Scan(&o.ID, &o.UserID, &o.Source, &o.Content, &o.Refs, &o.Salience, &o.At, &o.IdempotencyKey, &o.ToolName, &o.IntentID, &o.OutcomeClass, &o.TurnIndex); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > MaxRecallObservations {
		return nil, ErrObservationCapacity
	}
	return out, nil
}

func (s *Store) observationEmbeddings(ctx context.Context, user, model string) (map[string]storedEmbedding, error) {
	rows, err := s.pool.Query(ctx, `SELECT v.observation_id,v.content_hash,v.embedding FROM observation_embeddings v
		JOIN observations o ON o.id=v.observation_id WHERE o.user_id=$1 AND v.model=$2 AND `+eligibleObservation, user, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]storedEmbedding{}
	for rows.Next() {
		var id string
		var v storedEmbedding
		if err := rows.Scan(&id, &v.Hash, &v.Values); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}

// IndexObservations is opt-in and separate from Index: enabling memory embeddings
// does not automatically send raw conversations to an embedding provider.
func (e *Engine) IndexObservations(ctx context.Context, user string, limit int) (IndexResult, error) {
	var out IndexResult
	if e.embedder == nil || strings.TrimSpace(e.embedder.Model()) == "" {
		return out, errors.New("no embedder configured")
	}
	if strings.TrimSpace(user) == "" {
		return out, errors.New("user_id required")
	}
	if limit <= 0 {
		limit = 1000
	}
	if limit > MaxRecallObservations {
		return out, ErrObservationCapacity
	}
	observations, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return out, err
	}
	stored, err := e.store.observationEmbeddings(ctx, user, e.embedder.Model())
	if err != nil {
		return out, err
	}
	var pending []Observation
	for _, o := range observations {
		if v, ok := stored[o.ID]; !ok || v.Hash != contentHash(o.Content) {
			pending = append(pending, o)
		}
	}
	if len(pending) > limit {
		out.Incomplete = true
		pending = pending[:limit]
	}
	for start := 0; start < len(pending); start += 32 {
		batch := pending[start:min(start+32, len(pending))]
		texts := make([]string, len(batch))
		for i, o := range batch {
			texts[i] = o.Content
		}
		vectors, err := e.embedder.Embed(ctx, texts)
		if err != nil {
			out.Incomplete = true
			return out, errors.New("observation embedding failed")
		}
		if len(vectors) != len(batch) {
			return out, errors.New("embedding count mismatch")
		}
		for i, v := range vectors {
			if !validVector(v) || len(v) != len(vectors[0]) {
				return out, errors.New("invalid observation embedding")
			}
			o := batch[i]
			tag, err := e.store.pool.Exec(ctx, `INSERT INTO observation_embeddings(observation_id,model,content_hash,embedding)
				SELECT o.id,$3,$4,$5::double precision[] FROM observations o WHERE o.id=$1 AND o.user_id=$2 AND o.content=$6 AND `+eligibleObservation+`
				ON CONFLICT(observation_id,model) DO UPDATE SET content_hash=EXCLUDED.content_hash,embedding=EXCLUDED.embedding,created_at=now()`, o.ID, user, e.embedder.Model(), contentHash(o.Content), v, o.Content)
			if err != nil {
				return out, err
			}
			out.Indexed += int(tag.RowsAffected())
			if tag.RowsAffected() == 0 {
				out.Incomplete = true
			}
		}
	}
	return out, nil
}

// RecallObservations searches original evidence without turning it into a fact.
// It preserves attribution and does not change Recall's memory-only contract.
// The host must evaluate speaker, timestamp and uncertainty before using a hit.
func (e *Engine) RecallObservations(ctx context.Context, user, query string, opts RecallOptions) (ObservationRecallResult, error) {
	out := ObservationRecallResult{Hits: []ObservationHit{}, Mode: "lexical"}
	if strings.TrimSpace(user) == "" || strings.TrimSpace(query) == "" {
		return out, errors.New("user and query required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	limit = min(limit, 200)
	observations, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return out, err
	}
	out.Candidates = len(observations)
	var queryVector []float64
	stored := map[string]storedEmbedding{}
	if e.embedder != nil && !opts.LexicalOnly && len(observations) > 0 {
		out.Mode = "hybrid"
		vectors, err := e.embedder.Embed(ctx, []string{query})
		if err != nil {
			return out, errors.New("query embedding failed; explicitly select lexical-only recall to fall back")
		}
		if len(vectors) != 1 || !validVector(vectors[0]) {
			return out, errors.New("invalid query embedding")
		}
		queryVector = vectors[0]
		stored, err = e.store.observationEmbeddings(ctx, user, e.embedder.Model())
		if err != nil {
			return out, err
		}
	}
	type candidate struct {
		id    string
		score float64
	}
	var lexical, semantic []candidate
	terms := words(query)
	for _, o := range observations {
		if score := overlap(terms, words(o.Content)); score > 0 {
			lexical = append(lexical, candidate{o.ID, score})
		}
		if v, ok := stored[o.ID]; ok && v.Hash == contentHash(o.Content) && len(v.Values) == len(queryVector) {
			out.Indexed++
			if score := cosine(v.Values, queryVector); score > 0 {
				semantic = append(semantic, candidate{o.ID, score})
			}
		}
	}
	if out.Mode == "hybrid" {
		out.Incomplete = out.Indexed < len(observations)
	}
	fused := map[string]float64{}
	for _, channel := range [][]candidate{lexical, semantic} {
		sort.Slice(channel, func(i, j int) bool {
			if channel[i].score != channel[j].score {
				return channel[i].score > channel[j].score
			}
			return channel[i].id < channel[j].id
		})
		for i, c := range channel[:min(len(channel), limit*4)] {
			fused[c.id] += 1 / (60 + float64(i+1))
		}
	}
	// Re-read after provider work: forgetting, expiry and archival must also
	// fence source retrieval and stale source vectors.
	current, err := e.store.retrievalObservations(ctx, user)
	if err != nil {
		return out, err
	}
	original := map[string]string{}
	for _, o := range observations {
		original[o.ID] = o.Content
	}
	for _, o := range current {
		if score, ok := fused[o.ID]; ok && original[o.ID] == o.Content {
			out.Hits = append(out.Hits, ObservationHit{Observation: o, Score: score})
		}
	}
	sort.Slice(out.Hits, func(i, j int) bool {
		if out.Hits[i].Score != out.Hits[j].Score {
			return out.Hits[i].Score > out.Hits[j].Score
		}
		return out.Hits[i].Observation.ID < out.Hits[j].Observation.ID
	})
	if len(out.Hits) > limit {
		out.Hits = out.Hits[:limit]
	}
	return out, nil
}
