package runs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
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
	Counts
	// ByModel splits the counts by the model that served each turn: Claude
	// Code may hand side work to a smaller model.
	ByModel map[string]Counts `json:"byModel,omitempty"`
}

// Counts are token totals over some turns.
type Counts struct {
	Input      uint64 `json:"input"`
	Output     uint64 `json:"output"`
	CacheRead  uint64 `json:"cacheRead"`
	CacheWrite uint64 `json:"cacheWrite"`
	Turns      uint64 `json:"turns"`
}

// ErrBadUsage means a usage report names too many models or a malformed one.
var ErrBadUsage = errors.New("bad usage report")

// maxUsageModels bounds a report: a run uses one or two models.
const maxUsageModels = 8

var modelID = regexp.MustCompile(`^[a-z0-9][a-z0-9.@_-]{0,99}$`)

// SaveUsage records the usage of the run holding token. The worker sends a
// running total, but a report may never lower spend: it is merged with the
// stored usage so every counter is the max of old and new, over the union of
// models.
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
	if len(u.ByModel) > maxUsageModels {
		return ErrBadUsage
	}
	for m := range u.ByModel {
		if !modelID.MatchString(m) {
			return ErrBadUsage
		}
	}
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	old, err := s.Usage(r.ID)
	if err != nil {
		return err
	}
	if old != nil {
		u = mergeUsage(*old, u)
		if len(u.ByModel) > maxUsageModels {
			return ErrBadUsage
		}
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

func maxCounts(a, b Counts) Counts {
	return Counts{Input: max(a.Input, b.Input), Output: max(a.Output, b.Output),
		CacheRead: max(a.CacheRead, b.CacheRead), CacheWrite: max(a.CacheWrite, b.CacheWrite),
		Turns: max(a.Turns, b.Turns)}
}

// mergeUsage takes the per-counter maximum of a and b, over the union of models.
func mergeUsage(a, b Usage) Usage {
	out := Usage{Counts: maxCounts(a.Counts, b.Counts)}
	if len(a.ByModel)+len(b.ByModel) > 0 {
		out.ByModel = map[string]Counts{}
		for m, c := range a.ByModel {
			out.ByModel[m] = c
		}
		for m, c := range b.ByModel {
			out.ByModel[m] = maxCounts(out.ByModel[m], c)
		}
	}
	return out
}
