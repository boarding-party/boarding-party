package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// ended stores an ended replay run of journey j: via "" or "flake check",
// following follows, with bp-replay's result res.
func (f *fix) ended(id string, j store.Journey, via, follows string, state store.RunState, outcome, res string) store.Run {
	f.t.Helper()
	r := store.Run{ID: id, Token: "tok-" + id, Persona: j.Persona, Role: "patient", Story: j.Story, State: store.RunRunning,
		Started: f.clk.Now(), Chain: "c-" + id, Kind: store.RunReplay, JourneyID: j.ID, Revision: j.LiveRev, Seed: 42, Via: via, Follows: follows}
	if follows != "" {
		r.Chain = "c-" + follows
	}
	if err := f.st.CreateRun(r); err != nil {
		f.t.Fatal(err)
	}
	if res != "" {
		if err := f.st.SetReplayResult(id, json.RawMessage(res)); err != nil {
			f.t.Fatal(err)
		}
	}
	if ok, err := f.st.FinishRun(id, state, outcome, f.clk.Now()); err != nil || !ok {
		f.t.Fatal(ok, err)
	}
	r.State = state
	return r
}

// follow runs the replay phase alone, so drain does not start what it queues.
func (f *fix) follow() {
	f.t.Helper()
	if err := f.s.followReplays(f.clk.Now()); err != nil {
		f.t.Fatal(err)
	}
}

const failedAt3 = `{"status":"failed","step":3,"stepName":"Pay","error":"no Pay button","seed":42,"inputs":{"int1":4}}`

