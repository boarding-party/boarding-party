package runs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/store"
)

// UsageName is the artifact holding a run's token usage.
const UsageName = "usage.json"

// usageGrace is how long after a run finishes its worker may still report
// usage: the turn that calls finish_run lands in the transcript moments
// before the pod stops, so its report can arrive after the run is done.
const usageGrace = 2 * time.Minute

// Usage is the agent's token accounting for a run, summed by the worker from
// Claude Code's transcript. Each turn re-reads the whole conversation, so
// Input+CacheRead+CacheWrite is every token the model read.
type Usage struct {
	Input      uint64 `json:"input"`
	Output     uint64 `json:"output"`
	CacheRead  uint64 `json:"cacheRead"`
	CacheWrite uint64 `json:"cacheWrite"`
	Turns      uint64 `json:"turns"`
}

// SaveUsage replaces the usage of the run holding token. The worker sends a
// running total, so the newest report wins.
func (s *Service) SaveUsage(token string, u Usage, now time.Time) error {
	r, err := s.Store.RunByToken(token)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotActive
	}
	if err != nil {
		return err
	}
	if r.State != store.RunPending && r.State != store.RunRunning &&
		(r.Finished.IsZero() || now.Sub(r.Finished) > usageGrace) {
		return ErrNotActive
	}
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}
	_, err = s.Artifacts.Put(r.ID, UsageName, bytes.NewReader(b))
	return err
}

// Usage returns the run's last reported usage, or nil if none arrived.
func (s *Service) Usage(runID string) (*Usage, error) {
	rc, err := s.Artifacts.Open(runID, UsageName)
	if errors.Is(err, artifacts.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var u Usage
	if err := json.NewDecoder(io.LimitReader(rc, 1<<16)).Decode(&u); err != nil {
		return nil, err
	}
	return &u, nil
}
