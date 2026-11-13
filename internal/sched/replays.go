package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// FlakyLimit is how many replays in a row may fail and then pass with the
// same inputs before the copilot hardens the journey.
const FlakyLimit = 3

// CopilotEvery caps copilot runs: one per journey per day.
const CopilotEvery = 24 * time.Hour

// Via values of the runs the replay phase queues.
const (
	viaFlakeCheck = "flake check"
	viaCopilot    = "copilot"
)

// followReplays acts once on every ended replay: a failure is replayed
// again with the same inputs; a failure that repeats, a journey flaky
// FlakyLimit times in a row, or a wait that never came goes to the copilot.
func (s *Scheduler) followReplays(now time.Time) error {
	un, err := s.Store.UnfollowedReplays()
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range un {
		if err := s.followReplay(r, now); err != nil {
			errs = append(errs, fmt.Errorf("follow up replay %s: %w", r.ID, err))
		}
	}
	return errors.Join(append(errs, s.settleCopilots(now))...)
}

// settleCopilots returns to live every repairing journey whose copilot run
// ended without a decision, or never started: a journey is never left
// repairing for ever.
func (s *Scheduler) settleCopilots(now time.Time) error {
	js, err := s.Store.Journeys()
	if err != nil {
		return err
	}
	var queue []store.Queued
	var errs []error
	for _, j := range js {
		if j.State != store.JourneyRepairing {
			continue
		}
		if queue == nil {
			if queue, err = s.Store.Queue(); err != nil {
				return err
			}
		}
		if copilotQueued(queue, j) {
			continue
		}
		reason, runID := "the copilot run never started", ""
		r, err := s.Store.LastRunVia(j.Persona, j.Story, viaCopilot)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			errs = append(errs, err)
			continue
		case r.Started.Before(j.CopilotAt):
			// An earlier repair's copilot: this one never started.
		case !r.State.Final() || r.State == store.RunWaiting:
			continue // still at work
		default:
			decided, err := s.Store.RunDecided(r.ID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if decided {
				continue
			}
			reason, runID = firstLine(r.Outcome, 300), r.ID
			if reason == "" {
				reason = "it ended " + string(r.State) + " without deciding"
			}
		}
		if err := s.Store.SetJourneyState(j.ID, store.JourneyLive, store.Decision{RunID: runID, Kind: "copilot gave no decision",
			Reason: reason, Actor: store.ActorDirector, Created: now}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func copilotQueued(queue []store.Queued, j store.Journey) bool {
	for _, q := range queue {
		if q.Via == viaCopilot && q.Persona == j.Persona && q.Story == j.Story {
			return true
		}
	}
	return false
}

func (s *Scheduler) followReplay(r store.Run, now time.Time) error {
	j, err := s.Store.Journey(r.JourneyID)
	if errors.Is(err, store.ErrNotFound) {
		return s.Store.FollowReplay(r.ID, store.ReplayFollow{})
	}
	if err != nil {
		return err
	}
	if r.Revision != j.LiveRev {
		// A person replaying an older revision: nothing to repair.
		return s.Store.FollowReplay(r.ID, store.ReplayFollow{})
	}
	check := r.Via == viaFlakeCheck
	res := resultOf(r)
	row := func(kind, reason string) []store.Decision {
		return []store.Decision{{JourneyID: j.ID, RunID: r.ID, Kind: kind, Reason: reason, Actor: store.ActorDirector, Created: now}}
	}
	switch {
	case r.State == store.RunStuck:
		return s.copilot(j, r, r, "its wait never came", now, store.ReplayFollow{})
	case r.State == store.RunDone && !check:
		zero := 0
		return s.Store.FollowReplay(r.ID, store.ReplayFollow{FlakyStreak: &zero, ClearBlocked: true})
	case r.State == store.RunDone:
		failed, err := s.Store.RunByID(r.Follows)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err != nil {
			failed = r
		}
		streak := j.FlakyStreak + 1
		flaky := row("flaky", fmt.Sprintf("Failed at step %d, passed when run again with the same inputs", resultOf(failed).Step))
		if streak < FlakyLimit {
			return s.Store.FollowReplay(r.ID, store.ReplayFollow{FlakyStreak: &streak, Decisions: flaky})
		}
		zero := 0
		return s.copilot(j, r, failed, "flaky three times in a row", now, store.ReplayFollow{FlakyStreak: &zero, Decisions: flaky})
	case check:
		return s.copilot(j, r, r, "failed again with the same inputs", now, store.ReplayFollow{})
	}
	known, err := s.knownBug(j, res.Step)
	if err != nil {
		return err
	}
	if known {
		return s.Store.FollowReplay(r.ID, store.ReplayFollow{Decisions: row("still failing at a known bug",
			fmt.Sprintf("Failed at step %d, where bug %s stops it; its issue is still open", res.Step, j.BlockedBug))})
	}
	return s.Store.FollowReplay(r.ID, store.ReplayFollow{Next: []store.Queued{{
		ID: s.newID(), Persona: r.Persona, Role: r.Role, Story: r.Story, Chain: r.Chain, Follows: r.ID, Via: viaFlakeCheck,
		Reason: "Checking the failure again with the same inputs", Created: now,
		Kind: store.RunReplay, JourneyID: j.ID, Revision: r.Revision, Seed: r.Seed,
	}}})
}

// knownBug reports whether a failure at step is the one the journey is
// blocked at, by a bug whose issue is still open or confirmed.
func (s *Scheduler) knownBug(j store.Journey, step int) (bool, error) {
	if j.BlockedBug == "" || step != j.BlockedStep {
		return false, nil
	}
	b, err := s.Store.Report(j.BlockedBug)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	is, err := s.Store.Issue(b.IssueID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return is.Status == store.IssueOpen || is.Status == store.IssueConfirmed, nil
}

// copilot follows replay r by calling the copilot about the failure of
// failed (r itself, or the replay a flake check repeated), unless the
// journey is paused or had one within CopilotEvery. f carries what the
// caller also changes.
func (s *Scheduler) copilot(j store.Journey, r, failed store.Run, why string, now time.Time, f store.ReplayFollow) error {
	row := func(kind, reason string) []store.Decision {
		return []store.Decision{{JourneyID: j.ID, RunID: r.ID, Kind: kind, Reason: reason, Actor: store.ActorDirector, Created: now}}
	}
	switch {
	case j.State == store.JourneyPaused:
		f.Decisions = append(f.Decisions, row("copilot not called", "the journey is paused")...)
		return s.Store.FollowReplay(r.ID, f)
	case !j.CopilotAt.IsZero() && now.Sub(j.CopilotAt) < CopilotEvery:
		f.Decisions = append(f.Decisions, row("copilot not called", "one copilot run per journey per day; the last was "+j.CopilotAt.Format("2 Jan 15:04 MST"))...)
		return s.Store.FollowReplay(r.ID, f)
	}
	res := resultOf(failed)
	ctx, err := json.Marshal(store.RunContext{Kind: "replay_failed", PreviousRun: failed.ID, Previous: store.Clip(failed.Outcome, MaxContextText),
		Replay: &store.ContextReplay{JourneyID: j.ID, Revision: failed.Revision, Step: res.Step, StepName: res.StepName,
			Error: store.Clip(res.Error, 2000), Seed: failed.Seed, Inputs: res.Inputs, Why: why}})
	if err != nil {
		return err
	}
	line := firstLine(failed.Outcome, 300)
	reason := "A replay failed: " + line
	if failed.State == store.RunDone || line == "" {
		reason = "A replay needs the copilot: " + why
	}
	f.State, f.CopilotAt = store.JourneyRepairing, now
	f.Decisions = append(f.Decisions, row("copilot called", strings.TrimSpace(line+"\nCalled because it "+why+"."))...)
	f.Next = append(f.Next, store.Queued{ID: s.newID(), Persona: r.Persona, Role: r.Role, Story: r.Story, Chain: r.Chain,
		Follows: r.ID, Via: viaCopilot, Context: ctx, Reason: reason, Created: now})
	return s.Store.FollowReplay(r.ID, f)
}

func resultOf(r store.Run) runs.ReplayResult {
	var res runs.ReplayResult
	_ = json.Unmarshal(r.ReplayResult, &res)
	return res
}
