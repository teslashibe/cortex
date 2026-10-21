package cortex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrMemoryNotFound = errors.New("memory not found for user")

// RetentionPolicy controls relevance, not the truth of the underlying evidence.
// A zero HalfLife disables time decay. Maintenance is always an explicit call.
type RetentionPolicy struct {
	HalfLife     time.Duration `json:"half_life"`
	ArchiveBelow float64       `json:"archive_below"`
	MinAge       time.Duration `json:"min_age"`
}

func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{HalfLife: 90 * 24 * time.Hour, ArchiveBelow: 0.05, MinAge: 30 * 24 * time.Hour}
}

func (p RetentionPolicy) validate() error {
	if p.HalfLife < 0 || p.MinAge < 0 || math.IsNaN(p.ArchiveBelow) || math.IsInf(p.ArchiveBelow, 0) || p.ArchiveBelow < 0 || p.ArchiveBelow > 1 {
		return errors.New("invalid retention policy")
	}
	return nil
}

// Activation is a deterministic relevance multiplier. Reading never changes it.
func Activation(m Memory, at time.Time, p RetentionPolicy) float64 {
	if m.Pinned || p.HalfLife <= 0 {
		return 1
	}
	age := math.Max(at.Sub(m.LastSeenAt).Seconds(), 0)
	strength := 1 + math.Log1p(math.Max(float64(m.ReinforcedCount-1), 0))
	return math.Exp2(-age / (p.HalfLife.Seconds() * strength))
}

type Retention struct {
	Pinned    bool       `json:"pinned"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Restore   bool       `json:"restore"`
}

func (e *Engine) SetRetention(ctx context.Context, user, id string, r Retention) error {
	if strings.TrimSpace(user) == "" || id == "" {
		return ErrMemoryNotFound
	}
	conn, err := e.store.claimUser(ctx, user)
	if err != nil {
		return err
	}
	defer releaseUser(conn, user)
	tag, err := conn.Exec(ctx, `UPDATE memories SET pinned=$3,expires_at=$4,
		archived_at=CASE WHEN $5 THEN NULL ELSE archived_at END
		WHERE user_id=$1 AND id=$2 AND contradicted_by IS NULL`, user, id, r.Pinned, r.ExpiresAt, r.Restore)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrMemoryNotFound
	}
	return nil
}

type MaintenanceResult struct {
	Examined int  `json:"examined"`
	Archived int  `json:"archived"`
	More     bool `json:"more"`
}

// Maintain archives at most limit eligible memories. Expiry takes precedence
// over pinning. No raw evidence is deleted by relevance-based maintenance.
func (e *Engine) Maintain(ctx context.Context, user string, at time.Time, limit int) (MaintenanceResult, error) {
	var out MaintenanceResult
	if strings.TrimSpace(user) == "" {
		return out, errors.New("user_id required")
	}
	if err := e.policy.validate(); err != nil {
		return out, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		return out, errors.New("maintenance limit exceeds 10000")
	}
	conn, err := e.store.claimUser(ctx, user)
	if err != nil {
		return out, err
	}
	defer releaseUser(conn, user)
	// Selection and transition use the same explicit as-of instant. A stable
	// formula makes repeated calls idempotent and independent of tick frequency.
	rows, err := conn.Query(ctx, `SELECT `+memoryColumns+` FROM memories WHERE user_id=$1
		AND contradicted_by IS NULL AND archived_at IS NULL AND (
		(expires_at IS NOT NULL AND expires_at <= $2) OR
		(NOT pinned AND $3::double precision > 0 AND $4::double precision > 0
		AND last_seen_at <= $2 - $5::double precision * interval '1 second'
		AND power(2::double precision,-GREATEST(extract(epoch FROM ($2-last_seen_at)),0)/
		(NULLIF($3::double precision,0)*(1+ln(GREATEST(reinforced_count,1))))) < $4))
		ORDER BY last_seen_at,id LIMIT $6`, user, at, e.policy.HalfLife.Seconds(), e.policy.ArchiveBelow, e.policy.MinAge.Seconds(), limit+1)
	if err != nil {
		return out, err
	}
	memories, err := scanMemories(rows)
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(memories) > limit {
		out.More = true
		memories = memories[:limit]
	}
	out.Examined = len(memories)
	ids := make([]string, 0, len(memories))
	for _, m := range memories {
		ids = append(ids, m.ID)
	}
	if len(ids) > 0 {
		tag, err := conn.Exec(ctx, `UPDATE memories SET archived_at=$3 WHERE user_id=$1 AND id=ANY($2) AND archived_at IS NULL`, user, ids, at)
		if err != nil {
			return out, err
		}
		out.Archived = int(tag.RowsAffected())
	}
	return out, nil
}

func contentHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

type ForgetResult struct {
	Memories     int `json:"memories"`
	Observations int `json:"observations"`
}

// Forget removes a memory, connected versions and memories sharing its evidence.
// Whole source observations are removed, so a multi-fact source can remove more
// than one memory. One-way import tombstones prevent exact evidence replay.
func (e *Engine) Forget(ctx context.Context, user, id string) (ForgetResult, error) {
	var out ForgetResult
	if user == "" || id == "" {
		return out, ErrMemoryNotFound
	}
	conn, err := e.store.claimUser(ctx, user)
	if err != nil {
		return out, err
	}
	defer releaseUser(conn, user)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(214015,hashtext($1))`, user); err != nil {
		return out, err
	}
	type sourceObservation struct{ id, content, key string }
	allSources := map[string]sourceObservation{}
	byContent := map[string][]string{}
	rows, err := tx.Query(ctx, `SELECT id,content,COALESCE(idempotency_key,'') FROM observations WHERE user_id=$1 FOR UPDATE`, user)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var o sourceObservation
		if err = rows.Scan(&o.id, &o.content, &o.key); err != nil {
			rows.Close()
			return out, err
		}
		allSources[o.id] = o
		h := contentHash(o.content)
		byContent[h] = append(byContent[h], o.id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, `SELECT `+memoryColumns+` FROM memories WHERE user_id=$1 FOR UPDATE`, user)
	if err != nil {
		return out, err
	}
	memories, err := scanMemories(rows)
	rows.Close()
	if err != nil {
		return out, err
	}
	byID := map[string]Memory{}
	bySource := map[string][]string{}
	for _, m := range memories {
		byID[m.ID] = m
		for _, s := range m.Sources {
			bySource[s] = append(bySource[s], m.ID)
		}
	}
	if _, ok := byID[id]; !ok {
		var done bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM forgotten_memories WHERE user_id=$1 AND memory_id=$2)`, user, id).Scan(&done)
		if err != nil {
			return out, err
		}
		if done {
			return out, nil
		}
		return out, ErrMemoryNotFound
	}
	edges := map[string][]string{}
	rows, err = tx.Query(ctx, `SELECT e.source_id,e.target_id FROM memory_edges e JOIN memories a ON a.id=e.source_id JOIN memories b ON b.id=e.target_id WHERE a.user_id=$1 AND b.user_id=$1`, user)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a, b string
		if err = rows.Scan(&a, &b); err != nil {
			rows.Close()
			return out, err
		}
		edges[a] = append(edges[a], b)
		edges[b] = append(edges[b], a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	sources := map[string]bool{}
	queue := []string{id}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		seen[current] = true
		queue = append(queue, edges[current]...)
		for _, s := range byID[current].Sources {
			sources[s] = true
			queue = append(queue, bySource[s]...)
			if o, ok := allSources[s]; ok {
				for _, equal := range byContent[contentHash(o.content)] {
					sources[equal] = true
					queue = append(queue, bySource[equal]...)
				}
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sourceIDs := make([]string, 0, len(sources))
	for id := range sources {
		sourceIDs = append(sourceIDs, id)
	}
	type forgotten struct{ content, key string }
	var tombstones []forgotten
	for _, id := range sourceIDs {
		if o, ok := allSources[id]; ok {
			tombstones = append(tombstones, forgotten{o.content, o.key})
		}
	}
	for _, f := range tombstones {
		for kind, value := range map[string]string{"content": f.content, "idempotency": f.key} {
			if value == "" {
				continue
			}
			if _, err = tx.Exec(ctx, `INSERT INTO forgotten_observations(user_id,kind,fingerprint) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, user, kind, contentHash(value)); err != nil {
				return out, err
			}
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO forgotten_memories(user_id,memory_id) SELECT $1,unnest($2::text[]) ON CONFLICT DO NOTHING`, user, ids); err != nil {
		return out, err
	}
	for _, q := range []string{`DELETE FROM memory_edges WHERE source_id=ANY($1) OR target_id=ANY($1)`, `DELETE FROM training_events WHERE user_id=$2 AND memory_id=ANY($1)`, `DELETE FROM memories WHERE user_id=$2 AND id=ANY($1)`} {
		if strings.Contains(q, "$2") {
			_, err = tx.Exec(ctx, q, ids, user)
		} else {
			_, err = tx.Exec(ctx, q, ids)
		}
		if err != nil {
			return out, err
		}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM observations WHERE user_id=$1 AND id=ANY($2)`, user, sourceIDs)
	if err != nil {
		return out, err
	}
	out.Observations = int(tag.RowsAffected())
	out.Memories = len(ids)
	return out, tx.Commit(ctx)
}

