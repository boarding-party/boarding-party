// Package api exposes the director over HTTP.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/live"
	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// MaxArtifactBytes is the default per-artifact upload limit (500 MB).
const MaxArtifactBytes int64 = 500 << 20

// MaxFrameBytes caps one live frame.
const MaxFrameBytes = 512 << 10

type server struct {
	svc      *runs.Service
	maxBytes int64
	hub      *live.Hub

	// Live stream limits; fields so tests can shrink them.
	liveMax   time.Duration // longest a single stream stays open
	liveCheck time.Duration // how often the run is re-checked for activity
}

func newServer(svc *runs.Service, maxBytes int64, hub *live.Hub) *server {
	return &server{svc: svc, maxBytes: normalise(maxBytes), hub: hub,
		liveMax: 15 * time.Minute, liveCheck: 5 * time.Second}
}

func normalise(maxBytes int64) int64 {
	if maxBytes <= 0 {
		return MaxArtifactBytes
	}
	return maxBytes
}

// Admin is the operator API (/api/*). Serve it on a loopback-only listener;
// workers must never be able to reach it. Every request needs
// "Authorization: Bearer <token>"; an empty token refuses all requests.
func Admin(svc *runs.Service, maxBytes int64, token string, hub *live.Hub) http.Handler {
	_, h := admin(svc, maxBytes, token, hub)
	return h
}

func admin(svc *runs.Service, maxBytes int64, token string, hub *live.Hub) (*server, http.Handler) {
	s := newServer(svc, maxBytes, hub)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/project", s.putProject)
	mux.HandleFunc("PUT /api/project", s.putProjectJSON)
	mux.HandleFunc("GET /api/project", s.getProject)
	mux.HandleFunc("GET /api/profile-fields", s.getProfileFields)
	mux.HandleFunc("POST /api/runs", s.startRun)
	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("GET /api/runs/{id}", s.getRun)
	mux.HandleFunc("GET /api/runs/{id}/artifacts/{name}", s.getArtifact)
	mux.HandleFunc("GET /api/runs/{id}/live", s.streamLive)
	return s, guardAdmin(token, mux)
}