func (f *fix) decisions(j store.Journey) []store.Decision {
	f.t.Helper()
	ds, err := f.st.Decisions(j.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return ds
}

func (f *fix) journeyNow(j store.Journey) store.Journey {
	f.t.Helper()
	got, err := f.st.Journey(j.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func TestPassedReplayResetsFlakyAndBlocked(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	if err := f.st.SetBlocked(j.ID, 3, "b1"); err != nil {
		t.Fatal(err)
	}
	f.ended("r1", j, "", "", store.RunFailed, "Failed at step 3: Pay", failedAt3)
	f.ended("r2", j, "flake check", "r1", store.RunDone, "Passed", `{"status":"passed","seed":42}`)
	f.follow() // r1 queues a check; r2 counts one flaky
	n := len(f.decisions(j))
	f.st.DeleteQueued(f.queue()[0].ID)
	f.ended("r3", j, "", "", store.RunDone, "Passed", `{"status":"passed"}`)
	f.follow()
	got := f.journeyNow(j)
	if got.FlakyStreak != 0 || got.BlockedStep != 0 || got.BlockedBug != "" {
		t.Fatalf("journey %+v", got)
	}
	if len(f.decisions(j)) != n || len(f.queue()) != 0 {
		t.Fatalf("a passed replay wrote rows or queued: %+v", f.queue())
	}
}

func TestFailedReplayIsCheckedAgain(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.ended("r1", j, "", "", store.RunFailed, "Failed at step 3: Pay\nno Pay button", failedAt3)
	f.follow()
	q := f.queue()
	if len(q) != 1 || q[0].Kind != store.RunReplay || q[0].JourneyID != j.ID || q[0].Revision != 1 || q[0].Seed != 42 ||
		q[0].Via != "flake check" || q[0].Follows != "r1" || q[0].Chain != "c-r1" || q[0].Reason != "Checking the failure again with the same inputs" {
		t.Fatalf("queue = %+v", q)
	}
	f.follow()
	if len(f.queue()) != 1 {
		t.Fatalf("followed twice: %+v", f.queue())
	}
}

func TestFlakyReplayCountsAndCallsCopilotAtLimit(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	for i := 1; i <= FlakyLimit; i++ {
		id := string(rune('a' + i))
		f.ended("f"+id, j, "", "", store.RunFailed, "Failed at step 3: Pay", failedAt3)
		f.ended("p"+id, j, "flake check", "f"+id, store.RunDone, "Passed", `{"status":"passed","seed":42}`)
		f.follow()
		for _, q := range f.queue() {
			if q.Via == "flake check" {
				f.st.DeleteQueued(q.ID)
			}
		}
		got := f.journeyNow(j)
		if i < FlakyLimit && (got.FlakyStreak != i || got.State != store.JourneyLive) {
			t.Fatalf("after %d: %+v", i, got)
		}
	}
	got := f.journeyNow(j)
	if got.FlakyStreak != 0 || got.State != store.JourneyRepairing {
		t.Fatalf("at the limit: %+v", got)
	}
	ds := f.decisions(j)
	if ds[0].Kind != "copilot called" || ds[0].Actor != store.ActorDirector || ds[1].Kind != "flaky" ||
		ds[1].Reason != "Failed at step 3, passed when run again with the same inputs" {
		t.Fatalf("decisions %+v", ds[:2])
	}
	q := f.queue()
	if len(q) != 1 || q[0].Kind != "" || q[0].Via != "copilot" || q[0].Story != "book" || q[0].Persona != "Ada" {
		t.Fatalf("queue = %+v", q)
	}
	c := ctxOf(t, q[0])
	if c.Kind != "replay_failed" || c.Replay == nil || c.Replay.Why != "flaky three times in a row" || c.Replay.Step != 3 || c.Replay.Seed != 42 {
		t.Fatalf("context %+v %+v", c, c.Replay)
	}
}

func TestFlakeCheckFailureCallsCopilot(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.ended("r1", j, "", "", store.RunFailed, "Failed at step 3: Pay\nno Pay button", failedAt3)
	f.ended("r2", j, "flake check", "r1", store.RunFailed, "Failed at step 3: Pay\nno Pay button", failedAt3)
	f.follow()
	var copilot, checks int
	for _, q := range f.queue() {
		switch q.Via {
		case "copilot":
			copilot++
			c := ctxOf(t, q)
			if q.Follows != "r2" || q.Chain != "c-r1" || q.Reason != "A replay failed: Failed at step 3: Pay" ||
				c.PreviousRun != "r2" || c.Replay.Error != "no Pay button" || c.Replay.StepName != "Pay" || c.Replay.Inputs["int1"] != float64(4) ||
				c.Replay.Revision != 1 || c.Replay.JourneyID != j.ID || c.Replay.Why != "failed again with the same inputs" {
				t.Fatalf("copilot item %+v context %+v", q, c.Replay)
			}
		case "flake check":
			checks++
		}
	}
	if copilot != 1 || checks != 1 {
		t.Fatalf("queue = %+v", f.queue())
	}
	if ds := f.decisions(j); ds[0].Kind != "copilot called" || !strings.HasPrefix(ds[0].Reason, "Failed at step 3: Pay") {
		t.Fatalf("decision %+v", ds[0])
	}
}

func TestFlakeCheckFailureNeverRetriesAgain(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.ended("r1", j, "", "", store.RunFailed, "Failed at step 3: Pay", failedAt3)
	f.st.FollowReplay("r1", store.ReplayFollow{})
	f.ended("r2", j, "flake check", "r1", store.RunFailed, "Failed at step 3: Pay", failedAt3)
	f.follow()
	for _, q := range f.queue() {
		if q.Kind == store.RunReplay {
			t.Fatalf("a failed check was checked again: %+v", q)
		}
	}
}

func TestStuckReplayCallsCopilot(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.ended("r1", j, "", "", store.RunStuck, "Waited for an email that never came", "")
	f.follow()
	q := f.queue()
	if len(q) != 1 || q[0].Via != "copilot" || ctxOf(t, q[0]).Replay.Why != "its wait never came" {
		t.Fatalf("queue = %+v", q)
	}
}

func TestKnownBugDoesNotCallCopilotAgain(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.done("ai", "Ada", "patient", "book", "ai", "found a bug")
	if err := f.st.AddBug(store.Bug{ID: "b1", RunID: "ai", Summary: "Pay button gone", Created: now}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetBlocked(j.ID, 3, "b1"); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"r1", "r2"} {
		f.ended(id, j, "", "", store.RunFailed, "Failed at step 3: Pay", failedAt3)
		f.follow()
		if q := f.queue(); len(q) != 0 {
			t.Fatalf("replay %d queued %+v", i, q)
		}
		if ds := f.decisions(j); ds[0].Kind != "still failing at a known bug" || ds[0].Actor != store.ActorDirector || ds[0].RunID != id {
			t.Fatalf("decision %+v", ds[0])
		}
	}
	// A failure at another step is new: it is checked again.
	f.ended("r3", j, "", "", store.RunFailed, "Failed at step 2", `{"status":"failed","step":2}`)
	f.follow()
	if q := f.queue(); len(q) != 1 || q[0].Via != "flake check" {
		t.Fatalf("other step: %+v", q)
	}
	f.st.DeleteQueued(f.queue()[0].ID)
	// Once the issue is fixed, the same failure is checked again too.
	b, _ := f.st.Report("b1")
	if err := f.st.SetIssueStatus(b.IssueID, store.IssueFixed, "", now); err != nil {
		t.Fatal(err)
	}
	for _, q := range f.queue() {
		f.st.DeleteQueued(q.ID) // the fix check it queued
	}
	f.ended("r4", j, "", "", store.RunFailed, "Failed at step 3: Pay", failedAt3)
	f.follow()
	if q := f.queue(); len(q) != 1 || q[0].Via != "flake check" {
		t.Fatalf("fixed issue: %+v", q)
	}
}

func TestCopilotOncePerDay(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.ended("r1", j, "", "", store.RunStuck, "stuck", "")
	f.follow()
	for _, q := range f.queue() {
		f.st.DeleteQueued(q.ID)
	}
	if err := f.st.SetJourneyState(j.ID, store.JourneyLive, store.Decision{Kind: "resumed", Actor: "kate", Created: now}); err != nil {
		t.Fatal(err)
	}
	f.clk.Add(23 * time.Hour)
	f.ended("r2", j, "", "", store.RunStuck, "stuck", "")
	f.follow()
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("second copilot within a day: %+v", q)
	}
	if ds := f.decisions(j); ds[0].Kind != "copilot not called" || !strings.HasPrefix(ds[0].Reason, "one copilot run per journey per day") {
		t.Fatalf("decision %+v", ds[0])
	}
	f.clk.Add(2 * time.Hour)
	f.ended("r3", j, "", "", store.RunStuck, "stuck", "")
	f.follow()
	if q := f.queue(); len(q) != 1 || q[0].Via != "copilot" {
		t.Fatalf("after a day: %+v", q)
	}
}

func TestPausedJourneyGetsNoCopilot(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	if err := f.st.SetJourneyState(j.ID, store.JourneyPaused, store.Decision{Kind: "paused", Actor: "kate", Created: now}); err != nil {
		t.Fatal(err)
	}
	f.ended("r1", j, "", "", store.RunStuck, "stuck", "")
	f.follow()
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("queue = %+v", q)
	}
	if ds := f.decisions(j); ds[0].Kind != "copilot not called" || ds[0].Reason != "the journey is paused" {
		t.Fatalf("decision %+v", ds[0])
	}
	if got := f.journeyNow(j); got.State != store.JourneyPaused {
		t.Fatalf("state %s", got.State)
	}
}

// A replay of an older revision than the live one is a person's look back:
// its failure checks nothing and calls no copilot.
func TestOldRevisionReplayFailureIsLeftAlone(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.journey("book", now)
	f.ended("r1", j, "", "", store.RunFailed, "Failed at step 3: Pay", failedAt3) // revision 1, live is 2
	f.follow()
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("queue = %+v", q)
	}
}

