package cortex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/teslashibe/cortex/llm"
)

func TestFactAndUnreferencedObservationAudit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "owner", "source", "unused")
	model := &testModel{complete: func(context.Context, []llm.Message) (llm.Response, error) {
		return llm.Response{Text: `{"ops":[{"kind":"insert","memory":{"kind":"fact","summary":"A supported factual statement.","confidence":0.8},"sources":["source"]}]}`}, nil
	}}
	e := NewEngine(s, model)
	out, err := e.Consolidate(ctx, "owner", ConsolidationOptions{})
	if err != nil || len(out.UnreferencedObservationIDs) != 1 || out.UnreferencedObservationIDs[0] != "unused" {
		t.Fatalf("audit %+v %v", out, err)
	}
	ms, err := e.Memories(ctx, "owner")
	if err != nil || len(ms) != 1 || ms[0].Kind != KindFact || titleForKind(KindFact) != "Facts" {
		t.Fatalf("fact %+v %v", ms, err)
	}
}

func TestObservationRecallPreservesEvidenceAndLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := NewEngine(s, nil, WithEmbedder(vectorFixture{}))
	obs := Observation{ID: "raw", UserID: "owner", Source: "synthetic", Content: "The dog is named Spot.", At: time.Now(), Refs: map[string]any{"sender": "user", "conversation_uuid": "session"}}
	if _, err := e.Observe(ctx, []Observation{obs}); err != nil {
		t.Fatal(err)
	}
	// Memory indexing must not send unrequested raw evidence to the provider.
	if _, err := e.Index(ctx, "owner", 10); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM observation_embeddings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("implicit raw indexing: %d %v", count, err)
	}
	indexed, err := e.IndexObservations(ctx, "owner", 10)
	if err != nil || indexed.Indexed != 1 || indexed.Incomplete {
		t.Fatalf("index %+v %v", indexed, err)
	}
	for i := 0; i < 2; i++ {
		r, err := e.RecallObservations(ctx, "owner", "a canine companion", RecallOptions{Limit: 1})
		if err != nil || len(r.Hits) != 1 || r.Hits[0].Observation.Refs["sender"] != "user" || r.Hits[0].Observation.Source != "synthetic" || r.Incomplete {
			t.Fatalf("source recall %+v %v", r, err)
		}
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM training_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("source reads fabricated feedback: %d %v", count, err)
	}
	if r, err := e.RecallObservations(ctx, "other", "dog", RecallOptions{}); err != nil || len(r.Hits) != 0 {
		t.Fatalf("foreign source %+v %v", r, err)
	}
	seedMemory(t, s, "owner", "m")
	if _, err := s.pool.Exec(ctx, `UPDATE memories SET sources='["raw"]',archived_at=now() WHERE id='m'`); err != nil {
		t.Fatal(err)
	}
	if r, err := e.RecallObservations(ctx, "owner", "dog", RecallOptions{}); err != nil || len(r.Hits) != 0 {
		t.Fatalf("archived source %+v %v", r, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE memories SET archived_at=NULL,expires_at=now()-interval '1 second' WHERE id='m'`); err != nil {
		t.Fatal(err)
	}
	if r, err := e.RecallObservations(ctx, "owner", "dog", RecallOptions{}); err != nil || len(r.Hits) != 0 {
		t.Fatalf("expired source %+v %v", r, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE memories SET expires_at=NULL,contradicted_by='replacement' WHERE id='m'`); err != nil {
		t.Fatal(err)
	}
	if r, err := e.RecallObservations(ctx, "owner", "dog", RecallOptions{}); err != nil || len(r.Hits) != 0 {
		t.Fatalf("superseded source %+v %v", r, err)
	}
	if _, err := e.Forget(ctx, "owner", "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM observation_embeddings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("forgotten vector survived: %d %v", count, err)
	}
	if n, err := e.Observe(ctx, []Observation{obs}); err != nil || n != 0 {
		t.Fatalf("forgotten source replay: %d %v", n, err)
	}
}

