// Package rewind keeps a run's screen frames on disk while it runs, so the
// board can scroll back through a live run before its recording exists, and
// replay a run that ended without one.
//
// Each run has two files under the root: <run>.jpgs, the JPEG frames one
// after another, and <run>.idx, one fixed-size record per frame (arrival time
// in Unix milliseconds, offset, length). Frames are kept at most one per
// Interval, unchanged frames are skipped, and a run stops gaining frames once
// its frames reach the cap.
package rewind

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Interval is the shortest gap between two kept frames.
const Interval = time.Second

// ErrNotFound is an unknown run or frame.
var ErrNotFound = errors.New("frame not found")

const recSize = 20 // int64 at, int64 offset, int32 length

var runPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

type run struct {
	last  int64  // arrival of the newest kept frame, Unix ms; 0 = none
	frame []byte // the newest kept frame, to skip repeats
	size  int64  // bytes in <run>.jpgs
}

// Store holds every run's frames under one directory.
type Store struct {
	root   string
	maxRun int64
	mu     sync.Mutex
	runs   map[string]*run
}

// New keeps frames under root, at most maxRun bytes of frames per run.
func New(root string, maxRun int64) *Store {
	return &Store{root: root, maxRun: maxRun, runs: map[string]*run{}}
}

func (s *Store) paths(id string) (frames, index string) {
	return filepath.Join(s.root, id+".jpgs"), filepath.Join(s.root, id+".idx")
}

// state returns the run's in-memory state, reading it back from disk after a
// restart. Callers hold s.mu.
func (s *Store) state(id string) (*run, error) {
	if r := s.runs[id]; r != nil {
		return r, nil
	}
	r := &run{}
	fp, ip := s.paths(id)
	if fi, err := os.Stat(fp); err == nil {
		r.size = fi.Size()
	}
	idx, err := os.ReadFile(ip)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if n := len(idx) / recSize; n > 0 {
		at, off, ln := record(idx[(n-1)*recSize:])
		r.last = at
		if f, err := readAt(fp, off, ln); err == nil {
			r.frame = f
		}
	}
	s.runs[id] = r
	return r, nil
}

func record(b []byte) (at, off int64, ln int32) {
	return int64(binary.BigEndian.Uint64(b)), int64(binary.BigEndian.Uint64(b[8:])), int32(binary.BigEndian.Uint32(b[16:]))
}

func readAt(path string, off int64, ln int32) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, ln)
	if _, err := f.ReadAt(b, off); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return b, nil
}

func appendFile(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Append keeps frame as the run's screen at time at, unless a frame was kept
// less than Interval before, the screen has not changed, or the run is at
// its cap. Skipping is not an error.
func (s *Store) Append(id string, at time.Time, frame []byte) error {
	if !runPattern.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.state(id)
	if err != nil {
		return err
	}
	ms := at.UnixMilli()
	if r.last != 0 && ms-r.last < Interval.Milliseconds() {
		return nil
	}
	if bytes.Equal(frame, r.frame) || r.size+int64(len(frame)) > s.maxRun {
		return nil
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	fp, ip := s.paths(id)
	if err := appendFile(fp, frame); err != nil {
		return err
	}
	rec := make([]byte, recSize)
	binary.BigEndian.PutUint64(rec, uint64(ms))
	binary.BigEndian.PutUint64(rec[8:], uint64(r.size))
	binary.BigEndian.PutUint32(rec[16:], uint32(len(frame)))
	if err := appendFile(ip, rec); err != nil {
		return err
	}
	r.last, r.size = ms, r.size+int64(len(frame))
	r.frame = append(r.frame[:0], frame...)
	return nil
}

func (s *Store) index(id string) ([]byte, error) {
	if !runPattern.MatchString(id) {
		return nil, ErrNotFound
	}
	_, ip := s.paths(id)
	b, err := os.ReadFile(ip)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b[:len(b)/recSize*recSize], err
}

// Index returns when each kept frame arrived, in Unix milliseconds, oldest
// first. An unknown run has none.
func (s *Store) Index(id string) ([]int64, error) {
	b, err := s.index(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]int64, 0, len(b)/recSize)
	for i := 0; i+recSize <= len(b); i += recSize {
		at, _, _ := record(b[i:])
		out = append(out, at)
	}
	return out, nil
}

// Frame returns the run's n-th kept frame (0 is the oldest).
func (s *Store) Frame(id string, n int) ([]byte, error) {
	b, err := s.index(id)
	if err != nil {
		return nil, err
	}
	if n < 0 || (n+1)*recSize > len(b) {
		return nil, ErrNotFound
	}
	_, off, ln := record(b[n*recSize:])
	fp, _ := s.paths(id)
	return readAt(fp, off, ln)
}

// Remove deletes the run's frames.
func (s *Store) Remove(id string) error {
	if !runPattern.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runs, id)
	fp, ip := s.paths(id)
	for _, p := range []string{ip, fp} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
