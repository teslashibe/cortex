package cortex

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/teslashibe/cortex/llm"
	"github.com/teslashibe/cortex/prompts"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("CORTEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set CORTEX_TEST_DATABASE_URL to an explicit disposable test database")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "cortex_test_" + strings.ToLower(rand.Text())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("cleanup schema: %v", err)
		}
		_ = admin.Close(ctx)
	})
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := &Store{pool: pool}
	if err := store.ApplyMigrations(ctx, Migrations()); err != nil {
		t.Fatal(err)
	}
	return store
}

func seedObservations(t *testing.T, s *Store, user string, ids ...string) {
	t.Helper()
	obs := make([]Observation, len(ids))
	for i, id := range ids {
		obs[i] = Observation{ID: id, UserID: user, Source: "test", Content: "evidence " + id, Refs: map[string]any{}, At: time.Date(2026, 9, 1, 0, i, 0, 0, time.UTC), IdempotencyKey: id}
	}
	if n, err := s.WriteObservations(context.Background(), obs); err != nil || n != len(obs) {
		t.Fatalf("seed observations %d: %v", n, err)
	}
}

func seedMemory(t *testing.T, s *Store, user, id string) {
	t.Helper()
	_, err := s.pool.Exec(context.Background(), `INSERT INTO memories(id,user_id,kind,summary,confidence,sources) VALUES($1,$2,'preference','Likes tea',0.8,'["old_source"]')`, id, user)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMigrationRetiresLegacyWithoutAcknowledgement(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	// Reconstruct v2 inside this test's isolated schema, then apply the forward migration.
	_, err := s.pool.Exec(ctx, `DROP TABLE consolidations;
		DELETE FROM schema_version WHERE version=3;
		DROP INDEX observations_idem_idx;
		CREATE UNIQUE INDEX observations_idem_idx ON observations(idempotency_key) WHERE idempotency_key IS NOT NULL;
		INSERT INTO dream_sessions(id,user_id,status,batch_key) VALUES
		('legacy_running','a','submitted','one'),('legacy_done','b','completed','two');`)
	if err != nil {
		t.Fatal(err)
	}
	seedObservations(t, s, "a", "legacy_observation")
	seedMemory(t, s, "a", "legacy_memory")
	if err := s.ApplyMigrations(ctx, Migrations()); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM dream_sessions WHERE id='legacy_running'`).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("running status=%s: %v", status, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT status FROM dream_sessions WHERE id='legacy_done'`).Scan(&status); err != nil || status != "completed" {
		t.Fatalf("history status=%s: %v", status, err)
	}
	var pending, memories int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM observations WHERE consolidated_at IS NULL`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("migration acknowledged observations: %d %v", pending, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM memories`).Scan(&memories); err != nil || memories != 1 {
		t.Fatalf("migration lost memories: %d %v", memories, err)
	}
	if err := s.ApplyMigrations(ctx, Migrations()); err != nil {
		t.Fatalf("migration rerun: %v", err)
	}
}