func TestObservationRecallAndIndexRecheckAfterProvider(t *testing.T) {
	for _, operation := range []string{"recall", "index"} {
		t.Run(operation, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			seedObservations(t, s, "owner", "source")
			seedMemory(t, s, "owner", "m")
			if _, err := s.pool.Exec(ctx, `UPDATE memories SET sources='["source"]' WHERE id='m'`); err != nil {
				t.Fatal(err)
			}
			e := NewEngine(s, nil, WithEmbedder(vectorFixture{}))
			if _, err := e.IndexObservations(ctx, "owner", 10); err != nil {
				t.Fatal(err)
			}
			if operation == "index" {
				if _, err := s.pool.Exec(ctx, `DELETE FROM observation_embeddings`); err != nil {
					t.Fatal(err)
				}
			}
			waiting := waitingEmbedder{make(chan struct{}), make(chan struct{})}
			e.embedder = waiting
			done := make(chan error, 1)
			go func() {
				if operation == "index" {
					r, err := e.IndexObservations(ctx, "owner", 10)
					if err == nil && (r.Indexed != 0 || !r.Incomplete) {
						err = errors.New("published forgotten source")
					}
					done <- err
				} else {
					r, err := e.RecallObservations(ctx, "owner", "evidence", RecallOptions{})
					if err == nil && len(r.Hits) != 0 {
						err = errors.New("returned forgotten source")
					}
					done <- err
				}
			}()
			<-waiting.entered
			if _, err := e.Forget(ctx, "owner", "m"); err != nil {
				t.Fatal(err)
			}
			close(waiting.resume)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestObservationFailureAndHTTP(t *testing.T) {
	s := testStore(t)
	seedObservations(t, s, "owner", "source")
	e := NewEngine(s, nil, WithEmbedder(vectorFixture{fail: true}))
	if _, err := e.RecallObservations(context.Background(), "owner", "evidence", RecallOptions{}); err == nil {
		t.Fatal("silent fallback")
	}
	r := httptest.NewRecorder()
	NewAPI(e).Routes().ServeHTTP(r, httptest.NewRequest("GET", "/v1/observations/recall?user=owner&q=evidence&mode=lexical", nil))
	var result ObservationRecallResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || len(result.Hits) != 1 {
		t.Fatalf("HTTP recall %d %s", r.Code, r.Body)
	}
	r = httptest.NewRecorder()
	NewAPI(e).Routes().ServeHTTP(r, httptest.NewRequest("POST", "/v1/observations/index?user=owner", strings.NewReader("{}")))
	if r.Code != 400 {
		t.Fatalf("HTTP index %d", r.Code)
	}
}

func TestForgetUnconsolidatedObservation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := NewEngine(s, nil, WithEmbedder(vectorFixture{}))
	seedObservations(t, s, "owner", "source")
	if _, err := s.pool.Exec(ctx, `INSERT INTO observations(id,user_id,source,content) SELECT 'copy',user_id,source,content FROM observations WHERE id='source'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.IndexObservations(ctx, "owner", 10); err != nil {
		t.Fatal(err)
	}
	if r, err := e.ForgetObservation(ctx, "other", "source"); err != nil || r.Observations != 0 {
		t.Fatalf("foreign deletion %+v %v", r, err)
	}
	r, err := e.ForgetObservation(ctx, "owner", "source")
	if err != nil || r.Observations != 2 || r.Memories != 0 {
		t.Fatalf("source deletion %+v %v", r, err)
	}
	if r, err := e.ForgetObservation(ctx, "owner", "source"); err != nil || r.Observations != 0 {
		t.Fatalf("deletion retry %+v %v", r, err)
	}
	if n, err := e.Observe(ctx, []Observation{{ID: "new", UserID: "owner", Content: "evidence source", Source: "test"}}); err != nil || n != 0 {
		t.Fatalf("content replay %d %v", n, err)
	}
}

func TestConsolidationRepairsFormatOnceAndPreservesUsage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "owner", "source")
	model := &testModel{}
	model.complete = func(_ context.Context, messages []llm.Message) (llm.Response, error) {
		if model.calls == 1 {
			return llm.Response{Text: `{"ops":[{"kind":"insert","memory":{"kind":"fact","summary":"` + strings.Repeat("x", MaxSummaryCharacters+1) + `","confidence":0.8},"sources":["source"]}]}`, Usage: llm.Usage{TotalTokens: 9}}, nil
		}
		if len(messages) != 4 {
			t.Fatalf("repair context messages: %d", len(messages))
		}
		return llm.Response{Text: `{"ops":[{"kind":"insert","memory":{"kind":"fact","summary":"A short supported fact.","confidence":0.8},"sources":["source"]}]}`, Usage: llm.Usage{TotalTokens: 12}}, nil
	}
	r, err := NewEngine(s, model).Consolidate(ctx, "owner", ConsolidationOptions{})
	if err != nil || model.calls != 2 || r.FormatRepairs != 1 || r.Counts.TokensUsed != 21 || r.Counts.ObservationsProcessed != 1 {
		t.Fatalf("repair %+v calls=%d err=%v", r, model.calls, err)
	}
}

func TestConsolidationRepairBoundAndNoTransportRetry(t *testing.T) {
	for _, transport := range []bool{false, true} {
		s := testStore(t)
		seedObservations(t, s, "owner", "source")
		model := &testModel{complete: func(context.Context, []llm.Message) (llm.Response, error) {
			if transport {
				return llm.Response{}, errors.New("provider unavailable")
			}
			return llm.Response{Text: `{"ops":[`}, nil
		}}
		_, err := NewEngine(s, model).Consolidate(context.Background(), "owner", ConsolidationOptions{})
		want := 2
		if transport {
			want = 1
		}
		if err == nil || model.calls != want {
			t.Fatalf("transport=%v calls=%d err=%v", transport, model.calls, err)
		}
		var pending int
		if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM observations WHERE consolidated_at IS NULL`).Scan(&pending); err != nil || pending != 1 {
			t.Fatalf("failed repair acknowledged: %d %v", pending, err)
		}
	}
}

func TestObservationCapacityBeforeProviderWork(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `INSERT INTO observations(id,user_id,source,content) SELECT 'o-'||g,'owner','test','evidence' FROM generate_series(1,$1) g`, MaxRecallObservations+1); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(s, nil, WithEmbedder(vectorFixture{fail: true}))
	if _, err := e.RecallObservations(ctx, "owner", "evidence", RecallOptions{}); !errors.Is(err, ErrObservationCapacity) {
		t.Fatalf("recall limit: %v", err)
	}
	if _, err := e.IndexObservations(ctx, "owner", 100); !errors.Is(err, ErrObservationCapacity) {
		t.Fatalf("index limit: %v", err)
	}
}

func TestForgetObservationRemovesDerivedMemory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "owner", "source")
	seedMemory(t, s, "owner", "derived")
	seedMemory(t, s, "other", "foreign")
	if _, err := s.pool.Exec(ctx, `UPDATE memories SET sources='["source"]' WHERE id='derived'`); err != nil {
		t.Fatal(err)
	}
	r, err := NewEngine(s, nil).ForgetObservation(ctx, "owner", "source")
	if err != nil || r.Memories != 1 || r.Observations != 1 {
		t.Fatalf("derived deletion %+v %v", r, err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM memories WHERE id='foreign'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("foreign memory changed: %d %v", count, err)
	}
}
