package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

func (s *server) listIssues(w http.ResponseWriter, _ *http.Request) {
	is, err := s.svc.Store.Issues()
	if err != nil {
		fail(w, err)
		return
	}
	if is == nil {
		is = []store.Issue{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": is})
}

func (s *server) getIssue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	is, err := s.svc.Store.Issue(id)
	if err != nil {
		fail(w, err)
		return
	}
	reports, err := s.svc.Store.IssueReports(id)
	if err != nil {
		fail(w, err)
		return
	}
	if reports == nil {
		reports = []store.Report{}
	}
	checkBy, err := s.svc.CheckBy(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issue": is, "reports": reports, "checkBy": checkBy})
}

// putIssueStatus records the user's triage; fixed queues a check of the fix.
func (s *server) putIssueStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status store.IssueStatus `json:"status"`
		Reason string            `json:"reason"`
	}
	if !mediaTypeIs(r, jsonType) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	err := s.svc.SetIssueStatus(r.PathValue("id"), req.Status, req.Reason, time.Now().UTC())
	if errors.Is(err, runs.ErrInvalid) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	if req.Status == store.IssueFixed && s.svc.OnFinish != nil {
		s.svc.OnFinish() // a check may be queued: wake the scheduler
	}
	w.WriteHeader(http.StatusNoContent)
}

// reportOf reads the {"report": "<bug id>"} body of the make main and split
// routes; false when it wrote an error.
func reportOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Report string `json:"report"`
	}
	if !mediaTypeIs(r, jsonType) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return "", false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return "", false
	}
	return req.Report, true
}

// putIssueMain makes one of the issue's reports the one it is shown by.
func (s *server) putIssueMain(w http.ResponseWriter, r *http.Request) {
	report, ok := reportOf(w, r)
	if !ok {
		return
	}
	if err := s.svc.Store.SetMain(r.PathValue("id"), report); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// splitIssue moves one report into a new issue and returns its id.
func (s *server) splitIssue(w http.ResponseWriter, r *http.Request) {
	report, ok := reportOf(w, r)
	if !ok {
		return
	}
	id, err := s.svc.Store.SplitReport(r.PathValue("id"), report, time.Now().UTC())
	if errors.Is(err, store.ErrLastReport) {
		http.Error(w, "an issue with one report cannot be split", http.StatusConflict)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}
