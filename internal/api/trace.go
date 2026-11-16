package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/boarding-party/boardingparty/internal/tracesum"
)

// traceName is the Playwright trace a run uploads when it ends.
const traceName = "trace.zip"

// getTrace serves the summary of a run's trace: its actions, page URLs and
// network requests. It is worked out once and kept as an artifact, again
// whenever the trace is newer than the kept summary or an older reader kept it.
func (s *server) getTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := s.svc.Artifacts.Open(id, traceName)
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
	if kept, err := s.svc.Artifacts.Open(id, tracesum.SummaryName); err == nil {
		ki, err := kept.Stat()
		fresh := err == nil && !ki.ModTime().Before(fi.ModTime())
		var b []byte
		if fresh {
			b, err = io.ReadAll(kept)
		}
		kept.Close()
		var v struct{ Version int }
		if fresh && err == nil && json.Unmarshal(b, &v) == nil && v.Version == tracesum.Version {
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
			return
		}
	}
	ra, ok := f.(io.ReaderAt)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			fail(w, err)
			return
		}
		ra = bytes.NewReader(b)
	}
	sum, err := tracesum.Read(ra, fi.Size())
	if err != nil {
		msg := "the trace could not be read"
		if errors.Is(err, tracesum.ErrNotTrace) {
			msg = err.Error()
		}
		http.Error(w, msg, http.StatusUnprocessableEntity)
		return
	}
	b, err := json.Marshal(sum)
	if err != nil {
		fail(w, err)
		return
	}
	if _, err := s.svc.Artifacts.Put(id, tracesum.SummaryName, bytes.NewReader(b)); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}
