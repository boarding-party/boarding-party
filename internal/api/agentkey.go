package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// maxAgentKey caps an Anthropic key's length.
const maxAgentKey = 300

func (s *server) getAgentKey(w http.ResponseWriter, r *http.Request) {
	if s.svc.AgentKey == nil {
		http.NotFound(w, r)
		return
	}
	st, err := s.svc.AgentKey.Status(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *server) putAgentKey(w http.ResponseWriter, r *http.Request) {
	if s.svc.AgentKey == nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	k := body.Key
	if k == "" || len(k) > maxAgentKey || strings.ContainsAny(k, " \t\r\n") || !strings.HasPrefix(k, "sk-ant-") {
		jsonError(w, http.StatusBadRequest, "that doesn't look like an Anthropic API key (it starts with sk-ant-)")
		return
	}
	if err := s.svc.AgentKey.SetAPIKey(r.Context(), k); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
