package cortex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/teslashibe/cortex/llm"
)

type answerModel struct {
	call    int
	respond func(int, []llm.Message) (string, error)
}

func (m *answerModel) Model() string { return "test" }
func (m *answerModel) Complete(_ context.Context, messages []llm.Message) (llm.Response, error) {
	m.call++
	text, err := m.respond(m.call, messages)
	return llm.Response{Text: text, Model: "test"}, err
}

func TestAnswerEvidenceSourceAndDeletionFence(t *testing.T) {
	for _, mode := range []string{"ok", "fabricated_quote", "foreign_source", "deleted_during_answer", "transport_failure", "missing_citation"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			anchor := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
			text := "I arrived in Brookfield today."
			if _, err := s.WriteObservations(ctx, []Observation{{ID: "own", UserID: "u", Content: text, At: anchor, Refs: map[string]any{"sender": "user"}, IdempotencyKey: "own"}, {ID: "foreign", UserID: "v", Content: text, At: anchor, IdempotencyKey: "foreign"}}); err != nil {
				t.Fatal(err)
			}
			m := &answerModel{}
			m.respond = func(call int, msg []llm.Message) (string, error) {
				switch call {
				case 1:
					return `{"queries":["Brookfield arrival"],"speaker":"user"}`, nil
				case 2:
					if mode == "transport_failure" {
						return "", errors.New("private upstream body")
					}
					quote, id := text, "own"
					if mode == "fabricated_quote" {
						quote = "I arrived yesterday."
					}
					if mode == "foreign_source" {
						id = "foreign"
					}
					return fmt.Sprintf(`{"facts":[{"id":"f1","source_id":%q,"quote":%q,"claim":"Arrived","date_expression":"today"}],"calculation":{"operation":"days","from_fact":"f1","to_as_of":true}}`, id, quote), nil
				case 3:
					if mode == "deleted_during_answer" {
						if _, err := NewEngine(s, nil).ForgetObservation(ctx, "u", "own"); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "missing_citation" {
						return `{"answer":"0 days","status":"answered","citations":[]}`, nil
					}
					return `{"answer":"0 days","status":"answered","citations":["f1"]}`, nil
				}
				t.Fatal("unexpected retry")
				return "", nil
			}
			e := NewEngine(s, m)
			out, err := e.AnswerEvidence(ctx, "u", "When did I arrive in Brookfield?", AnswerOptions{anchor, "UTC", true})
			if mode != "ok" {
				if err == nil || out.Answer != "" || len(out.Facts) > 0 || len(out.Citations) > 0 {
					t.Fatalf("private partial result escaped: %+v %v", out, err)
				}
				return
			}
			if err != nil || out.Calculation.Value != 0 || out.ModelCalls != 3 || len(out.Facts) != 1 || out.Facts[0].Quote != text || out.Facts[0].StartRune != 0 || out.Facts[0].EndRune != len(text) {
				t.Fatalf("answer %+v: %v", out, err)
			}
		})
	}
}

func TestAnswerCalculations(t *testing.T) {
	for _, tc := range []struct {
		from, to     string
		days, months int
	}{{"2024-02-28", "2024-03-01", 2, 0}, {"2022-10-22", "2023-03-25", 154, 5}, {"2026-01-31", "2026-02-28", 28, 0}, {"2023-03-25", "2022-10-22", -154, -5}} {
		d, e := CalendarDaysBetween(tc.from, tc.to)
		m, f := CalendarMonthsBetween(tc.from, tc.to)
		if e != nil || f != nil || d != tc.days || m != tc.months {
			t.Fatalf("%+v: %d %d %v %v", tc, d, m, e, f)
		}
	}
	facts := []EvidenceFact{{ID: "a", Date: "2026-04-02"}, {ID: "b", Date: "2026-04-06"}}
	for _, c := range []EvidenceCalculation{{Operation: "days", FromFact: "missing", ToFact: "b"}, {Operation: "count", EventFacts: []string{"a", "a"}}, {Operation: "count", EventFacts: []string{"foreign"}}, {Operation: "none", FromFact: "a"}, {Operation: "bad"}} {
		if err := calculateEvidence(&c, facts, AnswerOptions{}); err == nil {
			t.Fatalf("accepted invalid %+v", c)
		}
	}
	c := EvidenceCalculation{Operation: "days", FromFact: "a", ToFact: "b", Value: 1000}
	if err := calculateEvidence(&c, facts, AnswerOptions{}); err != nil || c.Value != 4 {
		t.Fatalf("model arithmetic accepted: %+v %v", c, err)
	}
}

