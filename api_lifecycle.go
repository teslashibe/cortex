package cortex

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

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
