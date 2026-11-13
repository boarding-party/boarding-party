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

// ReplayTimeout is how long a replay run may take before it fails without a
// result: bp-replay's 10 minutes for the journey plus starting the pod. A
// replay's pod idles once bp-replay exits, so its session never ends on its
// own; this is what catches a replay that died without reporting.
const ReplayTimeout = 13 * time.Minute

// MaxSeed is the largest replay seed: seeds stay exact in JavaScript and
// in bp-replay's generator.
const MaxSeed = 1<<31 - 1

// ReplayResultName is the artifact keeping a replay run's result: its seed
// and drawn inputs, which the run page shows.
const ReplayResultName = "replay-result.json"

// replayNoResult is a replay run's outcome when its session ended, failed or
// timed out before bp-replay reported.
const replayNoResult = "The replay stopped without a result"

// EnqueueReplay queues a replay of a journey revision (0: the live one when
// it starts) with seed (0: one drawn now, so the queued item already says
// what it will replay). actor is who asked, kept on the journey's decision
// trail.
func (s *Service) EnqueueReplay(journeyID string, revision int, seed int64, actor string, now time.Time) (store.Queued, error) {
	j, role, rev, err := s.replayable(journeyID, revision)
	if err != nil {
		return store.Queued{}, err
	}
	if seed < 0 || seed > MaxSeed {
		return store.Queued{}, fmt.Errorf("%w: seed must be 1 to %d", ErrInvalid, MaxSeed)
	}
	if seed == 0 {
		if seed, err = NewSeed(); err != nil {
			return store.Queued{}, err
		}
	}
	id, err := randHex(16)
	if err != nil {
		return store.Queued{}, err
	}
	q := store.Queued{ID: id, Persona: j.Persona, Role: role, Story: j.Story, Reason: fmt.Sprintf("Replay of revision %d", rev.N),
		Created: now, Kind: store.RunReplay, JourneyID: j.ID, Revision: revision, Seed: seed}
	if err := s.Store.Enqueue(q); err != nil {
		return store.Queued{}, err
	}
	if err := s.Store.AddDecision(store.Decision{JourneyID: j.ID, Kind: "replayed", Actor: actor,
		Reason: fmt.Sprintf("Replay of revision %d asked", rev.N), RevBefore: j.LiveRev, RevAfter: j.LiveRev, Created: now}); err != nil {
		return q, err
	}
	return q, nil
}

// replayable returns the journey, its persona's role and the revision a
// replay would play, or why it cannot: ErrNotFound for no such journey,
// ErrStaleJourney for a paused journey, a persona or story no longer in the
// project, or a missing revision.
func (s *Service) replayable(journeyID string, revision int) (store.Journey, string, store.Revision, error) {
	j, err := s.Store.Journey(journeyID)
	if err != nil {
		return store.Journey{}, "", store.Revision{}, err
	}
	if j.State == store.JourneyPaused {
		return store.Journey{}, "", store.Revision{}, fmt.Errorf("%w: it is paused", ErrStaleJourney)
	}
	p, err := s.Store.Project()
	if err != nil {
		return store.Journey{}, "", store.Revision{}, fmt.Errorf("load project: %w", err)
	}
	role, _, err := find(p, j.Persona, j.Story)
	if err != nil {
		return store.Journey{}, "", store.Revision{}, fmt.Errorf("%w: %s's story %q is no longer in the project", ErrStaleJourney, j.Persona, j.Story)
	}
	rev, err := s.Store.Revision(j.ID, revision)
	if errors.Is(err, store.ErrNotFound) {
		if revision == 0 {
			return store.Journey{}, "", store.Revision{}, fmt.Errorf("%w: it has no live revision yet", ErrStaleJourney)
		}
		return store.Journey{}, "", store.Revision{}, fmt.Errorf("%w: it has no revision %d", ErrStaleJourney, revision)
	}
	if err != nil {
		return store.Journey{}, "", store.Revision{}, err
	}
	return j, role, rev, nil
}

// NewSeed draws a replay seed, 1 to MaxSeed.
func NewSeed() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(b[:])%MaxSeed) + 1, nil
}

// startReplay starts a queued replay item. A journey gone, paused or
// without that revision since it was queued makes the item invalid, so the
// scheduler drops it.
func (s *Service) startReplay(ctx context.Context, q store.Queued) (store.Run, error) {
	j, _, rev, err := s.replayable(q.JourneyID, q.Revision)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrStaleJourney) {
		return store.Run{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err != nil {
		return store.Run{}, err
	}
	if q.Seed < 1 || q.Seed > MaxSeed {
		return store.Run{}, fmt.Errorf("%w: seed must be 1 to %d", ErrInvalid, MaxSeed)
	}
	return s.start(ctx, j.Persona, j.Story, &q, &store.Run{JourneyID: j.ID, Revision: rev.N, Seed: q.Seed})
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