func TestReplayOfDeletedJourneyIsFollowed(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.ended("r1", j, "", "", store.RunFailed, "Failed at step 3: Pay", failedAt3)
	if err := f.st.DeleteJourney(j.ID, store.Decision{Kind: "deleted", Actor: "kate", Created: now}); err != nil {
		t.Fatal(err)
	}
	f.follow()
	if un, _ := f.st.UnfollowedReplays(); len(un) != 0 || len(f.queue()) != 0 {
		t.Fatalf("unfollowed %+v queue %+v", un, f.queue())
	}
}

// Replays never hand off: a passed replay of a story with hand-offs queues
// nothing.
func TestReplaysNeverHandOff(t *testing.T) {
	f := newFix(t, schedYAML)
	j, _, err := f.st.AddRevision("Ada", "book", store.Revision{Kind: store.RevisionFull, Code: "c", Reason: "r", Created: now},
		store.Decision{Kind: "recorded", Actor: store.ActorModel, Created: now, Explored: true})
	if err != nil {
		t.Fatal(err)
	}
	f.ended("r1", j, "", "", store.RunDone, "Passed", `{"status":"passed"}`)
	f.tick()
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("queue = %+v", q)
	}
}

// copilotEnded records a copilot run of Ada's story that ended in state.
func (f *fix) copilotEnded(id string, state store.RunState, outcome string) {
	f.t.Helper()
	r := store.Run{ID: id, Token: "tok-" + id, Persona: "Ada", Role: "patient", Story: "book", State: store.RunRunning,
		Started: f.clk.Now(), Chain: "c", Via: "copilot", Context: []byte(`{"kind":"replay_failed","replay":{"journeyId":"x"}}`)}
	if err := f.st.CreateRun(r); err != nil {
		f.t.Fatal(err)
	}
	if state != store.RunRunning {
		if ok, err := f.st.FinishRun(id, state, outcome, f.clk.Now()); err != nil || !ok {
			f.t.Fatal(ok, err)
		}
	}
}

