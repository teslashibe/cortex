package cortex

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/teslashibe/cortex/llm"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEvidenceTimeResolvedModelAndBoundedContext(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "owner", "source")
	at := time.Date(2020, 2, 3, 0, 0, 0, 0, time.UTC)
	if _, err := s.pool.Exec(ctx, `UPDATE observations SET at=$1 WHERE id='source'`, at); err != nil {
		t.Fatal(err)
	}
	model := &testModel{complete: func(context.Context, []llm.Message) (llm.Response, error) {
		return llm.Response{Model: "resolved", Text: `{"ops":[{"kind":"insert","memory":{"kind":"preference","summary":"Tea","confidence":0.8},"sources":["source"]}]}`}, nil
	}}
	e := NewEngine(s, model)
	result, err := e.Consolidate(ctx, "owner", ConsolidationOptions{})
	if err != nil || result.Runs[0].ResolvedModel != "resolved" {
		t.Fatalf("resolved model %+v %v", result, err)
	}
	ms, err := e.Memories(ctx, "owner")
	if err != nil || !ms[0].LastSeenAt.Equal(at) || !ms[0].BornAt.After(at) {
		t.Fatalf("evidence time %+v %v", ms, err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO memories(id,user_id,kind,summary,confidence) SELECT 'context-'||g,'owner','preference','A separate supported fact '||g,0.8 FROM generate_series(1,550) g`); err != nil {
		t.Fatal(err)
	}
	seedObservations(t, s, "owner", "next")
	model.complete = func(_ context.Context, messages []llm.Message) (llm.Response, error) {
		text := messages[1].Content
		start := strings.Index(text, `{"user_id"`)
		if start < 0 {
			t.Fatal("missing context")
		}
		var value struct {
			Memories []Memory `json:"memories"`
		}
		if err := json.Unmarshal([]byte(text[start:]), &value); err != nil {
			t.Fatal(err)
		}
		if len(value.Memories) != 500 {
			t.Fatalf("unbounded context %d", len(value.Memories))
		}
		return llm.Response{Text: `{"ops":[]}`}, nil
	}
	result, err = e.Consolidate(ctx, "owner", ConsolidationOptions{})
	if err != nil || !result.ContextLimited {
		t.Fatalf("context limit %+v %v", result, err)
	}
}

func TestActivationAndMaintenance(t *testing.T) {
	p := DefaultRetentionPolicy()
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m := Memory{LastSeenAt: at.Add(-p.HalfLife), ReinforcedCount: 1, Confidence: 0.9}
	if v := Activation(m, at, p); math.Abs(v-0.5) > 1e-9 {
		t.Fatalf("half-life: %f", v)
	}
	m.Pinned = true
	if Activation(m, at, p) != 1 {
		t.Fatal("pinned fact decayed")
	}
	m.Pinned = false
	m.ReinforcedCount = 3
	if Activation(m, at, p) <= 0.5 {
		t.Fatal("independent reinforcement did not preserve activation")
	}
	s := testStore(t)
	e := NewEngine(s, nil)
	seedMemory(t, s, "owner", "old")
	seedMemory(t, s, "owner", "pinned")
	seedMemory(t, s, "owner", "expired")
	var err error
	if _, err = s.pool.Exec(context.Background(), `UPDATE memories SET last_seen_at=$1 WHERE user_id='owner'`, at.Add(-800*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(context.Background(), `UPDATE memories SET pinned=true WHERE id IN ('pinned','expired')`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(context.Background(), `UPDATE memories SET expires_at=$1 WHERE id='expired'`, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	r, err := e.Maintain(context.Background(), "owner", at, 100)
	if err != nil || r.Archived != 2 {
		t.Fatalf("maintenance %+v %v", r, err)
	}
	r, err = e.Maintain(context.Background(), "owner", at, 100)
	if err != nil || r.Archived != 0 {
		t.Fatalf("maintenance replay %+v %v", r, err)
	}
	var confidence float64
	if err = s.pool.QueryRow(context.Background(), `SELECT confidence FROM memories WHERE id='old'`).Scan(&confidence); err != nil || confidence != 0.8 {
		t.Fatalf("confidence changed %v %v", confidence, err)
	}
	if err = e.SetRetention(context.Background(), "owner", "old", Retention{Pinned: true, Restore: true}); err != nil {
		t.Fatal(err)
	}
	ms, err := e.Memories(context.Background(), "owner")
	if err != nil || len(ms) != 2 {
		t.Fatalf("restore %d %v", len(ms), err)
	}
}

func TestFeedbackIdempotencyAndIsolation(t *testing.T) {
	s := testStore(t)
	e := NewEngine(s, nil)
	ctx := context.Background()
	seedMemory(t, s, "owner", "fact")
	yes := true
	f := Feedback{ID: "event", UserID: "owner", MemoryID: "fact", RetrievalID: "request", Used: true, Success: &yes}
	for i := 0; i < 2; i++ {
		if err := e.Feedback(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var utility float64
	if err := s.pool.QueryRow(ctx, `SELECT outcome_count,utility FROM memories WHERE id='fact'`).Scan(&count, &utility); err != nil || count != 1 || utility <= 0.5 {
		t.Fatalf("feedback: %d %v %v", count, utility, err)
	}
	f.Used = false
	if e.Feedback(ctx, f) == nil {
		t.Fatal("accepted changed event ID")
	}
	f.ID = "unused"
	if err := e.Feedback(ctx, f); err != nil {
		t.Fatal(err)
	}
	f.ID = "foreign"
	f.UserID = "other"
	if !errors.Is(e.Feedback(ctx, f), ErrMemoryNotFound) {
		t.Fatal("foreign feedback accepted")
	}
	if err := s.pool.QueryRow(ctx, `SELECT outcome_count FROM memories WHERE id='fact'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("unused retrieval trained utility")
	}
}

func TestForgetFencesReplayAndDerivedState(t *testing.T) {
	s := testStore(t)
	e := NewEngine(s, nil)
	ctx := context.Background()
	seedObservations(t, s, "owner", "source")
	seedMemory(t, s, "owner", "old")
	seedMemory(t, s, "owner", "new")
	seedMemory(t, s, "other", "foreign")
	if _, err := s.pool.Exec(ctx, `UPDATE memories SET sources='["source"]' WHERE id='old'; UPDATE memories SET sources='[]' WHERE id='new'; INSERT INTO memory_edges(source_id,target_id,edge_type) VALUES('new','old','contradicts'); INSERT INTO memory_embeddings(memory_id,model,content_hash,embedding) VALUES('new','test','hash',ARRAY[1.0,0.0]);`); err != nil {
		t.Fatal(err)
	}
	// An import already holding the shared barrier may finish. Forget waits and
	// includes its identical content even when the import uses a new identity.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(214015,hashtext('owner'))`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var result ForgetResult
	var forgetErr error
	go func() { defer wg.Done(); result, forgetErr = e.Forget(ctx, "owner", "old") }()
	copy := Observation{ID: "copy", UserID: "owner", Source: "test", Content: "evidence source", At: time.Now(), IdempotencyKey: "new-import"}
	if _, err = tx.Exec(ctx, `INSERT INTO observations(id,user_id,source,content,at,idempotency_key) VALUES($1,$2,$3,$4,$5,$6)`, copy.ID, copy.UserID, copy.Source, copy.Content, copy.At, copy.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if forgetErr != nil || result.Memories != 2 || result.Observations != 2 {
		t.Fatalf("forget %+v %v", result, forgetErr)
	}
	copy.ID = "replayed"
	copy.IdempotencyKey = "another-import"
	if n, err := s.WriteObservations(ctx, []Observation{copy}); err != nil || n != 0 {
		t.Fatalf("content replay %d %v", n, err)
	}
	copy.UserID = "other"
	if n, err := s.WriteObservations(ctx, []Observation{copy}); err != nil || n != 1 {
		t.Fatalf("foreign suppression %d %v", n, err)
	}
	if _, err = e.Forget(ctx, "owner", "old"); err != nil {
		t.Fatalf("forget retry %v", err)
	}
	var vectors int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM memory_embeddings`).Scan(&vectors); err != nil || vectors != 0 {
		t.Fatal("forgotten embeddings retained")
	}
	if ms, err := e.Memories(ctx, "other"); err != nil || len(ms) != 1 {
		t.Fatal("foreign memory removed")
	}
}

type vectorFixture struct{ fail bool }

func (vectorFixture) Model() string { return "test-vector-v1" }
func (v vectorFixture) Embed(_ context.Context, input []string) ([][]float64, error) {
	if v.fail {
		return nil, errors.New("unavailable")
	}
	out := make([][]float64, len(input))
	for i, s := range input {
		if s == "a canine companion" || s == "The dog is named Spot." {
			out[i] = []float64{1, 0}
		} else {
			out[i] = []float64{0, 1}
		}
	}
	return out, nil
}

func TestSemanticRecallAndIndexRecovery(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedMemory(t, s, "owner", "dog")
	seedMemory(t, s, "owner", "car")
	if _, err := s.pool.Exec(ctx, `UPDATE memories SET summary='The dog is named Spot.' WHERE id='dog'; UPDATE memories SET summary='The car is red.' WHERE id='car'`); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(s, nil, WithEmbedder(vectorFixture{}))
	indexed, err := e.Index(ctx, "owner", 100)
	if err != nil || indexed.Indexed != 2 {
		t.Fatalf("index %+v %v", indexed, err)
	}
	r, err := e.RecallWithOptions(ctx, "owner", "a canine companion", RecallOptions{Limit: 1})
	if err != nil || len(r.Hits) != 1 || r.Hits[0].Memory.ID != "dog" || r.Incomplete {
		t.Fatalf("semantic recall %+v %v", r, err)
	}
	indexed, err = e.Index(ctx, "owner", 100)
	if err != nil || indexed.Indexed != 0 {
		t.Fatalf("reindex %+v %v", indexed, err)
	}
	e.embedder = vectorFixture{fail: true}
	if _, err = e.Recall(ctx, "owner", "canine", 1); err == nil {
		t.Fatal("silently swallowed provider failure")
	}
	if _, err = e.RecallWithOptions(ctx, "owner", "car", RecallOptions{Limit: 1, LexicalOnly: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Forget(ctx, "owner", "dog"); err != nil {
		t.Fatal(err)
	}
	e.embedder = vectorFixture{}
	r, err = e.RecallWithOptions(ctx, "owner", "a canine companion", RecallOptions{Limit: 1})
	if err != nil || len(r.Hits) != 0 {
		t.Fatalf("forgotten vector recalled %+v %v", r, err)
	}
}

type rankingFixture struct{}

func (rankingFixture) Model() string { return "ranking-fixture" }
func (rankingFixture) Embed(_ context.Context, inputs []string) ([][]float64, error) {
	out := make([][]float64, len(inputs))
	for i, s := range inputs {
		if strings.Contains(s, "car") {
			out[i] = []float64{0.9, 0.1}
		} else {
			out[i] = []float64{1, 0}
		}
	}
	return out, nil
}
func TestAgeCannotBuryBestSemanticMatch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedMemory(t, s, "owner", "dog")
	seedMemory(t, s, "owner", "car")
	if _, err := s.pool.Exec(ctx, `UPDATE memories SET summary='dog',last_seen_at=now()-interval '5 years' WHERE id='dog'; UPDATE memories SET summary='car',reinforced_count=100 WHERE id='car'`); err != nil {
		t.Fatal(err)
	}
	e := NewEngine(s, nil, WithEmbedder(rankingFixture{}))
	if _, err := e.Index(ctx, "owner", 100); err != nil {
		t.Fatal(err)
	}
	r, err := e.Recall(ctx, "owner", "canine", 1)
	if err != nil || len(r) != 1 || r[0].Memory.ID != "dog" {
		t.Fatalf("age overrode relevance: %+v %v", r, err)
	}
}

type waitingEmbedder struct {
	entered chan struct{}
	resume  chan struct{}
}

func (waitingEmbedder) Model() string { return "test-vector-v1" }
func (e waitingEmbedder) Embed(ctx context.Context, _ []string) ([][]float64, error) {
	close(e.entered)
	select {
	case <-e.resume:
		return [][]float64{{1, 0}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestForgetDuringRecall(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedMemory(t, s, "owner", "fact")
	e := NewEngine(s, nil, WithEmbedder(vectorFixture{}))
	if _, err := e.Index(ctx, "owner", 10); err != nil {
		t.Fatal(err)
	}
	waiting := waitingEmbedder{make(chan struct{}), make(chan struct{})}
	e.embedder = waiting
	type response struct {
		hits []Hit
		err  error
	}
	done := make(chan response, 1)
	go func() { hits, err := e.Recall(ctx, "owner", "tea", 1); done <- response{hits, err} }()
	<-waiting.entered
	if _, err := e.Forget(ctx, "owner", "fact"); err != nil {
		t.Fatal(err)
	}
	close(waiting.resume)
	r := <-done
	if r.err != nil || len(r.hits) != 0 {
		t.Fatalf("forgotten result escaped after inference: %+v", r)
	}
}
