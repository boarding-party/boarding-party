package runs

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

// tokenLike matches a run token: 64 hex characters, as every director URL
// a run is given carries one.
var tokenLike = regexp.MustCompile(`[0-9a-fA-F]{64}`)

// Limits on what an AI run may save as a journey.
const (
	MaxJourneyCode   = 256 << 10
	MaxJourneyReason = 2000
)

// ReplayResult is what bp-replay prints for one replay of a journey: the
// same shape for an AI run's self-replay and a replay run's report.
type ReplayResult struct {
	Status     string         `json:"status"` // passed | failed
	Step       int            `json:"step,omitempty"`
	StepName   string         `json:"stepName,omitempty"`
	Error      string         `json:"error,omitempty"`
	Seed       int64          `json:"seed,omitempty"`
	Inputs     map[string]any `json:"inputs,omitempty"`
	DurationMs int            `json:"durationMs,omitempty"`
}

// SaveJourney is a revision an AI run asks to store, with the self-replay
// that proves it.
type SaveJourney struct {
	Code       string
	Reason     string
	Kind       store.RevisionKind
	BugID      string // bug revisions: the bug this run reported
	BugStep    int    // bug revisions: the step that fails while the bug is there
	SelfReplay ReplayResult
}

// ErrUnproven means the self-replay does not prove the revision: a full
// revision must pass, a bug revision must fail exactly at its bug step.
var ErrUnproven = errors.New("the self-replay does not prove this revision")

// ErrNoJourneyDecision means a run tried to finish without saving a journey
// revision or saying why it did not.
var ErrNoJourneyDecision = errors.New("before finish_run, call save_journey with a proven revision, or journey_decision with confirmed or not recorded and a reason")

// Journey decision kinds an AI run may make without a revision.
const (
	DecisionConfirmed   = "confirmed"
	DecisionNotRecorded = "not recorded"
)

// SaveJourney stores a proven revision of the run's persona and story
// journey with its decision. Nothing is stored when it is refused.
func (s *Service) SaveJourney(token string, in SaveJourney) (store.Revision, error) {
	r, err := s.Active(token)
	if err != nil {
		return store.Revision{}, err
	}
	if err := checkText(in.Code, in.Reason); err != nil {
		return store.Revision{}, err
	}
	if strings.TrimSpace(in.Code) == "" {
		return store.Revision{}, errors.New("code is required")
	}
	if tokenLike.MatchString(in.Code) {
		return store.Revision{}, errors.New("the journey holds what looks like a run token or server URL; journeys replay unattended, so they never carry one")
	}
	kind := "recorded"
	switch in.Kind {
	case store.RevisionFull:
		if in.SelfReplay.Status != "passed" {
			return store.Revision{}, fmt.Errorf("%w: a full revision's self-replay must pass, it %s", ErrUnproven, describe(in.SelfReplay))
		}
	case store.RevisionBug:
		kind = "bug revision recorded"
		if in.BugStep < 1 {
			return store.Revision{}, errors.New("a bug revision needs bug_step, the step that fails while the bug is there")
		}
		b, err := s.Store.Report(in.BugID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && b.RunID != r.ID) {
			return store.Revision{}, fmt.Errorf("bug_id %q is not a bug this run reported", in.BugID)
		}
		if err != nil {
			return store.Revision{}, err
		}
		if in.SelfReplay.Status != "failed" || in.SelfReplay.Step != in.BugStep {
			return store.Revision{}, fmt.Errorf("%w: a bug revision's self-replay must fail at step %d, it %s", ErrUnproven, in.BugStep, describe(in.SelfReplay))
		}
	default:
		return store.Revision{}, fmt.Errorf("kind must be full or bug, got %q", in.Kind)
	}
	now := time.Now().UTC()
	_, rev, err := s.Store.AddRevision(r.Persona, r.Story,
		store.Revision{Kind: in.Kind, Code: in.Code, Reason: in.Reason, RunID: r.ID, BugID: in.BugID, BugStep: in.BugStep, Created: now},
		store.Decision{RunID: r.ID, Kind: kind, Reason: in.Reason, Actor: store.ActorModel, Created: now})
	return rev, err
}