func (f *fix) repairing(j store.Journey) {
	f.t.Helper()
	if err := f.st.SetJourneyState(j.ID, store.JourneyRepairing, store.Decision{Kind: "copilot called", Actor: store.ActorDirector, Created: f.clk.Now()}); err != nil {
		f.t.Fatal(err)
	}
}

func TestCopilotWithoutDecisionReturnsJourneyLive(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.repairing(j)
	f.copilotEnded("cp1", store.RunRunning, "")
	f.follow()
	if got := f.journeyNow(j); got.State != store.JourneyRepairing {
		t.Fatalf("while the copilot runs: %s", got.State)
	}
	if ok, err := f.st.FinishRun("cp1", store.RunFailed, "The run timed out after 60 min\nlogs", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.follow()
	if got := f.journeyNow(j); got.State != store.JourneyLive {
		t.Fatalf("after an undecided copilot: %s", got.State)
	}
	ds := f.decisions(j)
	if ds[0].Kind != "copilot gave no decision" || ds[0].Actor != store.ActorDirector || ds[0].RunID != "cp1" || ds[0].Reason != "The run timed out after 60 min" {
		t.Fatalf("decision %+v", ds[0])
	}
	f.follow()
	if n := len(f.decisions(j)); n != len(ds) {
		t.Fatalf("settled twice: %d rows", n)
	}
}

func TestQueuedCopilotKeepsJourneyRepairing(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.repairing(j)
	f.copilotEnded("old", store.RunFailed, "yesterday's copilot")
	if err := f.st.Enqueue(store.Queued{ID: "cq", Persona: "Ada", Role: "patient", Story: "book", Via: "copilot", Created: now}); err != nil {
		t.Fatal(err)
	}
	f.follow()
	if got := f.journeyNow(j); got.State != store.JourneyRepairing {
		t.Fatalf("with a copilot queued: %s", got.State)
	}
	f.st.DeleteQueued("cq")
	f.follow()
	if got := f.journeyNow(j); got.State != store.JourneyLive {
		t.Fatalf("copilot taken off the queue: %s", got.State)
	}
}

func TestDecidedCopilotIsLeftAlone(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.repairing(j)
	f.copilotEnded("cp1", store.RunDone, "decided")
	if err := f.st.AddDecision(store.Decision{JourneyID: j.ID, RunID: "cp1", Kind: "bug", Actor: store.ActorModel, Created: now}); err != nil {
		t.Fatal(err)
	}
	// Repairing again by a later replay, its copilot not yet queued: the
	// decided run is not "no decision".
	f.follow()
	if ds := f.decisions(j); ds[0].Kind == "copilot gave no decision" {
		t.Fatalf("decision %+v", ds[0])
	}
}

// parked stores a replay run of j parked at its journey's n-th wait w,
// carrying context ctx (a resume from an earlier wait, or nil).
func (f *fix) parked(id string, j store.Journey, n int, w store.Wait, ctx []byte) store.Run {
	f.t.Helper()
	r := store.Run{ID: id, Token: "tok-" + id, Persona: j.Persona, Role: "patient", Story: j.Story, State: store.RunRunning,
		Started: f.clk.Now(), Chain: "c", Kind: store.RunReplay, JourneyID: j.ID, Revision: j.LiveRev, Seed: 42, Context: ctx}
	if err := f.st.CreateRun(r); err != nil {
		f.t.Fatal(err)
	}
	res, _ := json.Marshal(map[string]any{"status": "waiting", "step": 1, "seed": 42, "wait": map[string]any{"n": n, "until": map[string]any{}}})
	if err := f.st.SetReplayResult(id, res); err != nil {
		f.t.Fatal(err)
	}
	if ok, err := f.st.FinishRunWaiting(id, "Waiting", f.clk.Now(), w); err != nil || !ok {
		f.t.Fatal(ok, err)
	}
	return r
}

func TestReplayWaitContinuesAsReplay(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.parked("w1", j, 1, timeWait(now.Add(time.Hour)), nil)
	f.clk.Add(30 * time.Minute)
	if err := f.s.resolveAll(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("before the time: %+v", q)
	}
	f.clk.Add(time.Hour)
	if err := f.s.resolveAll(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	q := f.queue()
	if len(q) != 1 || q[0].Kind != store.RunReplay || q[0].JourneyID != j.ID || q[0].Revision != 1 || q[0].Seed != 42 ||
		q[0].Via != "continue" || q[0].Follows != "w1" || q[0].Chain != "c" {
		t.Fatalf("queue = %+v", q)
	}
	c := ctxOf(t, q[0])
	if c.Kind != "continue" || c.Resume == nil || c.Resume.After != 1 || len(c.Resume.Arrivals) != 1 || c.Resume.Arrivals[0].Detail != "time reached" {
		t.Fatalf("context %+v %+v", c, c.Resume)
	}
	// The continuation waits again at the second wait: arrivals add up.
	f.st.DeleteQueued(q[0].ID)
	f.parked("w2", j, 2, timeWait(f.clk.Now().Add(time.Minute)), q[0].Context)
	f.clk.Add(2 * time.Minute)
	if err := f.s.resolveAll(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	c = ctxOf(t, f.queue()[0])
	if c.Resume.After != 2 || len(c.Resume.Arrivals) != 2 {
		t.Fatalf("second wait: %+v", c.Resume)
	}
}

func TestReplayStuckWaitHasNoFinding(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.parked("w1", j, 1, store.Wait{Kind: "email", Subject: "Confirm", Deadline: now.Add(time.Hour)}, nil)
	f.clk.Add(2 * time.Hour)
	if err := f.s.resolveAll(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if got := f.state("w1"); got != store.RunStuck {
		t.Fatalf("state %s", got)
	}
	if bugs, _ := f.st.Bugs("w1"); len(bugs) != 0 {
		t.Fatalf("a replay's missed wait filed %+v", bugs)
	}
	f.follow()
	if q := f.queue(); len(q) != 1 || q[0].Via != "copilot" {
		t.Fatalf("queue = %+v", q)
	}
}

// Review fixes.

func TestCopilotItemCarriesJourney(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.ended("r1", j, "", "", store.RunStuck, "stuck", `{"status":"waiting","step":1,"wait":{"n":1,"until":{}}}`)
	f.follow()
	if q := f.queue(); len(q) != 1 || q[0].JourneyID != j.ID || q[0].Kind != "" {
		t.Fatalf("queue = %+v", q)
	}
	if err := f.st.DeleteJourney(j.ID, store.Decision{Kind: "deleted", Actor: "kate", Created: now}); err != nil {
		t.Fatal(err)
	}
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("a deleted journey's copilot stays queued: %+v", q)
	}
}

func TestStuckReplayAtKnownBugCallsNoCopilot(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.done("ai", "Ada", "patient", "book", "ai", "found a bug")
	if err := f.st.AddBug(store.Bug{ID: "b1", RunID: "ai", Summary: "no confirmation email", Created: now}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetBlocked(j.ID, 1, "b1"); err != nil {
		t.Fatal(err)
	}
	f.ended("r1", j, "", "", store.RunStuck, "Waited for an email that never came", `{"status":"waiting","step":1,"wait":{"n":1,"until":{}}}`)
	f.follow()
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("queue = %+v", q)
	}
	if ds := f.decisions(j); ds[0].Kind != "still failing at a known bug" {
		t.Fatalf("decision %+v", ds[0])
	}
}

// A stuck replay does not take its story off the schedule.
func TestStuckReplayKeepsStoryScheduled(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.ended("r1", j, "", "", store.RunStuck, "stuck", "")
	f.st.FollowReplay("r1", store.ReplayFollow{})
	f.fireOnce(scheduleFire("book"))
	if q := f.queue(); len(q) != 1 || q[0].Story != "book" {
		t.Fatalf("queue = %+v", q)
	}
}

// A replay that stopped without a result (pod lost, timed out) is the
// director's trouble, not the journey's: no retry, no copilot.
func TestReplayWithoutResultCallsNoCopilot(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.ended("r1", j, "", "", store.RunFailed, "The replay stopped without a result", "")
	f.follow()
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("queue = %+v", q)
	}
	if ds := f.decisions(j); ds[0].Kind != "replay gave no result" || ds[0].Actor != store.ActorDirector {
		t.Fatalf("decision %+v", ds[0])
	}
}

// A resumed replay is not run again: what arrived (a one-time link) may be
// used up. It goes to the copilot, saying so.
func TestResumedReplayFailureGoesToCopilot(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	ctx := []byte(`{"kind":"continue","previousRun":"w1","previous":"x","resume":{"after":1,"arrivals":[{"detail":"email"}]}}`)
	r := store.Run{ID: "r1", Token: "tok-r1", Persona: "Ada", Role: "patient", Story: "book", State: store.RunRunning, Started: now,
		Chain: "c", Kind: store.RunReplay, JourneyID: j.ID, Revision: 1, Seed: 42, Via: "continue", Follows: "w1", Context: ctx}
	if err := f.st.CreateRun(r); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetReplayResult("r1", []byte(failedAt3)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.FinishRun("r1", store.RunFailed, "Failed at step 3: Pay", now); err != nil {
		t.Fatal(err)
	}
	f.follow()
	q := f.queue()
	if len(q) != 1 || q[0].Via != "copilot" || !strings.Contains(ctxOf(t, q[0]).Replay.Why, "used up") {
		t.Fatalf("queue = %+v", q)
	}
}

// A person's pause between the scheduler reading a journey and calling the
// copilot wins: no copilot, the journey stays paused.
func TestPauseDuringFollowWins(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	f.ended("r1", j, "", "", store.RunStuck, "stuck", "")
	stale, _ := f.st.Journey(j.ID)
	if err := f.st.SetJourneyState(j.ID, store.JourneyPaused, store.Decision{Kind: "paused", Actor: "kate", Created: now}); err != nil {
		t.Fatal(err)
	}
	r, _ := f.st.RunByID("r1")
	if err := f.s.copilot(stale, r, r, "its wait never came", now, store.ReplayFollow{}); err != nil {
		t.Fatal(err)
	}
	if got := f.journeyNow(j); got.State != store.JourneyPaused || len(f.queue()) != 0 {
		t.Fatalf("journey %s queue %+v", got.State, f.queue())
	}
	if un, _ := f.st.UnfollowedReplays(); len(un) != 1 {
		t.Fatal("the replay was marked followed; the next tick should look again")
	}
	f.follow()
	if ds := f.decisions(j); ds[0].Kind != "copilot not called" {
		t.Fatalf("decision %+v", ds[0])
	}
}

// Over budget, a queued AI item of a persona does not stop its scheduled
// replays.
func TestOverBudgetReplayPastQueuedAIItem(t *testing.T) {
	f := newFix(t, exploreYAML)
	f.starter.SetGate(runs.ErrOverBudget)
	f.journey("book", now.Add(-time.Hour))
	if err := f.st.Enqueue(store.Queued{ID: "ai", Persona: "Ada", Role: "patient", Story: "lone", Created: now}); err != nil {
		t.Fatal(err)
	}
	f.fireOnce(scheduleFire("book"))
	if q := f.queue(); len(q) != 2 || q[1].Kind != store.RunReplay {
		t.Fatalf("queue = %+v", q)
	}
}

func TestFollowSkipsFixCheckReplay(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	before := len(f.decisions(j))
	ctx, _ := json.Marshal(store.RunContext{Kind: "verify", Verify: &store.ContextVerify{IssueID: "i1", JourneyID: j.ID, Revision: 1, JourneyStep: 3}})
	r := store.Run{ID: "fc", Token: "tok-fc", Persona: j.Persona, Role: "patient", Story: j.Story, State: store.RunRunning, Started: f.clk.Now(),
		Chain: "c-fc", Kind: store.RunReplay, JourneyID: j.ID, Revision: j.LiveRev, Seed: 42, Context: ctx}
	if err := f.st.CreateRun(r); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetReplayResult(r.ID, json.RawMessage(`{"status":"failed","step":2,"stepName":"Fill","seed":42}`)); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.st.FinishRun(r.ID, store.RunFailed, "Failed at step 2: Fill", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.follow()
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("a fix-check replay was followed up: %+v", q)
	}
	if ds := f.decisions(j); len(ds) != before {
		t.Fatalf("decisions %+v", ds[:len(ds)-before])
	}
	if un, _ := f.st.UnfollowedReplays(); len(un) != 0 {
		t.Fatalf("left unfollowed: %+v", un)
	}
}

func TestFixCheckReplayContinuationCarriesVerify(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	ctx, _ := json.Marshal(store.RunContext{Kind: "verify", Verify: &store.ContextVerify{IssueID: "i1", JourneyID: j.ID, Revision: 1, JourneyStep: 3}})
	f.parked("w1", j, 1, timeWait(now.Add(time.Minute)), ctx)
	f.clk.Add(2 * time.Minute)
	if err := f.s.resolveAll(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	q := f.queue()
	if len(q) != 1 || q[0].Kind != store.RunReplay {
		t.Fatalf("queue %+v", q)
	}
	if c := ctxOf(t, q[0]); c.Verify == nil || c.Verify.IssueID != "i1" || c.Verify.JourneyStep != 3 || c.Resume == nil {
		t.Fatalf("context %+v", c)
	}
}

func TestMissedWaitInFixCheckReplayHandsOver(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now)
	if err := f.st.AddBug(store.Bug{ID: "b1", RunID: "found", Summary: "total wrong", Created: now}); err != nil {
		t.Fatal(err)
	}
	bugs, _ := f.st.Bugs("found")
	issue := bugs[0].IssueID
	if ok, err := f.st.StartCheck(issue, store.Queued{ID: "check", Chain: "c", Persona: j.Persona, Role: "patient", Story: j.Story, Created: now}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	ctx, _ := json.Marshal(store.RunContext{Kind: "verify", Verify: &store.ContextVerify{IssueID: issue, Summary: "total wrong", JourneyID: j.ID, Revision: 1, JourneyStep: 3}})
	f.parked("w1", j, 1, store.Wait{Kind: "email", Subject: "Confirm", Deadline: now.Add(time.Hour)}, ctx)
	f.clk.Add(2 * time.Hour)
	if err := f.s.resolveAll(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if is, _ := f.st.Issue(issue); is.Status != store.IssueVerifying {
		t.Fatalf("issue %+v", is)
	}
	var next *store.Queued
	for _, q := range f.queue() {
		if q.Follows == "w1" {
			next = &q
		}
	}
	if next == nil || next.Kind == store.RunReplay || next.Chain != "c" {
		t.Fatalf("queue %+v", f.queue())
	}
	if c := ctxOf(t, *next); c.Verify == nil || !strings.Contains(c.Verify.ReplayNote, "wait") {
		t.Fatalf("context %+v", c.Verify)
	}
}

func TestDrainHandsUnplayableFixCheckToFallback(t *testing.T) {
	for _, falls := range []bool{true, false} {
		t.Run(fmt.Sprint("falls back ", falls), func(t *testing.T) {
			f := newFix(t, exploreYAML)
			if err := f.st.Enqueue(store.Queued{ID: "q1", Persona: "Ada", Role: "patient", Story: "book", Created: now,
				Kind: store.RunReplay, JourneyID: "gone", Revision: 1, Seed: 7}); err != nil {
				t.Fatal(err)
			}
			f.starter.errs = []error{fmt.Errorf("%w: its journey is paused", runs.ErrInvalid)}
			f.starter.fallback = func(store.Queued) bool { return falls }
			if err := f.s.drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(f.starter.fellBack) != 1 || f.starter.fellBack[0].ID != "q1" {
				t.Fatalf("fallback calls %+v", f.starter.fellBack)
			}
			// A fallback replaces the item itself; otherwise the item is dropped.
			if q := f.queue(); falls != (len(q) == 1) {
				t.Fatalf("queue %+v", q)
			}
		})
	}
}