func TestAnswerRankingBudgetAndSpeaker(t *testing.T) {
	anchor := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	obs := []Observation{{ID: "visit", Content: "I visited the History Museum on 2/8.", At: anchor, Refs: map[string]any{"sender": "user"}}, {ID: "future", Content: "I visited the History Museum on 2/8.", At: anchor.Add(time.Hour)}}
	for i := 0; i < 200; i++ {
		obs = append(obs, Observation{ID: fmt.Sprint(i), At: anchor, Content: strings.Repeat("Here are museums and galleries to visit. ", 40), Refs: map[string]any{"sender": "assistant"}})
	}
	x, sources, truncated, err := answerExcerpts(obs, []WindowHit{{Window: ObservationWindow{"visit", 0, 4, "I vi"}}}, []string{"museums February"}, "user", anchor)
	b, _ := json.Marshal(x)
	if err != nil || !truncated || len(b) > 32000 || len(x) > 80 || sources["visit"].ID != "visit" || sources["future"].ID != "" {
		t.Fatalf("budget or attribution: %d %v %v", len(b), truncated, err)
	}
	if !strings.Contains(string(b), "2/8") {
		t.Fatal("short semantic window hid the lexical date match")
	}
	date, rule, err := resolveAnswerDate("2/8", anchor, "UTC")
	if err != nil || date != "2026-02-08" || rule != "assumed_observation_year" {
		t.Fatalf("date %s %s %v", date, rule, err)
	}
}

func TestAnswerEndpointRejectsInvalidInput(t *testing.T) {
	api := NewAPI(NewEngine(nil, nil)).Routes()
	for _, body := range []string{`{}`, `{"user_id":"u","question":"q","timezone":"UTC"}`, `{"user_id":"u","question":"q","as_of":"2026-01-01T00:00:00Z","timezone":"Local"}`, `{"extra":true}`, `{} {}`} {
		r := httptest.NewRecorder()
		api.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/v1/evidence/answer", strings.NewReader(body)))
		if r.Code != 400 {
			t.Fatalf("%s: %d", body, r.Code)
		}
	}
}

func TestAnswerFormatRepairBoundAndIsolation(t *testing.T) {
	for _, valid := range []bool{true, false} {
		m := &answerModel{respond: func(call int, _ []llm.Message) (string, error) {
			if call == 1 {
				return `{"value":"unvalidated"} trailing`, nil
			}
			if valid {
				return `{}`, nil
			}
			return `still invalid`, nil
		}}
		e := NewEngine(nil, m)
		out := EvidenceAnswer{}
		var target struct {
			Value string `json:"value"`
		}
		err := e.answerCall(context.Background(), "Return JSON", map[string]string{"x": "test"}, &target, &out)
		if (err == nil) != valid || m.call != 2 || out.FormatRepairs != 1 || target.Value != "" {
			t.Fatalf("repair leaked partial fields or wrong bound: %+v %+v %v", target, out, err)
		}
		if err = e.answerCall(context.Background(), "Return JSON", map[string]string{"x": "test"}, &target, &out); !valid && (err == nil || m.call != 3) {
			t.Fatalf("repeated format repair: %v calls=%d", err, m.call)
		}
	}
}
