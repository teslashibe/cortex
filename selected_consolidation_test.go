package cortex

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/teslashibe/cortex/llm"
)

func allowSelected(context.Context, pgx.Tx, ConsolidationReview) error { return nil }

func TestConsolidateSelectedExactInputAndProvenance(t *testing.T) {
	s := testStore(t)
	seedObservations(t, s, "owner", "chosen", "not-selected")
	seedObservations(t, s, "foreign", "foreign-observation")
	seedMemory(t, s, "owner", "chosen-memory")
	seedMemory(t, s, "owner", "other-memory")
	if _, err := s.pool.Exec(t.Context(), `UPDATE memories SET summary=id`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	model := &testModel{complete: func(_ context.Context, messages []llm.Message) (llm.Response, error) {
		calls++
		text := messages[1].Content
		if strings.Contains(text, "not-selected") || strings.Contains(text, "other-memory") || strings.Contains(text, "foreign-observation") || !strings.Contains(text, "chosen-memory") {
			t.Fatal("selection was expanded", text)
		}
		return llm.Response{Model: "resolved", Text: `{"ops":[{"kind":"insert","memory":{"kind":"fact","summary":"Selected fact","confidence":0.8},"sources":["s1"]}]}`}, nil
	}}
	var hash string
	phases := []string{}
	guard := func(_ context.Context, _ pgx.Tx, r ConsolidationReview) error {
		if r.UserID != "owner" || len(r.ObservationIDs) != 1 || r.ObservationIDs[0] != "chosen" || len(r.MemoryIDs) != 1 || r.MemoryIDs[0] != "chosen-memory" || len(r.PromptHash) != 64 || r.RunID == "" {
			t.Fatal("invalid review", r)
		}
		if hash != "" && hash != r.PromptHash {
			t.Fatal("prompt identity changed")
		}
		hash = r.PromptHash
		phases = append(phases, r.Phase)
		r.ObservationIDs[0] = "mutated-callback-copy"
		r.MemoryIDs[0] = "mutated-callback-copy"
		return nil
	}
	e := NewEngine(s, model)
	result, err := e.ConsolidateSelected(t.Context(), "owner", ConsolidationSelection{ObservationIDs: []string{"chosen"}, MemoryIDs: []string{"chosen-memory"}}, guard)
	if err != nil || calls != 1 || len(result.Runs) != 1 || result.Runs[0].ResolvedModel != "resolved" || result.Counts.ObservationsProcessed != 1 || strings.Join(phases, ",") != "inference,commit" {
		t.Fatal(result, err, calls, phases)
	}
	if saved, err := s.Consolidation(t.Context(), "owner", result.Runs[0].ID); err != nil || saved.Status != "completed" {
		t.Fatal("saved outcome missing", saved, err)
	}
	if _, err := s.Consolidation(t.Context(), "other", result.Runs[0].ID); err == nil {
		t.Fatal("foreign outcome read")
	}
	var sources []byte
	if err = s.pool.QueryRow(t.Context(), `SELECT sources FROM memories WHERE summary='Selected fact'`).Scan(&sources); err != nil || string(sources) != `["chosen"]` {
		t.Fatal("provenance", string(sources), err)
	}
	var pending int
	if err = s.pool.QueryRow(t.Context(), `SELECT count(*) FROM observations WHERE consolidated_at IS NULL`).Scan(&pending); err != nil || pending != 2 {
		t.Fatal("unselected observations acknowledged", pending, err)
	}
	if _, err = e.ConsolidateSelected(t.Context(), "owner", ConsolidationSelection{ObservationIDs: []string{"chosen"}}, allowSelected); err == nil || calls != 1 {
		t.Fatal("completed input repeated provider work", err, calls)
	}
}

func TestConsolidateSelectedRejectsUnapprovedInput(t *testing.T) {
	s := testStore(t)
	seedObservations(t, s, "owner", "one")
	seedObservations(t, s, "other", "foreign")
	seedMemory(t, s, "other", "foreign-memory")
	calls := 0
	e := NewEngine(s, &testModel{complete: func(context.Context, []llm.Message) (llm.Response, error) {
		calls++
		return llm.Response{Text: `{"ops":[]}`}, nil
	}})
	for _, selection := range []ConsolidationSelection{
		{}, {ObservationIDs: []string{"one", "one"}}, {ObservationIDs: []string{"foreign"}},
		{ObservationIDs: []string{"one"}, MemoryIDs: []string{"foreign-memory"}},
		{ObservationIDs: []string{"one"}, MemoryIDs: []string{"missing"}},
		{ObservationIDs: make([]string, 201)},
	} {
		if _, err := e.ConsolidateSelected(t.Context(), "owner", selection, allowSelected); err == nil {
			t.Fatal("invalid selection accepted", selection)
		}
	}
	if _, err := e.ConsolidateSelected(t.Context(), "owner", ConsolidationSelection{ObservationIDs: []string{"one"}}, nil); err == nil {
		t.Fatal("missing host guard accepted")
	}
	deny := func(context.Context, pgx.Tx, ConsolidationReview) error { return errors.New("source access revoked") }
	if _, err := e.ConsolidateSelected(t.Context(), "owner", ConsolidationSelection{ObservationIDs: []string{"one"}}, deny); err == nil {
		t.Fatal("host denial ignored")
	}
	if calls != 0 {
		t.Fatal("unapproved provider calls", calls)
	}
	if _, err := s.pool.Exec(t.Context(), `UPDATE observations SET content=$1 WHERE id='one'`, strings.Repeat("x", MaxContextBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ConsolidateSelected(t.Context(), "owner", ConsolidationSelection{ObservationIDs: []string{"one"}}, allowSelected); err == nil || calls != 0 {
		t.Fatal("oversized input truncated or sent", err, calls)
	}
}

func TestConsolidateSelectedGuardAfterInference(t *testing.T) {
	for _, mode := range []string{"revoke", "mutate", "invalid", "foreign-output", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			seedObservations(t, s, "owner", "one")
			seedObservations(t, s, "owner", "excluded")
			if _, err := s.pool.Exec(t.Context(), `CREATE TABLE host_grant (allowed boolean);INSERT INTO host_grant VALUES(true)`); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			model := &testModel{complete: func(context.Context, []llm.Message) (llm.Response, error) {
				calls++
				switch mode {
				case "revoke":
					// This also proves the inference-phase transaction was released.
					queryCtx, stop := context.WithTimeout(t.Context(), time.Second)
					defer stop()
					if _, err := s.pool.Exec(queryCtx, `UPDATE host_grant SET allowed=false`); err != nil {
						t.Fatal(err)
					}
				case "mutate":
					if _, err := s.pool.Exec(t.Context(), `UPDATE observations SET content='Changed during inference' WHERE id='one'`); err != nil {
						t.Fatal(err)
					}
				case "invalid":
					return llm.Response{Text: `{"ops":null}`}, nil
				case "foreign-output":
					return llm.Response{Text: `{"ops":[{"kind":"insert","memory":{"kind":"fact","summary":"Outside selection","confidence":1},"sources":["excluded"]}]}`}, nil
				case "cancel":
					cancel()
				}
				return llm.Response{Text: `{"ops":[{"kind":"insert","memory":{"kind":"fact","summary":"Should not commit","confidence":1},"sources":["s1"]}]}`}, nil
			}}
			guard := func(ctx context.Context, tx pgx.Tx, _ ConsolidationReview) error {
				var allowed bool
				if err := tx.QueryRow(ctx, `SELECT allowed FROM host_grant FOR SHARE`).Scan(&allowed); err != nil {
					return err
				}
				if !allowed {
					return errors.New("source access revoked")
				}
				return nil
			}
			result, err := NewEngine(s, model).ConsolidateSelected(ctx, "owner", ConsolidationSelection{ObservationIDs: []string{"one"}}, guard)
			if err == nil || calls != 1 || len(result.Runs) != 1 || result.Runs[0].Status != "failed" {
				t.Fatal("failure was repaired or committed", result, err, calls)
			}
			var memories, pending int
			if err = s.pool.QueryRow(t.Context(), `SELECT count(*) FROM memories`).Scan(&memories); err != nil {
				t.Fatal(err)
			}
			if err = s.pool.QueryRow(t.Context(), `SELECT count(*) FROM observations WHERE consolidated_at IS NULL`).Scan(&pending); err != nil || memories != 0 || pending != 2 {
				t.Fatal("partial apply/ack", memories, pending, err)
			}
		})
	}
}

func TestSelectedCommitHoldsHostGrantLock(t *testing.T) {
	s := testStore(t)
	seedObservations(t, s, "owner", "one")
	if _, err := s.pool.Exec(t.Context(), `CREATE TABLE host_grant (allowed boolean);INSERT INTO host_grant VALUES(true)`); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	guard := func(ctx context.Context, tx pgx.Tx, r ConsolidationReview) error {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT allowed FROM host_grant FOR SHARE`).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return errors.New("revoked")
		}
		if r.Phase == "commit" {
			close(entered)
			<-release
		}
		return nil
	}
	model := &testModel{complete: func(context.Context, []llm.Message) (llm.Response, error) {
		return llm.Response{Text: `{"ops":[{"kind":"insert","memory":{"kind":"fact","summary":"Before revocation","confidence":1},"sources":["s1"]}]}`}, nil
	}}
	finished := make(chan error, 1)
	go func() {
		_, err := NewEngine(s, model).ConsolidateSelected(t.Context(), "owner", ConsolidationSelection{ObservationIDs: []string{"one"}}, guard)
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("commit guard not called")
	}
	revoked := make(chan error, 1)
	var started atomic.Bool
	go func() {
		started.Store(true)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		_, err := s.pool.Exec(ctx, `UPDATE host_grant SET allowed=false`)
		revoked <- err
	}()
	for !started.Load() {
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-revoked:
		t.Fatal("revocation committed through the guard lock", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
}
