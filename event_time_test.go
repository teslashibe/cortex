package cortex

import (
	"context"
	"testing"
	"time"
)

func TestCivilEventDates(t *testing.T) {
	anchor := time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC)
	for _, tc := range []struct{ text, zone, want string }{{"yesterday", "America/Los_Angeles", "2026-03-07"}, {"today", "UTC", "2026-03-09"}, {"last Monday", "UTC", "2026-03-02"}, {"7 days ago", "UTC", "2026-03-02"}, {"March 1, 2026", "UTC", "2026-03-01"}, {"last week", "UTC", ""}, {"March 1", "UTC", ""}, {"2026-02-30", "UTC", ""}} {
		got, _, err := ResolveDateExpression(tc.text, anchor, tc.zone)
		if err != nil || got != tc.want {
			t.Fatalf("%+v => %q %v", tc, got, err)
		}
	}
	for _, tc := range []struct {
		a, b string
		want int
	}{{"2026-03-07", "2026-03-09", 2}, {"2026-11-02", "2026-10-31", -2}, {"0001-01-01", "9999-12-31", 3652058}} {
		n, err := CalendarDaysBetween(tc.a, tc.b)
		if err != nil || n != tc.want {
			t.Fatalf("%+v %d %v", tc, n, err)
		}
	}
	if _, _, err := ResolveDateExpression("today", anchor, "Local"); err == nil {
		t.Fatal("implicit timezone")
	}
	if d, _, _ := ResolveDateExpression("yesterday", time.Time{}, "UTC"); d != "" {
		t.Fatal("invented anchor")
	}
}

func TestEventDatesRequireLiveOwnedEvidence(t *testing.T) {
	s := testStore(t)
	e := NewEngine(s, nil)
	ctx := context.Background()
	if _, err := e.Observe(ctx, []Observation{{ID: "o", UserID: "u", Content: "I visited yesterday", At: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)}}); err != nil {
		t.Fatal(err)
	}
	candidates := []Candidate{{"date", "o", 10, 19}}
	dates, err := e.ResolveEventDates(ctx, "u", "UTC", candidates)
	if err != nil || dates[0].Date != "2026-03-08" || dates[0].Text != "yesterday" {
		t.Fatalf("%+v %v", dates, err)
	}
	if _, err := e.ResolveEventDates(ctx, "other", "UTC", candidates); err == nil {
		t.Fatal("foreign date")
	}
	if _, err := e.ForgetObservation(ctx, "u", "o"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ResolveEventDates(ctx, "u", "UTC", candidates); err == nil {
		t.Fatal("forgotten date")
	}
}

func TestEvidenceUnionDeduplicatesWithoutClaimingEventCount(t *testing.T) {
	s := testStore(t)
	e := NewEngine(s, nil)
	ctx := context.Background()
	if _, err := e.Observe(ctx, []Observation{{ID: "one", UserID: "u", Content: "museum visit on Monday"}, {ID: "two", UserID: "u", Content: "gallery visit on Tuesday"}, {ID: "foreign", UserID: "v", Content: "museum gallery"}}); err != nil {
		t.Fatal(err)
	}
	r, err := e.RecallEvidence(ctx, "u", []string{"museum", "visit"}, RecallOptions{Limit: 10, LexicalOnly: true})
	if err != nil || r.Sources != 2 || len(r.Hits) != 2 || r.Coverage != "retrieved_evidence_only" || r.Truncated {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = e.RecallEvidence(ctx, "u", []string{"visit"}, RecallOptions{Limit: 1, LexicalOnly: true})
	if err != nil || !r.Truncated {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = e.RecallEvidence(ctx, "u", []string{"visit", "visit"}, RecallOptions{}); err == nil {
		t.Fatal("duplicate queries")
	}
}
