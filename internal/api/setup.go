package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/boarding-party/boardingparty/internal/runs"
)

// getSetup says whether the project still needs its first-run setup, and
// the staging URL the cloud gave it.
func (s *server) getSetup(w http.ResponseWriter, r *http.Request) {
	needed, err := s.svc.SetupNeeded()
	if err != nil {
		fail(w, err)
		return
	}
	staging := ""
	if s.svc.StagingURL != nil {
		staging = s.svc.StagingURL(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{"needed": needed, "stagingURL": staging})
}

func (s *server) postSetup(w http.ResponseWriter, r *http.Request) {
	if !mediaTypeIs(r, jsonType) {
		jsonError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}
	var req runs.SetupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	mailbox := ""
	if s.mail != nil {
		c, ok, err := s.mail.Secrets.Load(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		if ok {
			mailbox = c.Username
		}
	}
	err := s.svc.Setup(r.Context(), req, mailbox)
	if errors.Is(err, runs.ErrSetUp) {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
