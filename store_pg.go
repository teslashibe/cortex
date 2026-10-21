package cortex

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pg connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) ApplyMigrations(ctx context.Context, fsys fs.FS) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// Serialize startup migrations without leaving a session lock in the pool.
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(214013, 1)`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("ensure schema_version: %w", err)
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var v int
		if _, err := fmt.Sscanf(name, "%03d_", &v); err != nil {
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_version WHERE version=$1)`, v).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %d: %w", v, err)
		}
		if exists {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_version (version) VALUES ($1)`, v); err != nil {
			return fmt.Errorf("record %s: %w", name, err)
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) WriteObservations(ctx context.Context, obs []Observation) (int, error) {
	if len(obs) == 0 {
		return 0, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin observations: %w", err)
	}
	defer tx.Rollback(ctx)
	// Imports share a short barrier with forgetting, separate from inference's
	// session lock. Ordered acquisition permits a batch spanning several users.
	users := map[string]bool{}
	for _, o := range obs {
		users[o.UserID] = true
	}
	ordered := make([]string, 0, len(users))
	for user := range users {
		ordered = append(ordered, user)
	}
	sort.Strings(ordered)
	for _, user := range ordered {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(214015,hashtext($1))`, user); err != nil {
			return 0, err
		}
	}
	inserted := 0
	for _, o := range obs {
		if o.ID == "" || o.UserID == "" || strings.TrimSpace(o.Content) == "" {
			return 0, fmt.Errorf("observation id, user_id, and content required")
		}
		var forgotten bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM forgotten_observations
			WHERE user_id=$1 AND ((kind='content' AND fingerprint=$2) OR (kind='idempotency' AND fingerprint=$3)))`,
			o.UserID, contentHash(o.Content), contentHash(o.IdempotencyKey)).Scan(&forgotten); err != nil {
			return 0, err
		}
		if forgotten {
			continue
		}
		refs, err := json.Marshal(o.Refs)
		if err != nil {
			return 0, fmt.Errorf("marshal refs %s: %w", o.ID, err)
		}
		ct, err := tx.Exec(ctx, `INSERT INTO observations (id, user_id, source, content, refs, salience, at, idempotency_key,tool_name,intent_id,outcome_class,turn_index)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),$12) ON CONFLICT DO NOTHING`,
			o.ID, o.UserID, o.Source, o.Content, refs, o.Salience, o.At, o.IdempotencyKey, o.ToolName, o.IntentID, o.OutcomeClass, o.TurnIndex)
		if err != nil {
			return 0, fmt.Errorf("insert observation %s: %w", o.ID, err)
		}
		if ct.RowsAffected() == 0 {
			var ownDuplicate bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM observations WHERE user_id=$1 AND (id=$2 OR (idempotency_key=$3 AND $3<>'')))`, o.UserID, o.ID, o.IdempotencyKey).Scan(&ownDuplicate); err != nil {
				return 0, err
			}
			if !ownDuplicate {
				return 0, fmt.Errorf("observation %s conflicts with another user's ID", o.ID)
			}
		}
		inserted += int(ct.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit observations: %w", err)
	}
	return inserted, nil
}