func TestStoreAtomicCompletion(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "a", "obs_1", "obs_2", "obs_late")
	seedObservations(t, s, "b", "obs_other")
	if _, err := s.WriteObservations(ctx, []Observation{{ID: "obs_other", UserID: "a", Source: "test", Content: "cross user", At: time.Now()}}); err == nil {
		t.Fatal("silently accepted another user's observation ID")
	}
	seedMemory(t, s, "a", "mem_a")
	seedMemory(t, s, "b", "mem_b")
	conn, err := s.claimUser(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseUser(conn, "a")
	if other, err := s.claimUser(ctx, "a"); !errors.Is(err, ErrConsolidationBusy) {
		if other != nil {
			releaseUser(other, "a")
		}
		t.Fatalf("parallel claim: %v", err)
	}
	run, err := createConsolidation(ctx, conn, "a", []string{"obs_1", "obs_2"}, "test", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := completeConsolidation(ctx, conn, "b", run.ID, nil, 0); err == nil {
		t.Fatal("completed another user's run")
	}
	for _, op := range []Op{
		{Kind: OpReinforce, OldID: "mem_b", Sources: []string{"obs_1"}},
		{Kind: OpReinforce, OldID: "mem_a", Sources: []string{"obs_other"}},
		{Kind: OpReinforce, OldID: "mem_a", Sources: []string{"obs_late"}},
		{Kind: OpContradict, OldID: "mem_b", Sources: []string{"obs_1"}, Memory: Memory{Kind: KindPreference, Summary: "Coffee", Confidence: 0.9}},
	} {
		if _, err := completeConsolidation(ctx, conn, "a", run.ID, []Op{op}, 0); err == nil {
			t.Fatalf("accepted foreign/out-of-chunk operation: %+v", op)
		}
	}
	ops := []Op{{Kind: OpReinforce, OldID: "mem_a", Sources: []string{"obs_1"}}, {Kind: OpInsert, Sources: []string{"obs_2"}, Memory: Memory{Kind: KindRoutine, Summary: "Runs daily", Confidence: 0.9}}}
	completed, err := completeConsolidation(ctx, conn, "a", run.ID, ops, 17)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || completed.Counts.ObservationsProcessed != 2 || completed.Counts.MemoriesCreated != 1 || completed.Counts.MemoriesReinforced != 1 {
		t.Fatalf("bad run: %+v", completed)
	}
	repeated, err := completeConsolidation(ctx, conn, "a", run.ID, ops, 999)
	if err != nil || repeated.Counts.TokensUsed != 17 {
		t.Fatalf("repeat completion: %+v %v", repeated, err)
	}
	var pending int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM observations WHERE consolidated_at IS NULL`).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("exact ack pending=%d err=%v", pending, err)
	}
	mem, err := s.Memories(ctx, "a")
	if err != nil || len(mem) != 2 {
		t.Fatalf("memories: %+v %v", mem, err)
	}
	for _, m := range mem {
		if m.ID == "mem_a" && (m.ReinforcedCount != 2 || !reflect.DeepEqual(m.Sources, []string{"obs_1", "old_source"})) {
			t.Fatalf("lost provenance or double applied: %+v", m)
		}
		if m.ID != "mem_a" && !reflect.DeepEqual(m.Sources, []string{"obs_2"}) {
			t.Fatalf("insert provenance: %+v", m)
		}
	}
	if err := s.WriteSignal(ctx, Signal{ID: "sig_bad", UserID: "a", MemoryID: "mem_b", SignalType: "used", SignalValue: 1}); err == nil {
		t.Fatal("accepted foreign feedback")
	}
	if err := s.WriteSignal(ctx, Signal{ID: "sig_ok", UserID: "a", MemoryID: "mem_a", SignalType: "used", SignalValue: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRollbackAndRecovery(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "a", "obs_1")
	seedMemory(t, s, "a", "mem_a")
	conn, err := s.claimUser(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	run, err := createConsolidation(ctx, conn, "a", []string{"obs_1"}, "test", "v1")
	if err != nil {
		releaseUser(conn, "a")
		t.Fatal(err)
	}
	// Inject a database constraint so the second mutation fails after the first
	// has executed. The transaction must roll back both mutation and ack.
	if _, err := conn.Exec(ctx, `ALTER TABLE memories ADD CONSTRAINT reject_test_summary CHECK (summary <> 'reject')`); err != nil {
		releaseUser(conn, "a")
		t.Fatal(err)
	}
	ops := []Op{{Kind: OpReinforce, OldID: "mem_a", Sources: []string{"obs_1"}}, {Kind: OpInsert, Sources: []string{"obs_1"}, Memory: Memory{Kind: KindPreference, Summary: "reject", Confidence: 0.8}}}
	if _, err := completeConsolidation(ctx, conn, "a", run.ID, ops, 0); err == nil {
		releaseUser(conn, "a")
		t.Fatal("expected database failure")
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT reinforced_count FROM memories WHERE id='mem_a'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial mutation persisted: %d %v", count, err)
	}
	if err := conn.Conn().Close(ctx); err != nil {
		t.Fatal(err)
	}
	conn.Release() // simulate worker connection loss; PostgreSQL releases the lock
	conn, err = s.claimUser(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseUser(conn, "a")
	var status string
	if err := conn.QueryRow(ctx, `SELECT status FROM consolidations WHERE id=$1`, run.ID).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("recovery: %s %v", status, err)
	}
	if _, err := completeConsolidation(ctx, conn, "a", run.ID, nil, 0); err == nil {
		t.Fatal("completed failed run")
	}
	next, err := createConsolidation(ctx, conn, "a", []string{"obs_1"}, "test", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := completeConsolidation(ctx, conn, "a", next.ID, []Op{}, 0); err != nil {
		t.Fatal(err)
	}
}

func TestStoreContradictionsAndIdempotency(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for _, user := range []string{"a", "b", "a"} {
		in := ObservationInput{Sender: "human", Content: "Coffee", At: at, ConversationUUID: "same"}
		o := in.ToObservation(user)
		n, err := s.WriteObservations(ctx, []Observation{o})
		if err != nil {
			t.Fatal(err)
		}
		if user == "b" && n != 1 {
			t.Fatal("idempotency incorrectly global")
		}
	}
	seedMemory(t, s, "a", "mem_old")
	conn, err := s.claimUser(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseUser(conn, "a")
	obs, err := unconsolidatedObservations(ctx, conn, "a", 10)
	if err != nil || len(obs) != 1 {
		t.Fatalf("idempotency: %d %v", len(obs), err)
	}
	run, err := createConsolidation(ctx, conn, "a", []string{obs[0].ID}, "test", "v1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = completeConsolidation(ctx, conn, "a", run.ID, []Op{{Kind: OpContradict, OldID: "mem_old", Sources: []string{obs[0].ID}, Memory: Memory{Kind: KindPreference, Summary: "Likes coffee", Confidence: 0.9}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	mem, err := s.Memories(ctx, "a")
	if err != nil || len(mem) != 1 || mem[0].ID == "mem_old" {
		t.Fatalf("current memories: %+v %v", mem, err)
	}
	hits, err := NewEngine(s, nil).Recall(ctx, "a", "Likes", 10)
	if err != nil || len(hits) != 1 || hits[0].Memory.ID == "mem_old" {
		t.Fatalf("recall: %+v %v", hits, err)
	}
	var historical int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM memories WHERE user_id='a'`).Scan(&historical); err != nil || historical != 2 {
		t.Fatalf("history removed: %d %v", historical, err)
	}
}

