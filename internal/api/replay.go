package api

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
)

// maxReplayReport caps a replay run's result: a status, a step and an
// error, plus the drawn inputs.
const maxReplayReport = 64 << 10

// getReplayCode gives a replay run the code of the revision it replays.
func (s *server) getReplayCode(w http.ResponseWriter, r *http.Request) {
	code, err := s.svc.ReplayCode(r.PathValue("token"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, code)
}

// putReplay takes bp-replay's result and ends the replay run with it.
func (s *server) putReplay(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReplayReport))
	dec.DisallowUnknownFields()
	var res runs.ReplayResult
	if err := dec.Decode(&res); err != nil {
		http.Error(w, "bad replay result", http.StatusBadRequest)
		return
	}
	if err := s.svc.FinishReplay(r.PathValue("token"), res, time.Now().UTC()); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
