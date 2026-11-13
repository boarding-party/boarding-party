package api

import (
	"encoding/json"
	"net/http"

	"github.com/boarding-party/boardingparty/internal/store"
)

// getJourney returns a journey with its revisions (code included, shown
// only as text) and every decision about it.
func (s *server) getJourney(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, err := s.svc.Store.Journey(id)
	if err != nil {
		fail(w, err)
		return
	}
	revs, err := s.svc.Store.Revisions(id)
	if err != nil {
		fail(w, err)
		return
	}
	ds, err := s.svc.Store.Decisions(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Journey   store.Journey    `json:"journey"`
		Revisions []store.Revision `json:"revisions"`
		Decisions []store.Decision `json:"decisions"`
	}{j, revs, ds})
}

func (s *server) pauseJourney(w http.ResponseWriter, r *http.Request) {
	s.journeyAction(w, s.svc.PauseJourney(r.PathValue("id"), actorOf(r)))
}

func (s *server) resumeJourney(w http.ResponseWriter, r *http.Request) {
	s.journeyAction(w, s.svc.ResumeJourney(r.PathValue("id"), actorOf(r)))
}

func (s *server) deleteJourney(w http.ResponseWriter, r *http.Request) {
	s.journeyAction(w, s.svc.DeleteJourney(r.PathValue("id"), actorOf(r)))
}

func (s *server) journeyAction(w http.ResponseWriter, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rollbackJourney makes an older full revision's code live again.
func (s *server) rollbackJourney(w http.ResponseWriter, r *http.Request) {
	if !mediaTypeIs(r, jsonType) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	var req struct {
		Revision int `json:"revision"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	rev, err := s.svc.RollbackJourney(r.PathValue("id"), req.Revision, actorOf(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}
