package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/sched"
	"github.com/boarding-party/boardingparty/internal/store"
)

// maxReplayReport caps a replay run's result: a status, a step and an
// error, plus the drawn inputs.
const maxReplayReport = 64 << 10

// maxArrivalLinks caps the links of a message a resumed replay is given.
const maxArrivalLinks = 50

// journeyFetch is what a replay run fetches: the code of the revision it
// replays and, for a continuation, where to pick up.
type journeyFetch struct {
	Code   string       `json:"code"`
	Resume *resumeFetch `json:"resume,omitempty"`
	// Today is the day in London the replay's chain began: dates drawn
	// relative to today stay the same when a replay resumes after a wait.
	Today string `json:"today"`
}

type resumeFetch struct {
	After    int            `json:"after"`
	Arrivals []arrivalFetch `json:"arrivals"`
}

type arrivalFetch struct {
	Detail  string        `json:"detail"`
	Message *messageFetch `json:"message,omitempty"`
}

type messageFetch struct {
	From    string   `json:"from"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	Links   []string `json:"links"`
}

var textURL = regexp.MustCompile(`https?://[^\s<>"'()]+`)

// getReplayCode gives a replay run the code of the revision it replays,
// and its resume if it continues after a wait.
func (s *server) getReplayCode(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	code, err := s.svc.ReplayCode(token)
	if err != nil {
		fail(w, err)
		return
	}
	run, err := s.svc.ReplayRun(token)
	if err != nil {
		fail(w, err)
		return
	}
	out := journeyFetch{Code: code, Today: s.chainDay(run)}
	var c store.RunContext
	if json.Unmarshal(run.Context, &c) == nil && c.Resume != nil {
		out.Resume = &resumeFetch{After: c.Resume.After, Arrivals: []arrivalFetch{}}
		for _, a := range c.Resume.Arrivals {
			af := arrivalFetch{Detail: a.Detail}
			if a.MessageID != "" {
				m, err := s.svc.Store.Message(a.MessageID)
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					fail(w, err)
					return
				}
				if err == nil && m.Persona == run.Persona {
					af.Message = &messageFetch{From: m.From, Subject: m.Subject, Text: m.Text, Links: messageLinks(m)}
				}
			}
			out.Resume.Arrivals = append(out.Resume.Arrivals, af)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// london is the zone generated dates are relative to.
var london = func() *time.Location {
	if l, err := time.LoadLocation("Europe/London"); err == nil {
		return l
	}
	return time.UTC
}()

// chainDay is the London date the run's chain began: its first run's start.
func (s *server) chainDay(run store.Run) string {
	began := run.Started
	if run.Chain != "" {
		if rs, err := s.svc.Store.RunsInChain(run.Chain); err == nil && len(rs) > 0 && rs[0].Started.Before(began) {
			began = rs[0].Started
		}
	}
	return began.In(london).Format(time.DateOnly)
}

// postClaim spends a replay run's one-time claim and gives its pod the
// paths it works with, each carrying the run token. The pod's command holds
// only the claim, spent before the journey starts.
func (s *server) postClaim(w http.ResponseWriter, r *http.Request) {
	run, err := s.svc.Claim(r.PathValue("code"))
	if err != nil {
		fail(w, err)
		return
	}
	t := run.Token
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"journey": "/journey/" + t, "report": "/replay/" + t,
		"upload": "/artifacts/" + t + "/", "profile": "/profile/" + t, "live": "/live/" + t, "console": "/console/" + t})
}

// messageLinks is every http(s) link of a message, from its HTML and its
// text, in order and without repeats, at most maxArrivalLinks.
func messageLinks(m store.Message) []string {
	out, seen := []string{}, map[string]bool{}
	add := func(u string) {
		u = strings.TrimRight(u, ".,;:!?")
		if seen[u] || len(out) >= maxArrivalLinks || !(strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	for _, u := range m.Links {
		add(u)
	}
	for _, u := range textURL.FindAllString(m.Text, -1) {
		add(u)
	}
	return out
}

// putReplay takes bp-replay's result and ends the replay run with it, or
// parks it at its journey's wait. A wait the director cannot keep ends the
// replay as failed, with why.
func (s *server) putReplay(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReplayReport))
	dec.DisallowUnknownFields()
	var res runs.ReplayResult
	if err := dec.Decode(&res); err != nil {
		http.Error(w, "bad replay result", http.StatusBadRequest)
		return
	}
	token, now := r.PathValue("token"), time.Now().UTC()
	if res.Status == "waiting" {
		wait, outcome, why, err := s.replayWait(token, res, now)
		if err != nil {
			fail(w, err)
			return
		}
		if why == "" {
			err = s.svc.FinishReplayWaiting(token, res, wait, outcome, now)
		} else {
			res.Status, res.Error, res.Wait = "failed", "bp.waitFor: "+why, nil
			err = s.svc.FinishReplay(token, res, now)
		}
		if err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.svc.FinishReplay(token, res, now); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// replayWait checks a waiting replay's wait and returns it with the run's
// outcome, or why it cannot be kept. An email wait begins when the replay
// began, so mail that came while it ran still counts.
func (s *server) replayWait(token string, res runs.ReplayResult, now time.Time) (store.Wait, string, string, error) {
	run, err := s.svc.ReplayRun(token)
	if err != nil {
		return store.Wait{}, "", "", err
	}
	if res.Wait == nil || res.Wait.N < 1 {
		return store.Wait{}, "", "", fmt.Errorf("%w: a waiting result needs its wait", runs.ErrBadReplay)
	}
	var u sched.Until
	dec := json.NewDecoder(bytes.NewReader(res.Wait.Until))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&u); err != nil {
		return store.Wait{}, "", "the wait is not one a journey can make: " + err.Error(), nil
	}
	if u.Handoff != nil {
		return store.Wait{}, "", "a replay cannot wait for a hand-off", nil
	}
	p, err := s.svc.Store.Project()
	if err != nil {
		return store.Wait{}, "", "", err
	}
	if u.Email != nil {
		u.Email.AfterID = ""
		if !personaHasEmail(p, run) {
			return store.Wait{}, "", "this persona has no email address, so it can't wait for email", nil
		}
	}
	wait, err := sched.ParseWait(u, p, run, now)
	if err != nil {
		return store.Wait{}, "", err.Error(), nil
	}
	if wait.Kind == "email" {
		wait.Began = run.Started
	}
	if wait.Kind == "time" {
		return wait, "Waiting until " + sched.Describe(wait), "", nil
	}
	return wait, "Waiting for " + sched.Describe(wait), "", nil
}

func personaHasEmail(p project.Project, r store.Run) bool {
	for _, role := range p.Roles {
		for _, pe := range role.Personas {
			if role.Name == r.Role && pe.Name == r.Persona {
				return pe.Identity["email"] != ""
			}
		}
	}
	return false
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

// replayJourney queues a replay of a journey revision (0 or absent: the
// live one when it starts) for the person in X-BP-Actor.
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
	q, err := s.svc.EnqueueReplay(r.PathValue("id"), req.Revision, req.Seed, actorOf(r), time.Now().UTC())
	switch {
	case errors.Is(err, runs.ErrStaleJourney):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil && q.ID == "":
		fail(w, err)
		return
	case err != nil:
		log.Printf("replay of journey %s queued; recording the decision: %v", q.JourneyID, err)
	}
	if s.svc.OnFinish != nil {
		s.svc.OnFinish() // wake the scheduler
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": q})
}

// actorOf is who acts on the board: X-BP-Actor, or "local" for the CLI's
// own board.
func actorOf(r *http.Request) string {
	if a := r.Header.Get("X-BP-Actor"); a != "" {
		return a
	}
	return "local"
}