func unconsolidatedObservations(ctx context.Context, conn *pgxpool.Conn, userID string, limit int) ([]Observation, error) {
	rows, err := conn.Query(ctx, `SELECT id, user_id, source, content, refs, salience, at, idempotency_key,
		COALESCE(tool_name,''),COALESCE(intent_id,''),COALESCE(outcome_class,''),turn_index
		FROM observations WHERE user_id=$1 AND consolidated_at IS NULL ORDER BY at ASC, id ASC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("query observations: %w", err)
	}
	defer rows.Close()
	var out []Observation
	for rows.Next() {
		var o Observation
		var refs []byte
		var key *string
		if err := rows.Scan(&o.ID, &o.UserID, &o.Source, &o.Content, &refs, &o.Salience, &o.At, &key, &o.ToolName, &o.IntentID, &o.OutcomeClass, &o.TurnIndex); err != nil {
			return nil, err
		}
		if key != nil {
			o.IdempotencyKey = *key
		}
		if err := json.Unmarshal(refs, &o.Refs); err != nil {
			return nil, fmt.Errorf("observation %s refs: %w", o.ID, err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

const memoryColumns = `id, user_id, kind, summary, confidence, reinforced_count, born_at, last_seen_at,
	COALESCE(contradicted_by,''), expires_at, sources, archived_at, pinned, utility, outcome_count`
const currentMemories = ` FROM memories WHERE user_id=$1 AND contradicted_by IS NULL AND archived_at IS NULL AND (expires_at IS NULL OR expires_at > now())`

func (s *Store) Memories(ctx context.Context, userID string) ([]Memory, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	return memoriesForUser(ctx, conn, userID)
}

func memoriesForUser(ctx context.Context, conn *pgxpool.Conn, userID string) ([]Memory, error) {
	rows, err := conn.Query(ctx, `SELECT `+memoryColumns+currentMemories+` ORDER BY confidence DESC, last_seen_at DESC, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func (s *Store) retrievalMemories(ctx context.Context, user string) ([]Memory, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+memoryColumns+currentMemories+` ORDER BY id LIMIT $2`, user, MaxRecallMemories+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	memories, err := scanMemories(rows)
	if len(memories) > MaxRecallMemories {
		return nil, ErrRecallCapacity
	}
	return memories, err
}

func consolidationMemories(ctx context.Context, conn *pgxpool.Conn, user string) ([]Memory, error) {
	rows, err := conn.Query(ctx, `SELECT `+memoryColumns+currentMemories+` ORDER BY id LIMIT $2`, user, MaxRecallMemories+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	memories, err := scanMemories(rows)
	if len(memories) > MaxRecallMemories {
		return nil, ErrRecallCapacity
	}
	return memories, err
}

func (s *Store) RecallByText(ctx context.Context, userID, query string, limit int) ([]Memory, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+memoryColumns+currentMemories+`
		AND (summary ILIKE '%'||$2||'%' OR similarity(summary, $2) > 0.2)
		ORDER BY similarity(summary, $2) DESC, confidence DESC, last_seen_at DESC, id LIMIT $3`, userID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("recall: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func scanMemories(rows pgx.Rows) ([]Memory, error) {
	out := make([]Memory, 0)
	for rows.Next() {
		var m Memory
		var srcRaw []byte
		if err := rows.Scan(&m.ID, &m.UserID, &m.Kind, &m.Summary, &m.Confidence, &m.ReinforcedCount,
			&m.BornAt, &m.LastSeenAt, &m.ContradictedBy, &m.ExpiresAt, &srcRaw, &m.ArchivedAt, &m.Pinned, &m.Utility, &m.OutcomeCount); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		if err := json.Unmarshal(srcRaw, &m.Sources); err != nil {
			return nil, fmt.Errorf("memory %s sources: %w", m.ID, err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

var ErrConsolidationBusy = errors.New("consolidation already running for user")

// A dedicated session lock fences work across processes. Network calls hold the
// connection, never a transaction. A lost connection automatically releases it.
func (s *Store) claimUser(ctx context.Context, userID string) (*pgxpool.Conn, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(214014, hashtext($1))`, userID).Scan(&locked); err != nil {
		conn.Release()
		return nil, err
	}
	if !locked {
		conn.Release()
		return nil, ErrConsolidationBusy
	}
	// Any prior running row now belongs to a dead/disconnected worker. Its
	// finish transaction cannot commit without the original locked connection.
	if _, err := conn.Exec(ctx, `UPDATE consolidations SET status='failed', completed_at=now(), error_message='Interrupted before atomic completion; observations remain pending' WHERE user_id=$1 AND status='running'`, userID); err != nil {
		releaseUser(conn, userID)
		return nil, fmt.Errorf("recover consolidation: %w", err)
	}
	return conn, nil
}

func releaseUser(conn *pgxpool.Conn, userID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unlocked bool
	if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(214014, hashtext($1))`, userID).Scan(&unlocked); err != nil || !unlocked {
		// Never return a possibly locked session to the pool.
		_ = conn.Conn().Close(ctx)
	}
	conn.Release()
}

func createConsolidation(ctx context.Context, conn *pgxpool.Conn, userID string, ids []string, model, version string) (Consolidation, error) {
	run := Consolidation{ID: "con_" + rand.Text(), UserID: userID, Status: "running", ObservationIDs: ids, Model: model, PromptVersion: version}
	err := conn.QueryRow(ctx, `INSERT INTO consolidations (id,user_id,status,observation_ids,model,prompt_version)
		VALUES ($1,$2,'running',$3,$4,$5) RETURNING started_at`, run.ID, userID, ids, model, version).Scan(&run.StartedAt)
	return run, err
}

func failConsolidation(conn *pgxpool.Conn, run *Consolidation, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := conn.QueryRow(ctx, `UPDATE consolidations SET status='failed', completed_at=now(), error_message=$3
		WHERE id=$1 AND user_id=$2 AND status='running' RETURNING completed_at`, run.ID, run.UserID, cause.Error()).Scan(&run.CompletedAt)
	if err == nil {
		run.Status = "failed"
		run.ErrorMessage = cause.Error()
	}
	return err
}

// completeConsolidation re-reads trusted run IDs under row lock. Applying every
// op, acknowledging exactly those IDs, and completing the run are one commit.
// It must use the same dedicated connection that claimed the user.
func completeConsolidation(ctx context.Context, conn *pgxpool.Conn, userID, runID string, ops []Op, tokens int) (Consolidation, error) {
	return completeConsolidationWithModel(ctx, conn, userID, runID, ops, tokens, "")
}

func completeConsolidationWithModel(ctx context.Context, conn *pgxpool.Conn, userID, runID string, ops []Op, tokens int, resolvedModel string) (Consolidation, error) {
	var run Consolidation
	tx, err := conn.Begin(ctx)
	if err != nil {
		return run, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT id,user_id,status,observation_ids,model,prompt_version,started_at,completed_at,
		observations_processed,memories_created,memories_reinforced,tokens_used,COALESCE(error_message,''),resolved_model
		FROM consolidations WHERE id=$1 AND user_id=$2 FOR UPDATE`, runID, userID).Scan(
		&run.ID, &run.UserID, &run.Status, &run.ObservationIDs, &run.Model, &run.PromptVersion, &run.StartedAt, &run.CompletedAt,
		&run.Counts.ObservationsProcessed, &run.Counts.MemoriesCreated, &run.Counts.MemoriesReinforced, &run.Counts.TokensUsed, &run.ErrorMessage, &run.ResolvedModel)
	if err != nil {
		return run, fmt.Errorf("load consolidation: %w", err)
	}
	if run.Status == "completed" {
		return run, nil
	}
	if run.Status != "running" {
		return run, fmt.Errorf("consolidation is %s", run.Status)
	}
	if tokens < 0 {
		return run, fmt.Errorf("negative token count")
	}
	sources := make(map[string]bool, len(run.ObservationIDs))
	for _, id := range run.ObservationIDs {
		if id == "" || sources[id] {
			return run, fmt.Errorf("invalid persisted observation IDs")
		}
		sources[id] = true
	}
	var pending int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM observations WHERE user_id=$1 AND id=ANY($2) AND consolidated_at IS NULL`, userID, run.ObservationIDs).Scan(&pending); err != nil {
		return run, err
	}
	if pending != len(run.ObservationIDs) {
		return run, fmt.Errorf("run references missing, foreign, or already processed observations")
	}
	mutated := map[string]bool{}
	for i, op := range ops {
		if err := validateOp(op); err != nil {
			return run, fmt.Errorf("operation %d: %w", i, err)
		}
		for _, id := range op.Sources {
			if !sources[id] {
				return run, fmt.Errorf("operation %d: source %q is outside this chunk", i, id)
			}
		}
		refs := []string{}
		if op.OldID != "" {
			refs = append(refs, op.OldID)
		}
		for _, id := range refs {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memories WHERE id=$1 AND user_id=$2 AND contradicted_by IS NULL AND archived_at IS NULL AND (expires_at IS NULL OR expires_at > now()))`, id, userID).Scan(&exists); err != nil {
				return run, err
			}
			if !exists {
				return run, fmt.Errorf("operation %d: unknown or inactive memory %q for user", i, id)
			}
		}
		if op.OldID != "" {
			if mutated[op.OldID] {
				return run, fmt.Errorf("multiple fact mutations reference memory %q", op.OldID)
			}
			mutated[op.OldID] = true
		}
	}
	now := time.Now().UTC()
	for _, op := range ops {
		var evidenceAt time.Time
		if err := tx.QueryRow(ctx, `SELECT max(at) FROM observations WHERE user_id=$1 AND id=ANY($2)`, userID, op.Sources).Scan(&evidenceAt); err != nil {
			return run, err
		}
		if err := applyOpTx(ctx, tx, userID, op, now, evidenceAt, &run.Counts); err != nil {
			return run, err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE observations SET consolidated_at=$3 WHERE user_id=$1 AND id=ANY($2) AND consolidated_at IS NULL`, userID, run.ObservationIDs, now)
	if err != nil {
		return run, err
	}
	if tag.RowsAffected() != int64(len(run.ObservationIDs)) {
		return run, fmt.Errorf("observation acknowledgement count changed")
	}
	run.Counts.ObservationsProcessed = len(run.ObservationIDs)
	run.Counts.TokensUsed = tokens
	if _, err := tx.Exec(ctx, `UPDATE consolidations SET status='completed', completed_at=$3, observations_processed=$4,
		memories_created=$5, memories_reinforced=$6, tokens_used=$7, resolved_model=$8 WHERE id=$1 AND user_id=$2`,
		run.ID, userID, now, run.Counts.ObservationsProcessed, run.Counts.MemoriesCreated, run.Counts.MemoriesReinforced, tokens, resolvedModel); err != nil {
		return run, err
	}
	if err := tx.Commit(ctx); err != nil {
		return run, fmt.Errorf("commit consolidation: %w", err)
	}
	run.Status = "completed"
	run.CompletedAt = &now
	run.ResolvedModel = resolvedModel
	return run, nil
}

func applyOpTx(ctx context.Context, tx pgx.Tx, userID string, op Op, now, evidenceAt time.Time, c *ConsolidationCounts) error {
	switch op.Kind {
	case OpInsert, OpContradict:
		id := "mem_" + rand.Text()
		sources, err := json.Marshal(op.Sources)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO memories (id,user_id,kind,summary,confidence,reinforced_count,born_at,last_seen_at,sources)
			VALUES ($1,$2,$3,$4,$5,1,$6,$7,$8)`, id, userID, op.Memory.Kind, op.Memory.Summary, op.Memory.Confidence, now, evidenceAt, sources); err != nil {
			return fmt.Errorf("insert memory: %w", err)
		}
		if op.Kind == OpContradict {
			tag, err := tx.Exec(ctx, `UPDATE memories SET contradicted_by=$3 WHERE id=$1 AND user_id=$2 AND contradicted_by IS NULL`, op.OldID, userID, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("contradicted memory no longer current")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO memory_edges (source_id,target_id,edge_type,weight) VALUES ($1,$2,'contradicts',1)`, id, op.OldID); err != nil {
				return err
			}
		}
		c.MemoriesCreated++
	case OpReinforce:
		sources, err := json.Marshal(op.Sources)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE memories SET reinforced_count=reinforced_count+1,last_seen_at=GREATEST(last_seen_at,$3),
			sources=(SELECT COALESCE(jsonb_agg(value ORDER BY value),'[]'::jsonb) FROM (SELECT DISTINCT value FROM jsonb_array_elements_text(sources || $4::jsonb)) merged)
			WHERE id=$1 AND user_id=$2 AND contradicted_by IS NULL AND archived_at IS NULL`, op.OldID, userID, evidenceAt, sources)
		if err != nil {
			return fmt.Errorf("reinforce: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("reinforced memory no longer current")
		}
		c.MemoriesReinforced++
	}
	return nil
}

func (s *Store) WriteSignal(ctx context.Context, sig Signal) error {
	if sig.ID == "" || sig.UserID == "" {
		return fmt.Errorf("signal id and user_id required")
	}
	meta, err := json.Marshal(sig.Metadata)
	if err != nil {
		return fmt.Errorf("marshal signal metadata: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO training_events (id,user_id,memory_id,signal_type,signal_value,metadata)
		SELECT $1,$2,NULLIF($3,''),$4,$5,$6 WHERE $3='' OR EXISTS(SELECT 1 FROM memories WHERE id=$3 AND user_id=$2)
		ON CONFLICT (id) DO NOTHING`, sig.ID, sig.UserID, sig.MemoryID, sig.SignalType, sig.SignalValue, meta)
	if err != nil {
		return fmt.Errorf("write signal: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("signal rejected: unknown memory for user or duplicate signal ID")
	}
	return nil
}
