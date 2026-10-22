package cortex

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

func (a *API) getObservationWindows(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := a.engine.RecallObservationWindows(r.Context(), r.URL.Query().Get("user"), r.URL.Query().Get("q"), RecallOptions{Limit: limit, LexicalOnly: r.URL.Query().Get("mode") == "lexical"})
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) postEvidenceRecall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User        string   `json:"user_id"`
		Queries     []string `json:"queries"`
		Limit       int      `json:"limit"`
		LexicalOnly bool     `json:"lexical_only"`
	}
	if err := decodeBounded(w, r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	out, err := a.engine.RecallEvidence(r.Context(), body.User, body.Queries, RecallOptions{Limit: body.Limit, LexicalOnly: body.LexicalOnly})
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) postEvidenceAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User     string `json:"user_id"`
		Question string `json:"question"`
		AnswerOptions
	}
	if err := decodeBounded(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := a.engine.AnswerEvidence(r.Context(), body.User, body.Question, body.AnswerOptions)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (a *API) postEventDates(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User       string      `json:"user_id"`
		Timezone   string      `json:"timezone"`
		Candidates []Candidate `json:"candidates"`
	}
	if err := decodeBounded(w, r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	out, err := a.engine.ResolveEventDates(r.Context(), body.User, body.Timezone, body.Candidates)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"dates": out})
}
func (a *API) postObservationWindowIndex(w http.ResponseWriter, r *http.Request) {
	out, err := a.engine.IndexObservationWindows(r.Context(), r.URL.Query().Get("user"), 1000)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) postClassify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User       string      `json:"user_id"`
		Candidates []Candidate `json:"candidates"`
	}
	if err := decodeBounded(w, r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	out, err := a.engine.ClassifyCandidates(r.Context(), body.User, body.Candidates)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) getObservationRecall(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := a.engine.RecallObservations(r.Context(), r.URL.Query().Get("user"), r.URL.Query().Get("q"), RecallOptions{Limit: limit, LexicalOnly: r.URL.Query().Get("mode") == "lexical"})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) postObservationIndex(w http.ResponseWriter, r *http.Request) {
	out, err := a.engine.IndexObservations(r.Context(), r.URL.Query().Get("user"), 1000)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func decodeBounded(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
func (a *API) postFeedback(w http.ResponseWriter, r *http.Request) {
	var f Feedback
	if err := decodeBounded(w, r, &f); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := a.engine.Feedback(r.Context(), f); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
func (a *API) postIndex(w http.ResponseWriter, r *http.Request) {
	out, err := a.engine.Index(r.Context(), r.URL.Query().Get("user"), 1000)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) forgetObservation(w http.ResponseWriter, r *http.Request) {
	out, err := a.engine.ForgetObservation(r.Context(), r.URL.Query().Get("user"), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (a *API) postMaintain(w http.ResponseWriter, r *http.Request) {
	out, err := a.engine.Maintain(r.Context(), r.URL.Query().Get("user"), time.Now().UTC(), 1000)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) putRetention(w http.ResponseWriter, r *http.Request) {
	var retention Retention
	if err := decodeBounded(w, r, &retention); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := a.engine.SetRetention(r.Context(), r.URL.Query().Get("user"), r.PathValue("id"), retention); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
func (a *API) forgetMemory(w http.ResponseWriter, r *http.Request) {
	out, err := a.engine.Forget(r.Context(), r.URL.Query().Get("user"), r.PathValue("id"))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) getEvidence(w http.ResponseWriter, r *http.Request) {
	user, id := r.URL.Query().Get("user"), r.URL.Query().Get("memory")
	if user == "" || id == "" {
		writeErr(w, 400, errors.New("user and memory required"))
		return
	}
	out, err := a.engine.Evidence(r.Context(), user, id)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"observations": out})
}
