package runs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/store"
)

// NarrationName is the artifact holding what a run's agent said and did.
const NarrationName = "narration.json"

// Narration bounds: the worker keeps the newest entries within them.
const (
	maxNarration     = 2000
	maxNarrationText = 500 // characters
)

// NarrationEntry is one thing the agent said (a thought) or did (an action),
// read by the worker from Claude Code's transcript. Its text is untrusted:
// it can quote the site.
type NarrationEntry struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

// ErrBadNarration means a narration report is too long or malformed.
var ErrBadNarration = errors.New("bad narration report")

// SaveNarration replaces the narration of the run holding token. Like usage,
// reports are taken while the run lasts and for usageGrace after it ends.
func (s *Service) SaveNarration(token string, n []NarrationEntry, now time.Time) error {
	r, err := s.Store.RunByToken(token)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotActive
	}
	if err != nil {
		return err
	}
	if r.Kind == store.RunReplay {
		return ErrNotActive // a replay has no model to narrate
	}
	if r.State != store.RunPending && r.State != store.RunRunning &&
		(r.Finished.IsZero() || now.Sub(r.Finished) > usageGrace) {
		return ErrNotActive
	}
	if len(n) > maxNarration {
		return ErrBadNarration
	}
	for _, e := range n {
		if e.At.IsZero() || (e.Kind != "thought" && e.Kind != "action") ||
			e.Text == "" || utf8.RuneCountInString(e.Text) > maxNarrationText {
			return ErrBadNarration
		}
	}
	b, err := json.Marshal(n)
	if err != nil {
		return err
	}
	_, err = s.Artifacts.Put(r.ID, NarrationName, bytes.NewReader(b))
	return err
}

// Narration returns the run's last reported narration, or nil if none arrived.
func (s *Service) Narration(runID string) ([]NarrationEntry, error) {
	rc, err := s.Artifacts.Open(runID, NarrationName)
	if errors.Is(err, artifacts.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var n []NarrationEntry
	if err := json.NewDecoder(io.LimitReader(rc, 4<<20)).Decode(&n); err != nil {
		return nil, err
	}
	return n, nil
}
