package cortex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/teslashibe/cortex/llm"
	"github.com/teslashibe/cortex/prompts"
)

// ConsolidationSelection is an exact allowlist supplied by an authenticated host.
// Empty MemoryIDs means no existing memory context, never every memory.
type ConsolidationSelection struct {
	ObservationIDs []string
	MemoryIDs      []string
}

// ConsolidationReview identifies the frozen input without exposing source text.
// Phase is either "inference" or "commit". PromptHash covers the model and the
// exact serialized messages passed to Model.Complete.
type ConsolidationReview struct {
	RunID          string
	UserID         string
	Phase          string
	ObservationIDs []string
	MemoryIDs      []string
	PromptHash     string
}

// ConsolidationGuard must check the host's current source permissions and lock
// its policy rows until tx ends. It must not commit, roll back, or retain tx.
// The commit check and memory changes share one PostgreSQL transaction; host
// policy tables must be reachable from this database connection.
type ConsolidationGuard func(context.Context, pgx.Tx, ConsolidationReview) error

// ConsolidationPromptHash lets a host verify a queued inference payload against
// the exact model/messages approved by its guard.
func ConsolidationPromptHash(model string, messages []llm.Message) (string, error) {
	encoded, err := json.Marshal(struct {
		Model    string        `json:"model"`
		Messages []llm.Message `json:"messages"`
	}{model, messages})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// ConsolidateSelected makes one model call for at most 200 observations and 500
// explicitly selected memories. It never splits, truncates, repairs, or retries
// provider work. Rejected/changed sources leave all observations pending.
// Guard is required; this library does not authenticate the supplied namespace.
func (e *Engine) ConsolidateSelected(ctx context.Context, userID string, selection ConsolidationSelection, guard ConsolidationGuard) (ConsolidationResult, error) {
	result := ConsolidationResult{Runs: []Consolidation{}}
	if strings.TrimSpace(userID) == "" || e.model == nil || guard == nil || !validSelection(selection) {
		return result, errors.New("selected consolidation requires a model, namespace, guard, and bounded unique IDs")
	}
	selection.ObservationIDs = append([]string(nil), selection.ObservationIDs...)
	selection.MemoryIDs = append([]string(nil), selection.MemoryIDs...)
	ctx, cancel := context.WithTimeout(ctx, ConsolidationTimeout)
	defer cancel()
	conn, err := e.store.claimUser(ctx, userID)
	if err != nil {
		return result, err
	}
	defer func() {
		if conn != nil {
			releaseUser(conn, userID)
		}
	}()
	observations, memories, err := readSelection(ctx, conn, userID, selection, false)
	if err != nil {
		return result, err
	}
	model := e.model.Model()
	messages, err := consolidationMessages(model, userID, observations, memories)
	if err != nil {
		return result, err
	}
	promptHash, err := ConsolidationPromptHash(model, messages)
	if err != nil {
		return result, err
	}
	ids := make([]string, len(observations))
	for i, observation := range observations {
		ids[i] = observation.ID
	}
	run, err := createConsolidation(ctx, conn, userID, ids, model, prompts.Version)
	if err != nil {
		return result, err
	}
	review := ConsolidationReview{RunID: run.ID, UserID: userID, ObservationIDs: ids, MemoryIDs: selection.MemoryIDs, PromptHash: promptHash}
	check := func(ctx context.Context, tx pgx.Tx, phase string) error {
		currentObservations, currentMemories, err := readSelection(ctx, tx, userID, selection, true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(currentObservations, observations) || !reflect.DeepEqual(currentMemories, memories) {
			return errors.New("selected consolidation input changed")
		}
		value := review
		value.Phase = phase
		value.ObservationIDs = append([]string(nil), review.ObservationIDs...)
		value.MemoryIDs = append([]string(nil), review.MemoryIDs...)
		return guard(ctx, tx, value)
	}
	var unreferenced []string
	work := func() error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if err = check(ctx, tx, "inference"); err != nil {
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		modelCtx, modelCancel := context.WithTimeout(ctx, ModelTimeout)
		response, err := e.model.Complete(modelCtx, messages)
		modelCancel()
		if err != nil {
			return err
		}
		if len(response.Text) > 4<<20 {
			return errors.New("selected consolidation response exceeds 4 MiB")
		}
		ops, err := parseOps(response.Text)
		if err == nil {
			ops, err = resolveConsolidationAliases(ops, observations, memories)
		}
		if err != nil {
			return err
		}
		referenced := map[string]bool{}
		for _, op := range ops {
			for _, id := range op.Sources {
				referenced[id] = true
			}
		}
		for _, id := range ids {
			if !referenced[id] {
				unreferenced = append(unreferenced, id)
			}
		}
		completed, err := completeGuardedConsolidation(ctx, conn, userID, run.ID, ops, response.Usage.TotalTokens, response.Model,
			func(ctx context.Context, tx pgx.Tx) error { return check(ctx, tx, "commit") })
		if err != nil {
			return err
		}
		run = completed
		return nil
	}()
	if work != nil {
		// Cancellation may close the inference connection. Reconcile on a new
		// connection without retaining a namespace lock or overwriting a commit.
		releaseUser(conn, userID)
		conn = nil
		recovery, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, failed := e.store.pool.Exec(recovery, `UPDATE consolidations SET status='failed',completed_at=now(),error_message=$3 WHERE id=$1 AND user_id=$2 AND status='running'`, run.ID, userID, work.Error())
		if failed == nil {
			var saved Consolidation
			saved, failed = e.store.Consolidation(recovery, userID, run.ID)
			if failed == nil {
				run = saved
			}
		}
		if run.Status != "completed" {
			result.Runs = append(result.Runs, run)
			return result, errors.Join(work, failed)
		}
	}
	result.Runs = append(result.Runs, run)
	result.Counts = run.Counts
	result.UnreferencedObservationIDs = unreferenced
	return result, nil
}

// Consolidation reads one retained outcome in its trusted host-derived scope.
// A host can reconcile an uncertain response without invoking the model again.
func (s *Store) Consolidation(ctx context.Context, userID, id string) (Consolidation, error) {
	var run Consolidation
	if strings.TrimSpace(userID) == "" || id == "" || len(id) > 256 {
		return run, errors.New("consolidation not found")
	}
	err := s.pool.QueryRow(ctx, `SELECT id,user_id,status,observation_ids,model,prompt_version,started_at,completed_at,observations_processed,memories_created,memories_reinforced,tokens_used,COALESCE(error_message,''),resolved_model FROM consolidations WHERE id=$1 AND user_id=$2`, id, userID).Scan(&run.ID, &run.UserID, &run.Status, &run.ObservationIDs, &run.Model, &run.PromptVersion, &run.StartedAt, &run.CompletedAt, &run.Counts.ObservationsProcessed, &run.Counts.MemoriesCreated, &run.Counts.MemoriesReinforced, &run.Counts.TokensUsed, &run.ErrorMessage, &run.ResolvedModel)
	if errors.Is(err, pgx.ErrNoRows) {
		return run, errors.New("consolidation not found")
	}
	return run, err
}

func validSelection(value ConsolidationSelection) bool {
	if len(value.ObservationIDs) == 0 || len(value.ObservationIDs) > DefaultChunkSize || len(value.MemoryIDs) > 500 {
		return false
	}
	for _, ids := range [][]string{value.ObservationIDs, value.MemoryIDs} {
		seen := map[string]bool{}
		for _, id := range ids {
			if strings.TrimSpace(id) == "" || len(id) > 256 || seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	return true
}

type selectionReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func readSelection(ctx context.Context, reader selectionReader, userID string, selection ConsolidationSelection, lock bool) ([]Observation, []Memory, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	rows, err := reader.Query(ctx, `SELECT id,user_id,source,content,refs,salience,at,idempotency_key,COALESCE(tool_name,''),COALESCE(intent_id,''),COALESCE(outcome_class,''),turn_index FROM observations WHERE user_id=$1 AND id=ANY($2) AND consolidated_at IS NULL ORDER BY at,id`+suffix, userID, selection.ObservationIDs)
	if err != nil {
		return nil, nil, err
	}
	observations, err := scanObservations(rows)
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(observations) != len(selection.ObservationIDs) {
		return nil, nil, errors.New("selected observations are missing, foreign, or already processed")
	}
	rows, err = reader.Query(ctx, `SELECT `+memoryColumns+currentMemories+` AND id=ANY($2) ORDER BY id`+suffix, userID, selection.MemoryIDs)
	if err != nil {
		return nil, nil, err
	}
	memories, err := scanMemories(rows)
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(memories) != len(selection.MemoryIDs) {
		return nil, nil, errors.New("selected memories are missing, foreign, or inactive")
	}
	return observations, memories, nil
}
