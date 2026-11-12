package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// maxReplayReport caps a replay run's result: a status, a step and an
// error, plus the drawn inputs.
const maxReplayReport = 64 << 10

// getReplayCode gives a replay run the code of the revision it replays.
func (s *server) getReplayCode(w http.ResponseWriter, r *http.Request) {
	code, err := s.svc.ReplayCode(r.PathValue("token"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, code)
}

// putReplay takes bp-replay's result and ends the replay run with it.
func (s *server) putReplay(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReplayReport))
	dec.DisallowUnknownFields()
	var res runs.ReplayResult
	if err := dec.Decode(&res); err != nil {
		http.Error(w, "bad replay result", http.StatusBadRequest)
		return
	}
	if err := s.svc.FinishReplay(r.PathValue("token"), res, time.Now().UTC()); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// journeyItem is a journey as the board lists it: its revisions without
// their code (agent-written; the journey page shows it as text).
type journeyItem struct {
	store.Journey
	Revisions []revisionItem `json:"revisions"`
}

type revisionItem struct {
	N       int                `json:"n"`
	Kind    store.RevisionKind `json:"kind"`
	Reason  string             `json:"reason"`
	RunID   string             `json:"runId"`
	BugID   string             `json:"bugId,omitempty"`
	Created time.Time          `json:"created"`
}

// listJourneys lists journeys, optionally only one persona's or story's.
func (s *server) listJourneys(w http.ResponseWriter, r *http.Request) {
	persona, story := r.URL.Query().Get("persona"), r.URL.Query().Get("story")
	js, err := s.svc.Store.Journeys()
	if err != nil {
		fail(w, err)
		return
	}
	out := []journeyItem{}
	for _, j := range js {
		if (persona != "" && j.Persona != persona) || (story != "" && j.Story != story) {
			continue
		}
		revs, err := s.svc.Store.Revisions(j.ID)
		if err != nil {
			fail(w, err)
			return
		}
		item := journeyItem{Journey: j, Revisions: make([]revisionItem, 0, len(revs))}
		for _, rv := range revs {
			item.Revisions = append(item.Revisions, revisionItem{N: rv.N, Kind: rv.Kind, Reason: rv.Reason, RunID: rv.RunID, BugID: rv.BugID, Created: rv.Created})
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

// replayJourney starts a replay run of a journey revision (0 or absent: the
// live one) for the person in X-BP-Actor.
func (s *server) replayJourney(w http.ResponseWriter, r *http.Request) {
	if !mediaTypeIs(r, jsonType) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	var req struct {
		Revision int   `json:"revision"`
		Seed     int64 `json:"seed"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.Revision < 0 || req.Seed < 0 {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	actor := r.Header.Get("X-BP-Actor")
	if actor == "" {
		actor = "local"
	}
	run, err := s.svc.StartReplay(r.Context(), r.PathValue("id"), req.Revision, req.Seed, actor)
	switch {
	case errors.Is(err, runs.ErrStaleJourney):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil && run.ID != "" && !errors.Is(err, runs.ErrBusy):
		http.Error(w, "failed to launch the replay: "+err.Error(), http.StatusBadGateway)
		return
	case err != nil:
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, redact(run))
}
