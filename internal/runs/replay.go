package runs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

// ErrStaleJourney means a replay was asked of a journey whose persona or
// story is no longer in the project, or of a revision it does not have.
var ErrStaleJourney = errors.New("this journey cannot be replayed")

// ErrBadReplay means a replay result was refused: the run is not a replay,
// or the result is not one bp-replay sends.
var ErrBadReplay = errors.New("bad replay result")

// ReplayResultName is the artifact keeping a replay run's result: its seed
// and drawn inputs, which the run page shows.
const ReplayResultName = "replay-result.json"

// replayNoResult is a replay run's outcome when its session ended, failed or
// timed out before bp-replay reported.
const replayNoResult = "The replay stopped without a result"

// StartReplay starts a replay run of a journey revision (0: the live one)
// with seed (0: a random one). actor is who asked, kept on the journey's
// decision trail.
func (s *Service) StartReplay(ctx context.Context, journeyID string, revision int, seed int64, actor string) (store.Run, error) {
	j, err := s.Store.Journey(journeyID)
	if err != nil {
		return store.Run{}, err
	}
	p, err := s.Store.Project()
	if err != nil {
		return store.Run{}, fmt.Errorf("load project: %w", err)
	}
	if _, _, err := find(p, j.Persona, j.Story); err != nil {
		return store.Run{}, fmt.Errorf("%w: %s's story %q is no longer in the project", ErrStaleJourney, j.Persona, j.Story)
	}
	rev, err := s.Store.Revision(j.ID, revision)
	if errors.Is(err, store.ErrNotFound) {
		if revision == 0 {
			return store.Run{}, fmt.Errorf("%w: it has no live revision yet", ErrStaleJourney)
		}
		return store.Run{}, fmt.Errorf("%w: it has no revision %d", ErrStaleJourney, revision)
	}
	if err != nil {
		return store.Run{}, err
	}
	if seed == 0 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return store.Run{}, err
		}
		seed = int64(binary.BigEndian.Uint64(b[:])>>33) + 1 // positive, and exact as a JSON number
	}
	r, err := s.start(ctx, j.Persona, j.Story, nil, &store.Run{JourneyID: j.ID, Revision: rev.N, Seed: seed})
	if r.ID == "" {
		return r, err
	}
	if derr := s.Store.AddDecision(store.Decision{JourneyID: j.ID, RunID: r.ID, Kind: "replayed", Actor: actor,
		Reason: fmt.Sprintf("Replayed revision %d", rev.N), RevBefore: j.LiveRev, RevAfter: j.LiveRev, Created: time.Now().UTC()}); derr != nil {
		return r, errors.Join(err, derr)
	}
	return r, err
}

// FinishReplay ends a replay run with bp-replay's result.
func (s *Service) FinishReplay(token string, res ReplayResult, now time.Time) error {
	r, err := s.Active(token)
	if err != nil {
		return err
	}
	if r.Kind != store.RunReplay {
		return fmt.Errorf("%w: not a replay run", ErrBadReplay)
	}
	var state store.RunState
	var outcome string
	switch res.Status {
	case "passed":
		state, outcome = store.RunDone, "Passed"
	case "failed":
		state = store.RunFailed
		if res.Step > 0 {
			outcome = fmt.Sprintf("Failed at step %d: %s", res.Step, store.Clip(res.StepName, 200))
		} else {
			outcome = "Failed before the first step"
		}
		if res.Error != "" {
			outcome += "\n" + store.Clip(res.Error, 2000)
		}
	default:
		return fmt.Errorf("%w: status must be passed or failed, got %q", ErrBadReplay, res.Status)
	}
	won, err := s.Store.FinishRunChecked(r.ID, state, outcome, now, nil)
	if err != nil {
		return err
	}
	if !won {
		return ErrNotActive
	}
	if err := s.Store.CountReplay(r.JourneyID, state == store.RunDone, now); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if b, err := json.Marshal(res); err == nil && s.Artifacts != nil {
		if _, err := s.Artifacts.Put(r.ID, ReplayResultName, bytes.NewReader(b)); err != nil {
			log.Printf("runs: keep replay result of %s: %v", r.ID, err)
		}
	}
	return s.ended(context.Background(), r, now)
}

// ReplayCode returns the code of the revision an active replay run replays.
// Any other token is ErrNotActive: the code goes only to its own replay.
func (s *Service) ReplayCode(token string) (string, error) {
	r, err := s.Active(token)
	if err != nil {
		return "", err
	}
	if r.Kind != store.RunReplay {
		return "", ErrNotActive
	}
	rev, err := s.Store.Revision(r.JourneyID, r.Revision)
	if err != nil {
		return "", err
	}
	return rev.Code, nil
}
