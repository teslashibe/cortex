package cortex

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/teslashibe/cortex/prompts"
)

func TestConsolidationRoutes(t *testing.T) {
	api := NewAPI(NewEngine(nil, nil)).Routes()
	for _, path := range []string{"/v1/consolidate", "/v1/dream"} {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
}

func TestConsolidationContextOmitsHistoricalSourceIDs(t *testing.T) {
	memories := []Memory{{ID: "memory-to-reinforce", Kind: KindFact, Summary: "Known fact", Sources: []string{"historical-source-id"}, Confidence: .9}}
	messages, err := consolidationMessages("test", "owner", []Observation{{ID: "current-source-id", Content: "New support"}}, memories)
	if err != nil {
		t.Fatal(err)
	}
	text := messages[1].Content
	if strings.Contains(text, "historical-source-id") || strings.Contains(text, "current-source-id") || strings.Contains(text, "memory-to-reinforce") || !strings.Contains(text, `"id":"s1"`) || !strings.Contains(text, `"id":"m1"`) || !strings.Contains(text, "Known fact") {
		t.Fatalf("incorrect prompt projection: %s", text)
	}
	if len(memories[0].Sources) != 1 || memories[0].Sources[0] != "historical-source-id" {
		t.Fatal("stored provenance was mutated")
	}
}

func TestParseOps(t *testing.T) {
	valid := `{"ops":[{"kind":"insert","memory":{"kind":"preference","summary":"Likes tea","confidence":0.8},"sources":["obs_1"]}]}`
	for _, text := range []string{`{"ops":[]}`, valid, `{"ops":[{"kind":"reinforce","old_id":"mem_1","sources":["obs_1"]}]}`, `{"ops":[{"kind":"contradict","old_id":"mem_1","memory":{"kind":"preference","summary":"Likes coffee","confidence":0},"sources":["obs_1"],"reason":"Changed preference"}]}`} {
		if _, err := parseOps(text); err != nil {
			t.Errorf("valid response %s: %v", text, err)
		}
	}
	invalid := []string{
		``, `{}`, `null`, `[]`, `{"ops":null}`, `{"ops":{}}`, `{"ops":[]} {"ops":[]}`,
		"```json\n{\"ops\":[]}\n```", `{"ops":[],"extra":true}`, `{"Ops":[]}`, `{"ops":[],"ops":[]}`,
		`{"ops":[null]}`, `{"ops":[{}]}`, `{"ops":[{"kind":"decay"}]}`,
		`{"ops":[{"kind":"reinforce","old_id":"mem_1","sources":[]}]}`,
		`{"ops":[{"kind":"reinforce","old_id":"mem_1","sources":["obs_1","obs_1"]}]}`,
		`{"ops":[{"kind":"reinforce","old_id":"mem_1","sources":[""]}]}`,
		`{"ops":[{"kind":"reinforce","old_id":"mem_1","sources":["obs_1"],"memory":{}}]}`,
		`{"ops":[{"kind":"reinforce","old_id":"","sources":["obs_1"]}]}`,
		`{"ops":[{"kind":"edge","old_id":"mem_1","edge":{"target_id":"mem_2","edge_type":"semantic","weight":1}}]}`,
		strings.Replace(valid, `"confidence":0.8`, `"confidence":1.1`, 1),
		strings.Replace(valid, `"confidence":0.8`, `"confidence":-0.1`, 1),
		strings.Replace(valid, `,"confidence":0.8`, ``, 1),
		strings.Replace(valid, `"confidence":0.8`, `"confidence":null`, 1),
		strings.Replace(valid, `"confidence":0.8`, `"confidence":0.8,"Confidence":0.2`, 1),
		strings.Replace(valid, `"preference"`, `"unknown"`, 1),
		strings.Replace(valid, `"Likes tea"`, `" "`, 1),
		strings.Replace(valid, `"Likes tea"`, fmt.Sprintf("%q", strings.Repeat("x", MaxSummaryCharacters+1)), 1),
		strings.Replace(valid, `"sources"`, `"old_id":"","sources"`, 1),
		strings.Replace(valid, `"sources"`, `"user_id":"other","sources"`, 1),
		strings.Replace(valid, `"summary"`, `"Summary"`, 1),
	}
	for _, text := range invalid {
		if ops, err := parseOps(text); err == nil {
			t.Errorf("accepted invalid response %s: %+v", text, ops)
		}
	}
}

func TestChunkObservations(t *testing.T) {
	day := time.Date(2026, 9, 1, 23, 0, 0, 0, time.UTC)
	obs := make([]Observation, 6)
	for i := range obs {
		obs[i] = Observation{ID: fmt.Sprint(i), At: day.Add(time.Duration(i) * time.Hour)}
	}
	for _, tc := range []struct {
		size    int
		byDay   bool
		lengths []int
	}{
		{2, false, []int{2, 2, 2}}, {3, true, []int{1, 3, 2}}, {0, false, []int{6}}, {1, true, []int{1, 1, 1, 1, 1, 1}},
	} {
		chunks := chunkObservations(obs, tc.size, tc.byDay)
		if len(chunks) != len(tc.lengths) {
			t.Fatalf("chunks=%d want %v", len(chunks), tc.lengths)
		}
		index := 0
		for i, chunk := range chunks {
			if len(chunk) != tc.lengths[i] {
				t.Fatalf("chunk %d len=%d", i, len(chunk))
			}
			for _, o := range chunk {
				if o.ID != obs[index].ID {
					t.Fatal("changed observation order")
				}
				index++
			}
		}
		if index != len(obs) {
			t.Fatal("lost observations")
		}
	}
	if len(chunkObservations(nil, 2, true)) != 0 {
		t.Fatal("empty input produced chunks")
	}
}

func TestConsolidationContext(t *testing.T) {
	at := time.Date(2026, 9, 1, 1, 2, 3, 0, time.UTC)
	content := strings.Repeat("full observation ", 1000)
	obs := []Observation{{ID: "obs_1", UserID: "u", Source: "chat", Content: content, At: at, Refs: map[string]any{"sender": "assistant", "conversation_uuid": "chat_1"}}}
	mem := []Memory{{ID: "mem_1", UserID: "u", Kind: KindPreference, Summary: "Likes tea", BornAt: at, LastSeenAt: at, Sources: []string{"obs_old"}}}
	messages, err := consolidationMessages("test-model", "u", obs, mem)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != "system" || messages[0].Content != prompts.System || messages[1].Role != "user" {
		t.Fatalf("messages: %+v", messages)
	}
	var context struct {
		UserID       string        `json:"user_id"`
		Observations []Observation `json:"observations"`
		Memories     []Memory      `json:"memories"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(messages[1].Content, prompts.Consolidation+"\n")), &context); err != nil {
		t.Fatal(err)
	}
	if context.UserID != "u" || context.Observations[0].Content != content || context.Observations[0].Refs["sender"] != "assistant" || !context.Observations[0].At.Equal(at) || len(context.Memories[0].Sources) != 0 || context.Memories[0].ID != "m1" || context.Memories[0].Summary != "Likes tea" || !context.Memories[0].LastSeenAt.Equal(at) {
		t.Fatalf("lost evidence: %+v", context)
	}
	// The context fits by itself, but escaping it inside the chat envelope does not.
	obs[0].Content = strings.Repeat(`"`, MaxContextBytes/3)
	if _, err := consolidationMessages("test-model", "u", obs, mem); err == nil {
		t.Fatal("accepted context whose chat envelope exceeds the request limit")
	}
	obs[0].Content = strings.Repeat("x", MaxContextBytes)
	if _, err := consolidationMessages("test-model", "u", obs, mem); err == nil {
		t.Fatal("oversized context accepted")
	}
}

func TestConsolidationAliasesAreCollisionFreeAndRequestScoped(t *testing.T) {
	obs := []Observation{{ID: "s1", IdempotencyKey: "private-import-key"}, {ID: "real-second"}}
	mem := []Memory{{ID: "m1"}}
	o, m, _, _ := consolidationAliases(obs, mem)
	if o[0].ID == "s1" || m[0].ID == "m1" || o[0].IdempotencyKey != "" || obs[0].IdempotencyKey != "private-import-key" {
		t.Fatal("alias collision or input mutation")
	}
	ops, err := resolveConsolidationAliases([]Op{{Kind: OpReinforce, OldID: m[0].ID, Sources: []string{o[0].ID, "real-second"}}}, obs, mem)
	if err != nil || ops[0].OldID != "m1" || ops[0].Sources[0] != "s1" || ops[0].Sources[1] != "real-second" {
		t.Fatalf("bad resolution: %+v %v", ops, err)
	}
	for _, refs := range [][]string{{"foreign"}, {o[0].ID, "s1"}} {
		if _, err := resolveConsolidationAliases([]Op{{Sources: refs}}, obs, mem); err == nil {
			t.Fatal("accepted unknown or duplicate resolved source")
		}
	}
	if _, err := resolveConsolidationAliases([]Op{{OldID: "foreign", Sources: []string{o[0].ID}}}, obs, mem); err == nil {
		t.Fatal("accepted unknown update target")
	}
	other := []Observation{{ID: "different-source"}}
	resolved, err := resolveConsolidationAliases([]Op{{Sources: []string{"s1"}}}, other, nil)
	if err != nil || resolved[0].Sources[0] != "different-source" {
		t.Fatal("alias escaped its request")
	}
}

func TestObservationUserIsolation(t *testing.T) {
	in := ObservationInput{Sender: "human", Content: "Tea", At: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), ConversationUUID: "c"}
	a, b := in.ToObservation("a"), in.ToObservation("b")
	if a.ID == b.ID {
		t.Fatal("observation IDs collide across users")
	}
	if a.IdempotencyKey != b.IdempotencyKey {
		t.Fatal("legacy idempotency keys changed")
	}
	if a.IdempotencyKey != ObservationID(in.Sender, in.Content, in.At.Format(time.RFC3339Nano), in.ConversationUUID) {
		t.Fatal("legacy reimport key changed")
	}
	if in.ToObservation("a").ID != a.ID {
		t.Fatal("non-deterministic observation ID")
	}
}
