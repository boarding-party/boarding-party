package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// maxActor caps the X-BP-Actor header (an email address).
const maxActor = 254

// auditActions names the board actions worth recording, by route pattern.
// The subject is the route's {id}, {name} or {persona}; request bodies are
// never recorded, so no secret reaches the log.
var auditActions = map[string]string{
	"POST /api/project":                      "project.save",
	"PUT /api/project":                       "project.save",
	"POST /api/runs":                         "run.start",
	"POST /api/runs/{id}/stop":               "run.stop",
	"PUT /api/pause":                         "pause.set",
	"DELETE /api/queue/{id}":                 "queue.delete",
	"PUT /api/files/{name}":                  "file.save",
	"DELETE /api/files/{name}":               "file.delete",
	"PUT /api/agent-key":                     "agentkey.save",
	"PUT /api/mailbox":                       "mailbox.save",
	"DELETE /api/mailbox":                    "mailbox.delete",
	"DELETE /api/browser-profiles/{persona}": "profile.delete",
	"POST /api/issues/{id}/status":           "issue.triage",
	"POST /api/leads/{persona}/wake":         "lead.wake",
	"PUT /api/children/{name}":               "child.adopt",
	"DELETE /api/children/{name}":            "child.delete",
}

func validActor(a string) bool {
	if len(a) > maxActor {
		return false
	}
	for _, r := range a {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// withActor checks X-BP-Actor and records successful board actions in the
// audit log under that actor ("local" when absent: the CLI's own board).
func (s *server) withActor(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := r.Header.Get("X-BP-Actor")
		if !validActor(actor) {
			jsonError(w, http.StatusBadRequest, "bad X-BP-Actor header")
			return
		}
		if actor == "" {
			actor = "local"
		}
		_, pattern := mux.Handler(r)
		action, ok := auditActions[pattern]
		if !ok {
			mux.ServeHTTP(w, r)
			return
		}
		rec := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		mux.ServeHTTP(rec, r)
		if rec.code < 200 || rec.code > 299 {
			return
		}
		if err := s.svc.Store.Audit(actor, action, subjectOf(pattern, r), time.Now()); err != nil {
			log.Printf("api: audit %s: %v", action, err)
		}
	})
}

// subjectOf returns the value the mux bound to the pattern's wildcard, the
// same value the handler read; ServeMux sets it on r while serving.
func subjectOf(pattern string, r *http.Request) string {
	i := strings.IndexByte(pattern, '{')
	j := strings.IndexByte(pattern, '}')
	if i < 0 || j < i {
		return ""
	}
	return r.PathValue(strings.TrimSuffix(pattern[i+1:j], "..."))
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *server) getAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			jsonError(w, http.StatusBadRequest, "limit must be a positive number")
			return
		}
		limit = min(n, 500)
	}
	out, err := s.svc.Store.AuditLog(limit)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