// JourneyDecision records that the run saved no revision, and why: its
// journey already does what the run did (confirmed), or no passing journey
// could be made (not recorded).
func (s *Service) JourneyDecision(token, kind, reason string) error {
	r, err := s.Active(token)
	if err != nil {
		return err
	}
	if err := checkText("", reason); err != nil {
		return err
	}
	now := time.Now().UTC()
	var j store.Journey
	switch kind {
	case DecisionConfirmed:
		j, err = s.Store.JourneyFor(r.Persona, r.Story)
		if errors.Is(err, store.ErrNotFound) || (err == nil && j.LiveRev == 0) {
			return errors.New("there is no live journey to confirm; save one with save_journey or say not recorded")
		}
	case DecisionNotRecorded:
		j, err = s.Store.EnsureJourney(r.Persona, r.Story, now)
	default:
		return fmt.Errorf("kind must be %q or %q, got %q", DecisionConfirmed, DecisionNotRecorded, kind)
	}
	if err != nil {
		return err
	}
	return s.Store.AddDecision(store.Decision{JourneyID: j.ID, RunID: r.ID, Kind: kind, Reason: reason,
		Actor: store.ActorModel, RevBefore: j.LiveRev, RevAfter: j.LiveRev, Created: now})
}

// CheckJourneyDecided is ErrNoJourneyDecision until run r has saved a
// revision or made a journey decision.
func (s *Service) CheckJourneyDecided(r store.Run) error {
	ok, err := s.Store.RunDecided(r.ID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoJourneyDecision
	}
	return nil
}

// PauseJourney stops a journey's replays until it is resumed; nothing else
// runs for its persona and story either. A copilot run already under way
// still lands its decision; the journey stays paused.
func (s *Service) PauseJourney(id, actor string) error {
	return s.Store.SetJourneyState(id, store.JourneyPaused, store.Decision{Kind: "paused",
		Reason: "Paused by " + actor, Actor: actor, Created: time.Now().UTC()})
}

// ResumeJourney makes a journey live again.
func (s *Service) ResumeJourney(id, actor string) error {
	return s.Store.SetJourneyState(id, store.JourneyLive, store.Decision{Kind: "resumed",
		Reason: "Resumed by " + actor, Actor: actor, Created: time.Now().UTC()})
}

// RollbackJourney makes the code of full revision n live again, as a new
// revision.
func (s *Service) RollbackJourney(id string, n int, actor string) (store.Revision, error) {
	if n < 1 {
		return store.Revision{}, fmt.Errorf("%w: revision must be 1 or more", ErrInvalid)
	}
	_, rev, err := s.Store.Rollback(id, n, store.Decision{Kind: "rolled back",
		Reason: fmt.Sprintf("Rolled back to revision %d by %s", n, actor), Actor: actor, Created: time.Now().UTC()})
	if errors.Is(err, store.ErrBugRevision) {
		return store.Revision{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return rev, err
}

// DeleteJourney deletes a journey and its revisions; its decisions stay.
func (s *Service) DeleteJourney(id, actor string) error {
	return s.Store.DeleteJourney(id, store.Decision{Kind: "deleted",
		Reason: "Deleted by " + actor, Actor: actor, Created: time.Now().UTC()})
}

func checkText(code, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("reason is required")
	}
	if len(reason) > MaxJourneyReason {
		return fmt.Errorf("reason is over %d characters", MaxJourneyReason)
	}
	if len(code) > MaxJourneyCode {
		return fmt.Errorf("code is over %d bytes", MaxJourneyCode)
	}
	return nil
}

func describe(res ReplayResult) string {
	switch res.Status {
	case "passed":
		return "passed"
	case "failed":
		return fmt.Sprintf("failed at step %d", res.Step)
	case "":
		return "was not given"
	}
	return fmt.Sprintf("has status %q", res.Status)
}
