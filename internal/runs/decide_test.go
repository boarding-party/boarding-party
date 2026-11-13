package runs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

// copilotRun starts a copilot run for ann's checkout journey j, as the
// scheduler queues it after replays of revision j.LiveRev failed at step 3.
func copilotRun(t *testing.T, svc *Service, j store.Journey) store.Run {
	t.Helper()
	if err := svc.Store.SetJourneyState(j.ID, store.JourneyRepairing, store.Decision{Kind: "copilot called", Actor: store.ActorDirector, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ctx, _ := json.Marshal(store.RunContext{Kind: "replay_failed", PreviousRun: "rp", Previous: "Failed at step 3: Pay",
		Replay: &store.ContextReplay{JourneyID: j.ID, Revision: j.LiveRev, Step: 3, StepName: "Pay", Error: "no Pay button", Seed: 42, Why: "failed again with the same inputs"}})
	q := store.Queued{ID: "cq", Persona: "ann", Role: "buyer", Story: "checkout", Via: "copilot", Follows: "rp", Context: ctx, Created: time.Now()}
	if err := svc.Store.Enqueue(q); err != nil {
		t.Fatal(err)
	}
	r, err := svc.StartQueued(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDecideBugKeepsRevisionAndBlocksStep(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	r := copilotRun(t, svc, j)
	if _, err := svc.DecideJourney(r.Token, Decide{Kind: "bug", Reason: "the Pay button is gone", BugID: "nope"}); err == nil {
		t.Fatal("a bug this run did not report was accepted")
	}
	if err := svc.Store.AddBug(store.Bug{ID: "b1", RunID: r.ID, Summary: "Pay button gone", Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	d, err := svc.DecideJourney(r.Token, Decide{Kind: "bug", Reason: "the Pay button is gone", BugID: "b1", Evidence: []string{"failed-step.png"}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Store.Journey(j.ID)
	if got.State != store.JourneyLive || got.LiveRev != 1 || got.BlockedStep != 3 || got.BlockedBug != "b1" {
		t.Fatalf("journey %+v", got)
	}
	if d.Kind != "bug" || d.Actor != store.ActorModel || d.RunID != r.ID || d.Evidence[0] != "failed-step.png" || d.RevAfter != 1 {
		t.Fatalf("decision %+v", d)
	}
}

func TestDecideSiteChangedNeedsWhyNotBug(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	r := copilotRun(t, svc, j)
	in := Decide{Kind: "site changed", Reason: "Pay is now Checkout", Code: journeyCode, SelfReplay: ReplayResult{Status: "passed"}}
	if _, err := svc.DecideJourney(r.Token, in); err == nil || !strings.Contains(err.Error(), "why_not_bug") {
		t.Fatalf("no why_not_bug: %v", err)
	}
	in.WhyNotBug = "the release notes rename it"
	d, err := svc.DecideJourney(r.Token, in)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Store.Journey(j.ID)
	if got.State != store.JourneyLive || got.LiveRev != 2 || got.FlakyStreak != 0 {
		t.Fatalf("journey %+v", got)
	}
	revs, _ := svc.Store.Revisions(j.ID)
	if d.Reason != "Pay is now Checkout\n\nWhy this is not a bug: the release notes rename it" || revs[0].Reason != d.Reason || d.RevBefore != 1 || d.RevAfter != 2 {
		t.Fatalf("decision %+v rev %+v", d, revs[0])
	}
}

func TestDecideRevisionMustBeProven(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	r := copilotRun(t, svc, j)
	for name, in := range map[string]Decide{
		"failed self-replay": {Kind: "flaky", Reason: "r", Code: journeyCode, SelfReplay: ReplayResult{Status: "failed", Step: 2}},
		"no code":            {Kind: "flaky", Reason: "r", SelfReplay: ReplayResult{Status: "passed"}},
		"token in code":      {Kind: "flaky", Reason: "r", Code: journeyCode + "// " + strings.Repeat("ab", 32), SelfReplay: ReplayResult{Status: "passed"}},
		"no reason":          {Kind: "flaky", Code: journeyCode, SelfReplay: ReplayResult{Status: "passed"}},
		"unknown kind":       {Kind: "maybe", Reason: "r", Code: journeyCode, SelfReplay: ReplayResult{Status: "passed"}},
		"eleven evidence":    {Kind: "flaky", Reason: "r", Code: journeyCode, SelfReplay: ReplayResult{Status: "passed"}, Evidence: make([]string, 11)},
		"long evidence":      {Kind: "flaky", Reason: "r", Code: journeyCode, SelfReplay: ReplayResult{Status: "passed"}, Evidence: []string{strings.Repeat("x", 501)}},
	} {
		if _, err := svc.DecideJourney(r.Token, in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if revs, _ := svc.Store.Revisions(j.ID); len(revs) != 1 {
		t.Fatalf("revisions %+v", revs)
	}
	if _, err := svc.DecideJourney(r.Token, Decide{Kind: "input refused correctly", Reason: "duplicate email", Code: journeyCode,
		SelfReplay: ReplayResult{Status: "passed"}}); err != nil {
		t.Fatal(err)
	}
}

func TestDecideOnlyWeaknessOutsideCopilot(t *testing.T) {
	svc, _ := newSvc(t)
	recorded(t, svc, 1)
	r := startedRun(t, svc)
	for _, kind := range []string{"bug", "site changed", "flaky", "input refused correctly"} {
		if _, err := svc.DecideJourney(r.Token, Decide{Kind: kind, Reason: "r", WhyNotBug: "w", Code: journeyCode, SelfReplay: ReplayResult{Status: "passed"}}); err == nil {
			t.Errorf("%s accepted on an AI run", kind)
		}
	}
	d, err := svc.DecideJourney(r.Token, Decide{Kind: "journey weakness", Reason: "fixed date", Code: journeyCode, SelfReplay: ReplayResult{Status: "passed"}})
	if err != nil || d.RevAfter != 2 {
		t.Fatalf("%+v %v", d, err)
	}
}

// Copilot runs do not reset the explore interval; a fresh AI run's
// decision does.
func TestCopilotDecisionDoesNotResetExplore(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	before, _ := svc.Store.Journey(j.ID)
	r := copilotRun(t, svc, j)
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.DecideJourney(r.Token, Decide{Kind: "flaky", Reason: "slow page", Code: journeyCode, SelfReplay: ReplayResult{Status: "passed"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Store.Journey(j.ID); !got.LastExplored.Equal(before.LastExplored) {
		t.Fatalf("explored moved %v → %v", before.LastExplored, got.LastExplored)
	}
	if _, err := svc.Store.FinishRun(r.ID, store.RunDone, "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	ai := startedRun(t, svc)
	time.Sleep(2 * time.Millisecond)
	if err := svc.JourneyDecision(ai.Token, DecisionConfirmed, "still does it"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Store.Journey(j.ID); !got.LastExplored.After(before.LastExplored) {
		t.Fatal("a fresh AI run's decision did not move explored")
	}
}

// A journey paused while its copilot runs stays paused when the copilot
// decides.
func TestCopilotDecisionLeavesPausedJourneyPaused(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	r := copilotRun(t, svc, j)
	if err := svc.PauseJourney(j.ID, "kate"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecideJourney(r.Token, Decide{Kind: "flaky", Reason: "slow", Code: journeyCode, SelfReplay: ReplayResult{Status: "passed"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Store.Journey(j.ID); got.State != store.JourneyPaused || got.LiveRev != 2 {
		t.Fatalf("journey %+v", got)
	}
}

func TestIsCopilot(t *testing.T) {
	ctx, _ := json.Marshal(store.RunContext{Kind: "replay_failed", Replay: &store.ContextReplay{JourneyID: "j"}})
	if !IsCopilot(store.Run{Context: ctx}) || IsCopilot(store.Run{}) || IsCopilot(store.Run{Context: json.RawMessage(`{"kind":"continue"}`)}) {
		t.Fatal("IsCopilot")
	}
	_ = errors.New
}

func copilotItem(j store.Journey) store.Queued {
	ctx, _ := json.Marshal(store.RunContext{Kind: "replay_failed", Replay: &store.ContextReplay{JourneyID: j.ID, Revision: 1, Step: 3}})
	return store.Queued{ID: "cq", Persona: "ann", Role: "buyer", Story: "checkout", Via: "copilot", Context: ctx, JourneyID: j.ID, Created: time.Now()}
}

func TestQueuedCopilotOfPausedOrGoneJourneyIsDropped(t *testing.T) {
	svc, fl := newSvc(t)
	j := recorded(t, svc, 1)
	if err := svc.PauseJourney(j.ID, "kate"); err != nil {
		t.Fatal(err)
	}
	q := copilotItem(j)
	if err := svc.Store.Enqueue(q); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartQueued(context.Background(), q); !errors.Is(err, ErrInvalid) {
		t.Fatalf("paused: %v", err)
	}
	q.JourneyID = "gone"
	if _, err := svc.StartQueued(context.Background(), q); !errors.Is(err, ErrInvalid) {
		t.Fatalf("gone: %v", err)
	}
	if len(fl.started) != 0 {
		t.Fatal("a copilot started")
	}
}

// A copilot whose journey was deleted while it ran can still finish.
func TestCopilotOfDeletedJourneyMayFinish(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	r := copilotRun(t, svc, j)
	if err := svc.CheckJourneyDecided(r); !errors.Is(err, ErrNoJourneyDecision) {
		t.Fatalf("undecided: %v", err)
	}
	if err := svc.DeleteJourney(j.ID, "kate"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckJourneyDecided(r); err != nil {
		t.Fatalf("journey gone: %v", err)
	}
}

// A self-replay that stops at a wait before the failed step proves nothing
// about that step.
func TestDecideRevisionMustReachTheFailedStep(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	r := copilotRun(t, svc, j) // failed at step 3
	waitingAt := func(step int) ReplayResult {
		return ReplayResult{Status: "waiting", Step: step, Wait: &ReplayWait{N: 1, Until: json.RawMessage(`{"time":{"after":"1h"}}`)}}
	}
	if _, err := svc.DecideJourney(r.Token, Decide{Kind: "flaky", Reason: "r", Code: journeyCode, SelfReplay: waitingAt(2)}); !errors.Is(err, ErrUnproven) {
		t.Fatalf("stops before step 3: %v", err)
	}
	if _, err := svc.DecideJourney(r.Token, Decide{Kind: "flaky", Reason: "r", Code: journeyCode, SelfReplay: waitingAt(3)}); err != nil {
		t.Fatalf("reaches step 3: %v", err)
	}
}