type Feedback struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	MemoryID    string `json:"memory_id"`
	RetrievalID string `json:"retrieval_id"`
	Used        bool   `json:"used"`
	Success     *bool  `json:"success,omitempty"`
}

// Feedback records an explicit application outcome. A shown/unused result or
// an unknown outcome changes no utility. An identical event ID is a safe retry.
func (e *Engine) Feedback(ctx context.Context, f Feedback) error {
	if f.ID == "" || f.UserID == "" || f.MemoryID == "" || f.RetrievalID == "" {
		return errors.New("feedback id, user_id, memory_id and retrieval_id required")
	}
	meta, _ := json.Marshal(f)
	eventID := "feedback_" + contentHash(f.UserID+"\x00"+f.ID)
	signalType := "retrieved"
	signalValue := 0.0
	if f.Used {
		signalType = "used"
		if f.Success != nil && *f.Success {
			signalValue = 1
		}
	}
	tx, err := e.store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Taking the memory row first serializes feedback with forgetting.
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM memories WHERE id=$1 AND user_id=$2 FOR UPDATE`, f.MemoryID, f.UserID).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return ErrMemoryNotFound
	} else if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO training_events(id,user_id,memory_id,signal_type,signal_value,metadata) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`, eventID, f.UserID, f.MemoryID, signalType, signalValue, meta)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var same bool
		if err = tx.QueryRow(ctx, `SELECT user_id=$2 AND memory_id=$3 AND metadata=$4::jsonb FROM training_events WHERE id=$1`, eventID, f.UserID, f.MemoryID, meta).Scan(&same); err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("feedback ID already used for a different event")
		}
		return nil
	}
	if f.Used && f.Success != nil {
		reward := 0.0
		if *f.Success {
			reward = 1
		}
		if _, err = tx.Exec(ctx, `UPDATE memories SET utility=(utility*(outcome_count+2)+$3)/(outcome_count+3),outcome_count=outcome_count+1 WHERE id=$1 AND user_id=$2`, f.MemoryID, f.UserID, reward); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