// Worker is the surface reachable by worker pods: artifact upload and health only.
func Worker(svc *runs.Service, maxBytes int64, hub *live.Hub) http.Handler {
	s := newServer(svc, maxBytes, hub)
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /artifacts/{token}/{name}", s.putArtifact)
	mux.HandleFunc("PUT /live/{token}", s.putLive)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, artifacts.ErrNotFound), errors.Is(err, runs.ErrNotActive):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, runs.ErrBusy):
		http.Error(w, runs.ErrBusy.Error(), http.StatusConflict)
	case errors.Is(err, artifacts.ErrBadName):
		http.Error(w, "bad name", http.StatusBadRequest)
	case errors.Is(err, artifacts.ErrTooLarge):
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
	default:
		log.Printf("api: internal error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (s *server) putProject(w http.ResponseWriter, r *http.Request) {
	if !mediaTypeIs(r, yamlTypes) {
		http.Error(w, "content type must be application/yaml", http.StatusUnsupportedMediaType)
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	p, err := project.Load(b)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.svc.Store.PutProject(p); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) putProjectJSON(w http.ResponseWriter, r *http.Request) {
	if !mediaTypeIs(r, jsonType) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	p, err := project.LoadJSON(b)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.svc.Store.PutProject(p); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.svc.Store.Project()
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no project yet"})
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *server) startRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Persona string `json:"persona"`
		Story   string `json:"story"`
	}
	if !mediaTypeIs(r, jsonType) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	run, err := s.svc.Start(r.Context(), req.Persona, req.Story)
	if err != nil {
		// Unknown persona/story is a client error; a launcher failure leaves
		// a failed run behind (run.ID set); anything else is internal.
		switch {
		case errors.Is(err, runs.ErrInvalid):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case run.ID != "" && !errors.Is(err, runs.ErrBusy):
			log.Printf("api: start run %s: %v", run.ID, err)
			http.Error(w, "failed to launch run", http.StatusBadGateway)
		default:
			fail(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, redact(run))
}

// redact hides the worker token from read endpoints.
func redact(r store.Run) store.Run { r.Token = ""; return r }

func (s *server) listRuns(w http.ResponseWriter, r *http.Request) {
	rs, err := s.svc.Store.Runs(100)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]store.Run, len(rs))
	for i, x := range rs {
		out[i] = redact(x)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.svc.Store.RunByID(id)
	if err != nil {
		fail(w, err)
		return
	}
	bugs, err := s.svc.Store.Bugs(id)
	if err != nil {
		fail(w, err)
		return
	}
	names, err := s.svc.Artifacts.List(id)
	if err != nil {
		fail(w, err)
		return
	}
	if bugs == nil {
		bugs = []store.Bug{}
	}
	if names == nil {
		names = []string{}
	}
	out := map[string]any{"run": redact(run), "bugs": bugs, "artifacts": names}
	if isActive(run.State) {
		if run.Session != "" {
			if stage, err := s.svc.Launcher.Stage(r.Context(), run.Session); err == nil && stage != "" {
				out["stage"] = stage
			}
		}
		if s.hub != nil && s.hub.HasFrame(id) {
			out["live"] = true
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// artifactTypes pins the content types we serve; anything else is opaque.
var artifactTypes = map[string]string{
	".webm": "video/webm",
	".mp4":  "video/mp4",
	".zip":  "application/zip",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".json": "application/json",
	".txt":  "text/plain; charset=utf-8",
}

func (s *server) getArtifact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	f, err := s.svc.Artifacts.Open(r.PathValue("id"), name)
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		fail(w, err)
		return
	}
	if !fi.Mode().IsRegular() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	ext := strings.ToLower(path.Ext(name))
	ct, ok := artifactTypes[ext]
	if !ok {
		ct = "application/octet-stream"
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", ct)
	if strings.HasPrefix(ct, "video/") {
		w.Header().Set("Content-Disposition", "inline")
	}
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func (s *server) putArtifact(w http.ResponseWriter, r *http.Request) {
	run, err := s.svc.Active(r.PathValue("token"))
	if err != nil {
		fail(w, err)
		return
	}
	body := http.MaxBytesReader(w, r.Body, s.maxBytes)
	if _, err := s.svc.Artifacts.Put(run.ID, r.PathValue("name"), body); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// putLive takes the newest screen frame from a worker.
func (s *server) putLive(w http.ResponseWriter, r *http.Request) {
	run, err := s.svc.Active(r.PathValue("token"))
	if err != nil {
		fail(w, err)
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxFrameBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "unreadable body", http.StatusBadRequest)
		}
		return
	}
	if !bytes.HasPrefix(b, []byte{0xFF, 0xD8}) {
		http.Error(w, "not a jpeg", http.StatusBadRequest)
		return
	}
	n := s.hub.Publish(run.ID, b)
	w.Header().Set("X-Bp-Watchers", strconv.Itoa(n))
	w.WriteHeader(http.StatusNoContent)
}

func isActive(st store.RunState) bool { return st == store.RunPending || st == store.RunRunning }

const liveWriteTimeout = 10 * time.Second

// streamLive serves a run's screen as multipart/x-mixed-replace JPEG parts.
func (s *server) streamLive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.svc.Store.RunByID(id)
	if err != nil {
		fail(w, err)
		return
	}
	if !isActive(run.State) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "run finished"})
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	frames, cancel := s.hub.Subscribe(id)
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	rc := http.NewResponseController(w)
	deadline := time.NewTimer(s.liveMax)
	defer deadline.Stop()
	check := time.NewTicker(s.liveCheck)
	defer check.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-check.C:
			cur, err := s.svc.Store.RunByID(id)
			if err != nil || !isActive(cur.State) {
				return
			}
		case f := <-frames:
			// A stalled viewer must not pin this goroutine: bound each part's write.
			if err := rc.SetWriteDeadline(time.Now().Add(liveWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
				return
			}
			if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(f)); err != nil {
				return
			}
			if _, err := w.Write(f); err != nil {
				return
			}
			if _, err := io.WriteString(w, "\r\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// profileOption and profileField are the wire shape of project.ProfileFields;
// the Guide sentences stay server-side.
type profileOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type profileField struct {
	Key     string          `json:"key"`
	Label   string          `json:"label"`
	Hint    string          `json:"hint"`
	Options []profileOption `json:"options"`
}

func (s *server) getProfileFields(w http.ResponseWriter, _ *http.Request) {
	out := make([]profileField, 0, len(project.ProfileFields))
	for _, f := range project.ProfileFields {
		pf := profileField{Key: f.Key, Label: f.Label, Hint: f.Hint, Options: make([]profileOption, 0, len(f.Options))}
		for _, o := range f.Options {
			pf.Options = append(pf.Options, profileOption{o.Value, o.Label})
		}
		out = append(out, pf)
	}
	writeJSON(w, http.StatusOK, out)
}
