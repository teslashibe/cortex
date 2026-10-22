package cortex

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestObservationWindowsCoverUnicodeAndBoundaries(t *testing.T) {
	for _, n := range []int{0, 1, 511, 512, 513, 896, 897, 2000} {
		text := strings.Repeat("界", n)
		w, err := SplitObservation(Observation{ID: "o", Content: text})
		if err != nil {
			t.Fatal(err)
		}
		covered := make([]bool, n)
		for i, x := range w {
			if x.ObservationID != "o" || x.EndRune-x.StartRune > 512 || utf8.RuneCountInString(x.Text) != x.EndRune-x.StartRune {
				t.Fatalf("invalid %+v", x)
			}
			if i > 0 && w[i-1].EndRune-x.StartRune != 128 {
				t.Fatal("overlap")
			}
			for j := x.StartRune; j < x.EndRune; j++ {
				covered[j] = true
			}
		}
		for _, ok := range covered {
			if !ok {
				t.Fatal("uncovered rune")
			}
		}
	}
	if _, err := SplitObservation(Observation{Content: string([]byte{255})}); err == nil {
		t.Fatal("invalid UTF8 accepted")
	}
}

func TestWindowRecallTailAndDeletion(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := NewEngine(s, nil, WithEmbedder(vectorFixture{}))
	text := strings.Repeat("irrelevant preamble ", 80) + "The dog is named Spot."
	if _, err := e.Observe(ctx, []Observation{{ID: "tail", UserID: "owner", Content: text, Refs: map[string]any{"sender": "user"}}}); err != nil {
		t.Fatal(err)
	}
	r, err := e.IndexObservationWindows(ctx, "owner", 10)
	if err != nil || r.Indexed != 1 || r.Incomplete {
		t.Fatalf("index %+v %v", r, err)
	}
	r, err = e.IndexObservationWindows(ctx, "owner", 10)
	if err != nil || r.Indexed != 0 {
		t.Fatalf("reindex %+v %v", r, err)
	}
	hit, err := e.RecallObservationWindows(ctx, "owner", "Spot", RecallOptions{Limit: 1, LexicalOnly: true})
	if err != nil || len(hit.Hits) != 1 || !strings.Contains(hit.Hits[0].Window.Text, "Spot") || hit.Hits[0].Window.StartRune == 0 || hit.Hits[0].Refs["sender"] != "user" {
		t.Fatalf("tail %+v %v", hit, err)
	}
	if hit, err = e.RecallObservationWindows(ctx, "other", "Spot", RecallOptions{}); err != nil || len(hit.Hits) != 0 {
		t.Fatal("foreign evidence")
	}
	if _, err = e.ForgetObservation(ctx, "owner", "tail"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM observation_window_embeddings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("vectors survived", count, err)
	}
}

func TestWindowOperationsRecheckForgetAndLifecycle(t *testing.T) {
	for _, operation := range []string{"index", "recall"} {
		t.Run(operation, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			seedObservations(t, s, "owner", "source")
			e := NewEngine(s, nil, WithEmbedder(vectorFixture{}))
			if _, err := e.IndexObservationWindows(ctx, "owner", 10); err != nil {
				t.Fatal(err)
			}
			if operation == "index" {
				if _, err := s.pool.Exec(ctx, `DELETE FROM observation_window_embeddings`); err != nil {
					t.Fatal(err)
				}
			}
			waiting := waitingEmbedder{make(chan struct{}), make(chan struct{})}
			e.embedder = waiting
			done := make(chan error, 1)
			go func() {
				if operation == "index" {
					r, err := e.IndexObservationWindows(ctx, "owner", 10)
					if err == nil && (r.Indexed != 0 || !r.Incomplete) {
						err = errors.New("forgotten vector published")
					}
					done <- err
				} else {
					r, err := e.RecallObservationWindows(ctx, "owner", "evidence", RecallOptions{})
					if err == nil && len(r.Hits) != 0 {
						err = errors.New("forgotten evidence returned")
					}
					done <- err
				}
			}()
			<-waiting.entered
			if _, err := e.ForgetObservation(ctx, "owner", "source"); err != nil {
				t.Fatal(err)
			}
			close(waiting.resume)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
	s := testStore(t)
	ctx := context.Background()
	seedObservations(t, s, "owner", "source")
	seedMemory(t, s, "owner", "m")
	e := NewEngine(s, nil)
	if _, err := s.pool.Exec(ctx, `UPDATE memories SET sources='["source"]',archived_at=now() WHERE id='m'`); err != nil {
		t.Fatal(err)
	}
	if r, err := e.RecallObservationWindows(ctx, "owner", "evidence", RecallOptions{}); err != nil || len(r.Hits) != 0 {
		t.Fatal("archived source returned")
	}
}

type failSecondWindowBatch struct{ calls int }

func (*failSecondWindowBatch) Model() string { return "fail-second" }
func (e *failSecondWindowBatch) Embed(_ context.Context, texts []string) ([][]float64, error) {
	e.calls++
	if e.calls == 2 {
		return nil, errors.New("interrupted")
	}
	out := make([][]float64, len(texts))
	for i := range out {
		out[i] = []float64{1, 0}
	}
	return out, nil
}

func TestWindowIndexDoesNotPublishPartialObservation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	e := NewEngine(s, nil, WithEmbedder(&failSecondWindowBatch{}))
	if _, err := e.Observe(ctx, []Observation{{ID: "long", UserID: "owner", Content: strings.Repeat("x", 14000)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.IndexObservationWindows(ctx, "owner", 1); err == nil {
		t.Fatal("expected interrupted embedding")
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM observation_window_embeddings`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial observation published", count, err)
	}
}
