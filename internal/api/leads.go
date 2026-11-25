package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// leadLogShown is how many decisions the lead page shows.
const leadLogShown = 50

// childView is a child with what its runs found, for the lead page.
type childView struct {
	store.Child
	Runs int `json:"runs"`
	Bugs int `json:"bugs"`
	// LoginWait is the kind of user the child's newest run waits for a
	// login for, and LoginRun that run; empty when it waits for none.
	LoginWait string `json:"loginWait,omitempty"`
	LoginRun  string `json:"loginRun,omitempty"`
}

func (s *server) getLead(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("persona")
	p, err := s.svc.Store.Project()
	if err != nil {
		fail(w, err)
		return
	}
	pe, _, ok := p.Persona(name)
	if !ok || !pe.IsLead() {
		http.Error(w, "not a lead", http.StatusNotFound)
		return
	}
	u, err := s.svc.LeadUsage(p, name, time.Now())
	if err != nil {
		fail(w, err)
		return
	}
	cs, err := s.svc.Store.Children(name)
	if err != nil {
		fail(w, err)
		return
	}
	recent, err := s.svc.Store.Runs(1000)
	if err != nil {
		fail(w, err)
		return
	}
	children := make([]childView, 0, len(cs))
	for _, c := range cs {
		v := childView{Child: c}
		for _, run := range recent {
			if run.Persona != c.Name {
				continue
			}
			if v.Runs == 0 && run.State == store.RunWaiting { // runs are newest first
				wt, err := s.svc.Store.WaitFor(run.ID)
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					fail(w, err)
					return
				}
				if err == nil && wt.Kind == "login" && wt.Status == store.WaitOpen {
					v.LoginWait, v.LoginRun = wt.Login, run.ID
				}
			}
			v.Runs++
			bugs, err := s.svc.Store.Bugs(run.ID)
			if err != nil {
				fail(w, err)
				return
			}
			v.Bugs += len(bugs)
		}
		children = append(children, v)
	}
	log, err := s.svc.Store.LeadLog(name, leadLogShown)
	if err != nil {
		fail(w, err)
		return
	}
	if log == nil {
		log = []store.LeadAction{}
	}
	ask, err := s.svc.OpenAsk(name)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lead": pe, "usage": u, "children": children, "log": log, "ask": ask})
}

// raiseLead is the lead page's answer to an ask: it raises one limit.
func (s *server) raiseLead(w http.ResponseWriter, r *http.Request) {
	var a runs.Ask
	if !mediaTypeIs(r, jsonType) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&a); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.svc.RaiseLeadLimit(r.Context(), r.PathValue("persona"), a); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// declineLead is "Keep at" on a lead's ask: the owner says no to raising
// the limit it names.
func (s *server) declineLead(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Limit string `json:"limit"`
	}
	if !mediaTypeIs(r, jsonType) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.svc.DeclineAsk(r.PathValue("persona"), in.Limit, time.Now().UTC()); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// wakeLead is "Run now" on the lead page.
func (s *server) wakeLead(w http.ResponseWriter, r *http.Request) {
	q, err := s.svc.WakeLead(r.PathValue("persona"), "Run from the board", time.Now().UTC())
	if errors.Is(err, runs.ErrInvalid) {
		http.Error(w, "not a lead", http.StatusNotFound)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	if s.svc.OnFinish != nil {
		s.svc.OnFinish() // wake the scheduler
	}
	writeJSON(w, http.StatusAccepted, q)
}

// adoptChild saves the user's edit of a child, which makes it hand-made.
func (s *server) adoptChild(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Persona project.Persona `json:"persona"`
		Stories []project.Story `json:"stories"`
	}
	if !mediaTypeIs(r, jsonType) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.svc.Adopt(r.Context(), r.PathValue("name"), req.Persona, req.Stories); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deleteChild(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Store.DeleteChild(r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
