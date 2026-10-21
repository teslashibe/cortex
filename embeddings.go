package cortex

import (
	"context"
	"errors"
	"strings"
)

type storedEmbedding struct {
	Hash   string
	Values []float64
}
type IndexResult struct {
	Indexed    int  `json:"indexed"`
	Incomplete bool `json:"incomplete"`
}

func (s *Store) embeddings(ctx context.Context, user, model string) (map[string]storedEmbedding, error) {
	rows, err := s.pool.Query(ctx, `SELECT v.memory_id,v.content_hash,v.embedding FROM memory_embeddings v JOIN memories m ON m.id=v.memory_id WHERE m.user_id=$1 AND v.model=$2 AND m.contradicted_by IS NULL AND m.archived_at IS NULL AND (m.expires_at IS NULL OR m.expires_at>now())`, user, model)
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

// Index refreshes a bounded batch of missing embeddings. Inference occurs
// outside transactions. Publication rechecks content and current status, so a
// concurrent correction or forget cannot publish a stale vector.
func (e *Engine) Index(ctx context.Context, user string, limit int) (IndexResult, error) {
	var out IndexResult
	if e.embedder == nil {
		return out, errors.New("no embedder configured")
	}
	if strings.TrimSpace(user) == "" || strings.TrimSpace(e.embedder.Model()) == "" {
		return out, errors.New("user and embedding model required")
	}
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		return out, errors.New("index limit exceeds 10000")
	}
	memories, err := e.store.retrievalMemories(ctx, user)
	if err != nil {
		return out, err
	}
	stored, err := e.store.embeddings(ctx, user, e.embedder.Model())
	if err != nil {
		return out, err
	}
	var pending []Memory
	for _, m := range memories {
		if v, ok := stored[m.ID]; !ok || v.Hash != contentHash(m.Summary) {
			pending = append(pending, m)
		}
	}
	if len(pending) > limit {
		out.Incomplete = true
		pending = pending[:limit]
	}
	for start := 0; start < len(pending); start += 32 {
		end := min(start+32, len(pending))
		batch := pending[start:end]
		texts := make([]string, len(batch))
		for i, m := range batch {
			texts[i] = m.Summary
		}
		vectors, err := e.embedder.Embed(ctx, texts)
		if err != nil {
			out.Incomplete = true
			return out, errors.New("memory embedding failed")
		}
		if len(vectors) != len(batch) {
			return out, errors.New("embedding count mismatch")
		}
		dimension := 0
		for _, v := range vectors {
			if !validVector(v) {
				return out, errors.New("invalid memory embedding")
			}
			if dimension == 0 {
				dimension = len(v)
			}
			if len(v) != dimension {
				return out, errors.New("embedding dimension mismatch")
			}
		}
		for i, m := range batch {
			tag, err := e.store.pool.Exec(ctx, `INSERT INTO memory_embeddings(memory_id,model,content_hash,embedding)
			SELECT id,$3,$4,$5::double precision[] FROM memories WHERE id=$1 AND user_id=$2 AND summary=$6 AND contradicted_by IS NULL AND archived_at IS NULL AND (expires_at IS NULL OR expires_at>now())
			ON CONFLICT(memory_id,model) DO UPDATE SET content_hash=EXCLUDED.content_hash,embedding=EXCLUDED.embedding,created_at=now()`, m.ID, user, e.embedder.Model(), contentHash(m.Summary), vectors[i], m.Summary)
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

// Evidence returns source observations for one memory in the same user scope.
func (e *Engine) Evidence(ctx context.Context, user, id string) ([]Observation, error) {
	rows, err := e.store.pool.Query(ctx, `SELECT o.id,o.user_id,o.source,o.content,o.refs,o.salience,o.at,COALESCE(o.idempotency_key,'') FROM observations o JOIN memories m ON m.sources ? o.id AND m.user_id=o.user_id WHERE m.user_id=$1 AND m.id=$2 ORDER BY o.at,o.id LIMIT 1001`, user, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Observation{}
	for rows.Next() {
		var o Observation
		if err := rows.Scan(&o.ID, &o.UserID, &o.Source, &o.Content, &o.Refs, &o.Salience, &o.At, &o.IdempotencyKey); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if len(out) > 1000 {
		return nil, errors.New("memory evidence exceeds 1000 observations")
	}
	return out, rows.Err()
}
