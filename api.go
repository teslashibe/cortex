package cortex

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/teslashibe/cortex/prompts"
)

type API struct {
	engine *Engine
}

func NewAPI(e *Engine) *API { return &API{engine: e} }

func (a *API) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("POST /v1/observations", a.postObservations)
	mux.HandleFunc("GET /v1/recall", a.getRecall)
	mux.HandleFunc("GET /v1/observations/recall", a.getObservationRecall)
	mux.HandleFunc("GET /v1/observations/windows", a.getObservationWindows)
	mux.HandleFunc("POST /v1/observations/windows/index", a.postObservationWindowIndex)
	mux.HandleFunc("POST /v1/classify", a.postClassify)
	mux.HandleFunc("POST /v1/evidence/recall", a.postEvidenceRecall)
	mux.HandleFunc("POST /v1/evidence/dates", a.postEventDates)
	mux.HandleFunc("POST /v1/evidence/answer", a.postEvidenceAnswer)
	mux.HandleFunc("POST /v1/observations/index", a.postObservationIndex)
	mux.HandleFunc("DELETE /v1/observations/{id}", a.forgetObservation)
	mux.HandleFunc("POST /v1/consolidate", a.postConsolidate)
	mux.HandleFunc("POST /v1/dream", a.postConsolidate) // legacy route alias
	mux.HandleFunc("GET /v1/profile/{user}", a.getProfile)
	mux.HandleFunc("POST /v1/signals", a.postSignals)
	mux.HandleFunc("POST /v1/feedback", a.postFeedback)
	mux.HandleFunc("POST /v1/index", a.postIndex)
	mux.HandleFunc("POST /v1/maintain", a.postMaintain)
	mux.HandleFunc("PUT /v1/memories/{id}/retention", a.putRetention)
	mux.HandleFunc("DELETE /v1/memories/{id}", a.forgetMemory)
	mux.HandleFunc("GET /v1/evidence", a.getEvidence)
	return mux
}

func (a *API) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "prompt_version": prompts.Version})
}

// ObservationInput is the wire shape for POST /v1/observations.
// Shared between the API and cortex-import; one source of truth keeps the
// JSON schema in sync across processes.
type ObservationInput struct {
	Sender           string         `json:"sender"`
	Source           string         `json:"source"`
	Content          string         `json:"content"`
	At               time.Time      `json:"at"`
	ConversationUUID string         `json:"conversation_uuid"`
	ConversationName string         `json:"conversation_name"`
	Refs             map[string]any `json:"refs"`
	ToolName         string         `json:"tool_name,omitempty"`
	IntentID         string         `json:"intent_id,omitempty"`
	OutcomeClass     string         `json:"outcome_class,omitempty"`
	TurnIndex        *int           `json:"turn_index,omitempty"`
}

// ToObservation builds the canonical internal Observation, computing the
// deterministic ID + idempotency key from (sender, content, at, conversation).
func (p ObservationInput) ToObservation(userID string) Observation {
	atStr := p.At.UTC().Format(time.RFC3339Nano)
	id := ObservationID(userID, ObservationID(p.Sender, p.Content, atStr, p.ConversationUUID), "", "")
	refs := p.Refs
	if refs == nil {
		refs = map[string]any{}
	}
	if p.Sender != "" {
		refs["sender"] = p.Sender
	}
	if p.ConversationUUID != "" {
		refs["conversation_uuid"] = p.ConversationUUID
	}
	if p.ConversationName != "" {
		refs["conversation_name"] = p.ConversationName
	}
	source := p.Source
	if source == "" {
		source = "api"
	}
	return Observation{
		ID:             id,
		UserID:         userID,
		Source:         source,
		Content:        p.Content,
		Refs:           refs,
		At:             p.At.UTC(),
		IdempotencyKey: ObservationID(p.Sender, p.Content, atStr, p.ConversationUUID),
		ToolName:       p.ToolName, IntentID: p.IntentID, OutcomeClass: p.OutcomeClass, TurnIndex: p.TurnIndex,
	}
}

type observationsReq struct {
	UserID       string             `json:"user_id"`
	Observations []ObservationInput `json:"observations"`
}

func (a *API) postObservations(w http.ResponseWriter, r *http.Request) {
	var body observationsReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("decode: %w", err))
		return
	}
	if body.UserID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("user_id required"))
		return
	}
	obs := make([]Observation, 0, len(body.Observations))
	for _, p := range body.Observations {
		if p.Content == "" {
			continue
		}
		obs = append(obs, p.ToObservation(body.UserID))
	}
	inserted, err := a.engine.Observe(r.Context(), obs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"received": len(obs), "inserted": inserted})
}

