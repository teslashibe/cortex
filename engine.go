package cortex

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/teslashibe/cortex/prompts"
)

var ErrNoObservations = errors.New("no unconsolidated observations")

type Engine struct {
	store *Store
	model Model
}

func NewEngine(store *Store, model Model) *Engine { return &Engine{store: store, model: model} }

// Observe writes a batch of raw observations, idempotently within each user.
func (e *Engine) Observe(ctx context.Context, obs []Observation) (int, error) {
	for i := range obs {
		if obs[i].IdempotencyKey == "" {
			obs[i].IdempotencyKey = obs[i].ID
		}
		if obs[i].Salience == 0 {
			obs[i].Salience = 0.5
		}
		if obs[i].At.IsZero() {
			obs[i].At = time.Now().UTC()
		}
	}
	return e.store.WriteObservations(ctx, obs)
}

func (e *Engine) Recall(ctx context.Context, userID, query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 200 {
		limit = 200
	}
	candidates, err := e.store.RecallByText(ctx, userID, query, limit*2)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := make([]Hit, 0, len(candidates))
	for _, m := range candidates {
		out = append(out, Hit{Memory: m, Score: recallScore(query, m, now)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type ConsolidationOptions struct {
	ChunkSize int
	ByDay     bool
}

type ConsolidationResult struct {
	Runs   []Consolidation     `json:"runs"`
	Counts ConsolidationCounts `json:"counts"`
}

// Consolidate processes the pending snapshot sequentially. Successful chunks
// remain committed if a later chunk fails; retries select only pending work.
func (e *Engine) Consolidate(ctx context.Context, userID string, opts ConsolidationOptions) (ConsolidationResult, error) {
	result := ConsolidationResult{Runs: []Consolidation{}}
	if strings.TrimSpace(userID) == "" {
		return result, errors.New("user_id required")
	}
	if e.model == nil {
		return result, errors.New("no model configured")
	}
	if opts.ChunkSize < 0 || opts.ChunkSize > DefaultChunkSize {
		return result, fmt.Errorf("chunk size must be between 1 and %d (or zero for default)", DefaultChunkSize)
	}
	ctx, cancel := context.WithTimeout(ctx, ConsolidationTimeout)
	defer cancel()
	conn, err := e.store.claimUser(ctx, userID)
	if err != nil {
		return result, err
	}
	defer releaseUser(conn, userID)
	observations, err := unconsolidatedObservations(ctx, conn, userID, MaxObservationsPerConsolidation)
	if err != nil {
		return result, err
	}
	if len(observations) == 0 {
		return result, ErrNoObservations
	}
	for _, chunk := range chunkObservations(observations, opts.ChunkSize, opts.ByDay) {
		memories, err := memoriesForUser(ctx, conn, userID)
		if err != nil {
			return result, err
		}
		// Bound the complete serialized context, not only observation count.
		// A large chunk is reduced without truncating any individual observation.
		for len(chunk) > 0 {
			n := len(chunk)
			messages, err := consolidationMessages(e.model.Model(), userID, chunk[:n], memories)
			for err != nil && n > 1 {
				n = (n + 1) / 2
				messages, err = consolidationMessages(e.model.Model(), userID, chunk[:n], memories)
			}
			if err != nil {
				return result, err
			}
			ids := make([]string, n)
			for i, o := range chunk[:n] {
				ids[i] = o.ID
			}
			run, err := createConsolidation(ctx, conn, userID, ids, e.model.Model(), prompts.Version)
			if err != nil {
				return result, fmt.Errorf("create consolidation: %w", err)
			}
			modelCtx, modelCancel := context.WithTimeout(ctx, ModelTimeout)
			response, callErr := e.model.Complete(modelCtx, messages)
			modelCancel()
			var ops []Op
			if callErr == nil {
				ops, callErr = parseOps(response.Text)
			}
			if callErr == nil {
				var completed Consolidation
				completed, callErr = completeConsolidation(ctx, conn, userID, run.ID, ops, response.Usage.TotalTokens)
				if callErr == nil {
					run = completed
				}
			}
			if callErr != nil {
				if failErr := failConsolidation(conn, &run, callErr); failErr != nil {
					callErr = errors.Join(callErr, fmt.Errorf("record failure: %w", failErr))
				}
				result.Runs = append(result.Runs, run)
				return result, fmt.Errorf("consolidation %s: %w", run.ID, callErr)
			}
			result.Runs = append(result.Runs, run)
			result.Counts.ObservationsProcessed += run.Counts.ObservationsProcessed
			result.Counts.MemoriesCreated += run.Counts.MemoriesCreated
			result.Counts.MemoriesReinforced += run.Counts.MemoriesReinforced
			result.Counts.TokensUsed += run.Counts.TokensUsed
			chunk = chunk[n:]
			if len(chunk) > 0 {
				memories, err = memoriesForUser(ctx, conn, userID)
				if err != nil {
					return result, err
				}
			}
		}
	}
	return result, nil
}

func (e *Engine) Profile(ctx context.Context, userID, tier string) (string, error) {
	memories, err := e.store.Memories(ctx, userID)
	if err != nil {
		return "", err
	}
	return renderProfile(userID, tier, memories), nil
}

func (e *Engine) Memories(ctx context.Context, userID string) ([]Memory, error) {
	return e.store.Memories(ctx, userID)
}

func (e *Engine) WriteSignal(ctx context.Context, signal Signal) error {
	if strings.TrimSpace(signal.UserID) == "" {
		return errors.New("user_id required")
	}
	switch signal.SignalType {
	case "retrieved", "used", "ignored", "reinforced", "decayed", "contradicted":
	default:
		return fmt.Errorf("invalid signal type %q", signal.SignalType)
	}
	return e.store.WriteSignal(ctx, signal)
}
