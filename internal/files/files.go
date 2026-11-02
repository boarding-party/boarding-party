// Package files stores test fixtures a persona can attach in the site (a
// logo, a CSV, a PDF). A file is shared by the whole project or belongs to
// one persona. Files live on the director's data volume, one directory per
// scope; the content type is sniffed from the bytes when listed or opened.
package files

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Default limits: per file and across every file.
const (
	DefaultMaxFile  int64 = 25 << 20
	DefaultMaxTotal int64 = 200 << 20
)

var (
	ErrBadName  = errors.New("bad file name: use lower-case letters, digits, dot, underscore or hyphen, starting with a letter or digit, at most 64 characters")
	ErrBadScope = errors.New("bad scope: use project or persona:<name>")
	ErrNotFound = errors.New("file not found")
	// ErrTooLarge and ErrTotalTooLarge both mean HTTP 413; the text is shown to the user.
	ErrTooLarge      = errors.New("file is over the 25 MiB limit")
	ErrTotalTooLarge = errors.New("files would exceed the 200 MiB total; delete some first")
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidName reports whether n is an allowed file name.
func ValidName(n string) bool { return namePattern.MatchString(n) }

const (
	// ScopeProject is the scope of files every persona may use.
	ScopeProject  = "project"
	personaPrefix = "persona:"
	tmpPrefix     = ".upload-"
	maxPersonaLen = 100
)

// PersonaScope is the scope of one persona's files.
func PersonaScope(persona string) string { return personaPrefix + persona }

// PersonaOf returns the persona a scope belongs to, "" for the project scope.
func PersonaOf(scope string) string { return strings.TrimPrefix(scope, personaPrefix) }

// Info describes one stored file.
type Info struct {
	Name        string    `json:"name"`
	Scope       string    `json:"scope"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
	Uploaded    time.Time `json:"uploaded"`
}

// File is an opened file: seekable.
type File interface {
	io.ReadSeekCloser
}

type Store struct {
	root     string
	MaxFile  int64
	MaxTotal int64
	mu       sync.Mutex
}

// New stores files under root/<scope>/<name>, with the default limits.
func New(root string) *Store {
	return &Store{root: root, MaxFile: DefaultMaxFile, MaxTotal: DefaultMaxTotal}
}

// scopeDir maps a scope to its directory. Persona names are hex-encoded so
// no persona name can escape the root.
func scopeDir(scope string) (string, error) {
	switch {
	case scope == ScopeProject:
		return "project", nil
	case strings.HasPrefix(scope, personaPrefix):
		p := scope[len(personaPrefix):]
		if p == "" || len(p) > maxPersonaLen {
			return "", ErrBadScope
		}
		return "persona-" + hex.EncodeToString([]byte(p)), nil
	}
	return "", ErrBadScope
}

func scopeOfDir(dir string) (string, bool) {
	if dir == "project" {
		return ScopeProject, true
	}
	if h, ok := strings.CutPrefix(dir, "persona-"); ok {
		if b, err := hex.DecodeString(h); err == nil && len(b) > 0 {
			return personaPrefix + string(b), true
		}
	}
	return "", false
}

// Put stores r as name in scope, replacing a file of the same name and scope.
// Persona existence is the caller's business.
func (s *Store) Put(scope, name string, r io.Reader) (Info, error) {
	dir, err := scopeDir(scope)
	if err != nil {
		return Info{}, err
	}
	if !ValidName(name) {
		return Info{}, ErrBadName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	full := filepath.Join(s.root, dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		return Info{}, err
	}
	tmp, err := os.CreateTemp(full, tmpPrefix+"*")
	if err != nil {
		return Info{}, err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename

	n, err := io.Copy(tmp, io.LimitReader(r, s.MaxFile+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return Info{}, ErrTooLarge
	}
	if err != nil {
		return Info{}, err
	}
	if n > s.MaxFile {
		return Info{}, ErrTooLarge
	}
	all, err := s.list()
	if err != nil {
		return Info{}, err
	}
	total := n
	for _, f := range all {
		if !(f.Scope == scope && f.Name == name) {
			total += f.Size
		}
	}
	if total > s.MaxTotal {
		return Info{}, ErrTotalTooLarge
	}
	dest := filepath.Join(full, name)
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return Info{}, err
	}
	return s.info(scope, name, dest)
}

// Delete removes the file; ErrNotFound if there is none.
func (s *Store) Delete(scope, name string) error {
	dir, err := scopeDir(scope)
	if err != nil {
		return err
	}
	if !ValidName(name) {
		return ErrBadName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err = os.Remove(filepath.Join(s.root, dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// List returns every file, project files first, then by persona and name.
func (s *Store) List() ([]Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list()
}

func (s *Store) list() ([]Info, error) {
	dirs, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	var out []Info
	for _, d := range dirs {
		scope, ok := scopeOfDir(d.Name())
		if !ok || !d.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(s.root, d.Name()))
		if err != nil {
			return nil, fmt.Errorf("list files: %w", err)
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !ValidName(e.Name()) {
				continue
			}
			in, err := s.info(scope, e.Name(), filepath.Join(s.root, d.Name(), e.Name()))
			if err != nil {
				continue // vanished while listing
			}
			out = append(out, in)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Scope == ScopeProject) != (out[j].Scope == ScopeProject) {
			return out[i].Scope == ScopeProject
		}
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *Store) info(scope, name, full string) (Info, error) {
	f, err := os.Open(full)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Info{}, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	return Info{Name: name, Scope: scope, Size: fi.Size(), ContentType: http.DetectContentType(head[:n]), Uploaded: fi.ModTime().UTC()}, nil
}

// ForPersona lists what a persona may use: the project's files plus its own,
// its own winning when a name appears in both. Sorted by name.
func (s *Store) ForPersona(persona string) ([]Info, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	byName := map[string]Info{}
	for _, f := range all { // project files come first, so a persona file overwrites
		if f.Scope == ScopeProject || f.Scope == PersonaScope(persona) {
			byName[f.Name] = f
		}
	}
	out := make([]Info, 0, len(byName))
	for _, f := range byName {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// OpenFor opens name as persona sees it (its own file first, else the project's).
func (s *Store) OpenFor(persona, name string) (File, Info, error) {
	if !ValidName(name) {
		return nil, Info{}, ErrBadName
	}
	for _, scope := range []string{PersonaScope(persona), ScopeProject} {
		f, in, err := s.open(scope, name)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		return f, in, err
	}
	return nil, Info{}, ErrNotFound
}

func (s *Store) open(scope, name string) (File, Info, error) {
	dir, err := scopeDir(scope)
	if err != nil {
		return nil, Info{}, err
	}
	full := filepath.Join(s.root, dir, name)
	li, err := os.Lstat(full) // refuse symlinks and directories
	if errors.Is(err, os.ErrNotExist) || (err == nil && !li.Mode().IsRegular()) {
		return nil, Info{}, ErrNotFound
	}
	if err != nil {
		return nil, Info{}, err
	}
	in, err := s.info(scope, name, full)
	if err != nil {
		return nil, Info{}, ErrNotFound
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, Info{}, ErrNotFound
	}
	return f, in, nil
}
