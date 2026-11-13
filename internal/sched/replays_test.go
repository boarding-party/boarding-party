package sched

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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
		store.Decision{Kind: "recorded", Actor: store.ActorModel, Created: now})
	if err != nil {
		t.Fatal(err)
	}
	f.ended("r1", j, "", "", store.RunDone, "Passed", `{"status":"passed"}`)
	f.tick()
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("queue = %+v", q)
	}
}
