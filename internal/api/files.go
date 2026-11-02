package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/boarding-party/boardingparty/internal/files"
	"github.com/boarding-party/boardingparty/internal/store"
)

// fileStore returns the file store, or answers 501 when the service has none.
func (s *server) fileStore(w http.ResponseWriter) *files.Store {
	if s.svc.Files == nil {
		http.Error(w, "files are not enabled on this director", http.StatusNotImplemented)
		return nil
	}
	return s.svc.Files
}

// failFile answers a files error with its own message, which is safe to show.
func failFile(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrBadName), errors.Is(err, files.ErrBadScope):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, files.ErrTooLarge), errors.Is(err, files.ErrTotalTooLarge):
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
	case errors.Is(err, files.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	default:
		fail(w, err)
	}
}

func (s *server) listFiles(w http.ResponseWriter, _ *http.Request) {
	fs := s.fileStore(w)
	if fs == nil {
		return
	}
	l, err := fs.List()
	if err != nil {
		fail(w, err)
		return
	}
	if l == nil {
		l = []files.Info{}
	}
	writeJSON(w, http.StatusOK, l)
}

// personaExists reports whether the current project has a persona with that name.
func (s *server) personaExists(name string) (bool, error) {
	p, err := s.svc.Store.Project()
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, r := range p.Roles {
		for _, pe := range r.Personas {
			if pe.Name == name {
				return true, nil
			}
		}
	}
	return false, nil
}

// putFile stores the raw request body. Any content type is accepted here: the
// body is opaque bytes, never parsed, and the admin guard already refuses
// browser cross-origin requests.
func (s *server) putFile(w http.ResponseWriter, r *http.Request) {
	fs := s.fileStore(w)
	if fs == nil {
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = files.ScopeProject
	}
	if p, isPersona := strings.CutPrefix(scope, "persona:"); isPersona && p != "" {
		ok, err := s.personaExists(p)
		if err != nil {
			fail(w, err)
			return
		}
		if !ok {
			http.Error(w, "unknown persona "+strconv.Quote(p)+": add it to the project first", http.StatusBadRequest)
			return
		}
	}
	if r.ContentLength > fs.MaxFile {
		failFile(w, files.ErrTooLarge)
		return
	}
	in, err := fs.Put(scope, r.PathValue("name"), http.MaxBytesReader(w, r.Body, fs.MaxFile))
	if err != nil {
		failFile(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *server) deleteFile(w http.ResponseWriter, r *http.Request) {
	fs := s.fileStore(w)
	if fs == nil {
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = files.ScopeProject
	}
	if err := fs.Delete(scope, r.PathValue("name")); err != nil {
		failFile(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workerFiles lists the names the run's persona may use.
func (s *server) workerFiles(w http.ResponseWriter, r *http.Request) {
	run, err := s.svc.Active(r.PathValue("token"))
	if err != nil {
		fail(w, err)
		return
	}
	names := []string{}
	if fs := s.svc.Files; fs != nil {
		l, err := fs.ForPersona(run.Persona)
		if err != nil {
			fail(w, err)
			return
		}
		for _, f := range l {
			names = append(names, f.Name)
		}
	}
	writeJSON(w, http.StatusOK, names)
}

// workerFile serves one file to the run's persona; 404 when not allowed.
func (s *server) workerFile(w http.ResponseWriter, r *http.Request) {
	run, err := s.svc.Active(r.PathValue("token"))
	if err != nil {
		fail(w, err)
		return
	}
	if s.svc.Files == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	f, in, err := s.svc.Files.OpenFor(run.Persona, r.PathValue("name"))
	if errors.Is(err, files.ErrBadName) || errors.Is(err, files.ErrBadScope) {
		err = files.ErrNotFound
	}
	if err != nil {
		failFile(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", in.ContentType)
	w.Header().Set("Content-Disposition", "attachment")
	http.ServeContent(w, r, in.Name, in.Uploaded, f)
}