func (a *API) getRecall(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user")
	q := r.URL.Query().Get("q")
	if userID == "" || q == "" {
		writeErr(w, http.StatusBadRequest, errors.New("user and q required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := a.engine.RecallWithOptions(r.Context(), userID, q, RecallOptions{Limit: limit, LexicalOnly: r.URL.Query().Get("mode") == "lexical"})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) postConsolidate(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user")
	if userID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("user required"))
		return
	}
	result, err := a.engine.Consolidate(r.Context(), userID, ConsolidationOptions{})
	if errors.Is(err, ErrNoObservations) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "noop", "reason": err.Error()})
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrConsolidationBusy) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": err.Error(), "result": result})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) getProfile(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user")
	if userID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("user required"))
		return
	}
	tier := r.URL.Query().Get("tier")
	if tier == "" {
		tier = "public"
	}
	if tier != "public" && tier != "internal" {
		writeErr(w, http.StatusBadRequest, errors.New("tier must be public|internal"))
		return
	}
	mems, err := a.engine.Memories(r.Context(), userID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	visible := filterMemoriesByTier(mems, tier)
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, map[string]any{
			"user":       userID,
			"tier":       tier,
			"profile_md": renderProfile(userID, tier, mems),
			"memories":   visible,
		})
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(renderProfile(userID, tier, mems)))
}

// filterMemoriesByTier applies the same public-tier policy used by
// renderProfile so the JSON payload never leaks relationship/sensitive
// memories that the markdown view hides.
func filterMemoriesByTier(mems []Memory, tier string) []Memory {
	if tier != "public" {
		return mems
	}
	out := make([]Memory, 0, len(mems))
	for _, m := range mems {
		if m.Kind == KindRelationship || sensitiveSummaryRE.MatchString(m.Summary) {
			continue
		}
		out = append(out, m)
	}
	return out
}

type signalReq struct {
	UserID      string         `json:"user_id"`
	MemoryID    string         `json:"memory_id"`
	SignalType  string         `json:"signal_type"`
	SignalValue float64        `json:"signal_value"`
	Metadata    map[string]any `json:"metadata"`
}

func (a *API) postSignals(w http.ResponseWriter, r *http.Request) {
	var body signalReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("decode: %w", err))
		return
	}
	if body.UserID == "" || body.SignalType == "" {
		writeErr(w, http.StatusBadRequest, errors.New("user_id and signal_type required"))
		return
	}
	now := time.Now().UnixNano()
	if err := a.engine.WriteSignal(r.Context(), Signal{
		ID:          fmt.Sprintf("sig_%s_%d", body.UserID, now),
		UserID:      body.UserID,
		MemoryID:    body.MemoryID,
		SignalType:  body.SignalType,
		SignalValue: body.SignalValue,
		Metadata:    body.Metadata,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// renderProfile produces the markdown profile served by GET /v1/profile/:user.
// Pure formatter — no LLM round-trip on read.
func renderProfile(userID, tier string, mems []Memory) string {
	if len(mems) == 0 {
		return fmt.Sprintf("# Profile: %s\n\n_No memories yet. Consolidate observations to create memories._\n", userID)
	}
	visible := filterMemoriesByTier(mems, tier)
	sort.SliceStable(visible, func(i, j int) bool {
		if visible[i].Kind != visible[j].Kind {
			return kindOrder(visible[i].Kind) < kindOrder(visible[j].Kind)
		}
		if visible[i].Confidence != visible[j].Confidence {
			return visible[i].Confidence > visible[j].Confidence
		}
		return visible[i].LastSeenAt.After(visible[j].LastSeenAt)
	})
	var b strings.Builder
	fmt.Fprintf(&b, "# Profile: %s\n\n", userID)
	fmt.Fprintf(&b, "_Tier: %s. %d memories shown._\n\n", tier, len(visible))
	current := Kind("")
	for _, m := range visible {
		if m.Kind != current {
			current = m.Kind
			fmt.Fprintf(&b, "## %s\n\n", titleForKind(m.Kind))
		}
		fmt.Fprintf(&b, "- %s _(confidence %.2f, reinforced ×%d, last seen %s)_\n",
			m.Summary, m.Confidence, m.ReinforcedCount, m.LastSeenAt.UTC().Format(time.DateOnly))
	}
	return b.String()
}

var kindMeta = map[Kind]struct {
	order int
	title string
}{
	KindProject:      {0, "Projects"},
	KindBehavior:     {1, "Behaviours"},
	KindPreference:   {2, "Preferences"},
	KindRoutine:      {3, "Routines"},
	KindRelationship: {4, "Relationships"},
	KindFact:         {5, "Facts"},
}

func kindOrder(k Kind) int { return kindMeta[k].order }
func titleForKind(k Kind) string {
	if m, ok := kindMeta[k]; ok {
		return m.title
	}
	return string(k)
}