type testModel struct {
	calls    int
	complete func(context.Context, []llm.Message) (llm.Response, error)
}

func (m *testModel) Model() string { return "test-model" }
func (m *testModel) Complete(ctx context.Context, messages []llm.Message) (llm.Response, error) {
	m.calls++
	return m.complete(ctx, messages)
}

func TestConsolidateSequentialChunksAndFailure(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "a", "obs_1", "obs_2", "obs_3")
	model := &testModel{}
	model.complete = func(ctx context.Context, messages []llm.Message) (llm.Response, error) {
		var input struct {
			Observations []Observation `json:"observations"`
			Memories     []Memory      `json:"memories"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(messages[1].Content, prompts.Consolidation+"\n")), &input); err != nil {
			t.Fatal(err)
		}
		if len(input.Observations) != 1 {
			t.Fatalf("chunk count %d", len(input.Observations))
		}
		if model.calls == 1 {
			// Backdated arrival must not be swept into this request's snapshot.
			seedObservations(t, s, "a", "obs_late")
			return llm.Response{Text: `{"ops":[{"kind":"insert","memory":{"kind":"preference","summary":"Likes tea","confidence":0.8},"sources":["obs_1"]}]}`, Usage: llm.Usage{TotalTokens: 12}}, nil
		}
		if len(input.Memories) != 1 {
			t.Fatalf("chunk missing previous memory: %+v", input.Memories)
		}
		if model.calls == 2 {
			return llm.Response{Text: fmt.Sprintf(`{"ops":[{"kind":"reinforce","old_id":%q,"sources":["obs_2"]}]}`, input.Memories[0].ID)}, nil
		}
		return llm.Response{}, errors.New("model unavailable")
	}
	result, err := NewEngine(s, model).Consolidate(ctx, "a", ConsolidationOptions{ChunkSize: 1})
	if err == nil || len(result.Runs) != 3 || result.Counts.ObservationsProcessed != 2 || result.Runs[2].Status != "failed" {
		t.Fatalf("partial result: %+v %v", result, err)
	}
	var pending int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM observations WHERE consolidated_at IS NULL`).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("pending=%d %v", pending, err)
	}
	model.complete = func(context.Context, []llm.Message) (llm.Response, error) {
		return llm.Response{Text: `{"ops":[]}`}, nil
	}
	result, err = NewEngine(s, model).Consolidate(ctx, "a", ConsolidationOptions{})
	if err != nil || result.Counts.ObservationsProcessed != 2 {
		t.Fatalf("retry: %+v %v", result, err)
	}
}
