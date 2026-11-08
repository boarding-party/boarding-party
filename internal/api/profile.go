package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
)

// getProfile serves the browser profile the run's browser starts with; 204
// when it starts empty.
func (s *server) getProfile(w http.ResponseWriter, r *http.Request) {
	state, _, err := s.svc.Profile(r.PathValue("token"))
	if err != nil {
		fail(w, err)
		return
	}
	if state == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(state)
}

// putProfile keeps the run's browser storage state for the persona's next run.
func (s *server) putProfile(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, runs.MaxProfileBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "unreadable body", http.StatusBadRequest)
		}
		return
	}
	if err := s.svc.SaveProfile(r.PathValue("token"), b, time.Now()); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// isProfileName matches the names Playwright gives a saved storage state, and
// the one the prompt asks for, so a profile never lands among the artifacts.
func isProfileName(name string) bool {
	n := strings.ToLower(name)
	return n == "profile.json" || strings.HasPrefix(n, "storage-state")
}

// getBrowserProfile says when the persona's browser was last saved; 404 when
// nothing is saved. The cookies themselves never leave the director.
func (s *server) getBrowserProfile(w http.ResponseWriter, r *http.Request) {
	state, saved, err := s.svc.Store.PersonaProfile(r.PathValue("persona"))
	if err != nil {
		fail(w, err)
		return
	}
	if state == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Saved time.Time `json:"saved"`
	}{saved})
}

// deleteBrowserProfile forgets the persona's browser, so its next run starts empty.
func (s *server) deleteBrowserProfile(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Store.DeletePersonaProfile(r.PathValue("persona")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
