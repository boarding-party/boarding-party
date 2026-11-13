package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func addRev(t *testing.T, s *Store, kind RevisionKind, code string, at time.Time) (Journey, Revision) {
	t.Helper()
	r := Revision{Kind: kind, Code: code, Reason: "why " + code, RunID: "run-" + code, Created: at}
	dk := "recorded"
	if kind == RevisionBug {
		r.BugID, r.BugStep, dk = "bug-"+code, 3, "bug revision recorded"
	}
	j, rev, err := s.AddRevision("megan", "invoice", r, Decision{RunID: r.RunID, Kind: dk, Reason: r.Reason, Actor: "model", Created: at, Explored: true})
	if err != nil {
		t.Fatal(err)
	}
	return j, rev
}

func TestAddRevisionCreatesJourneyAndGoesLive(t *testing.T) {
	s := open(t)
	j, rev := addRev(t, s, RevisionFull, "a", t0)
	if j.ID == "" || j.Persona != "megan" || j.Story != "invoice" || j.State != JourneyLive || j.LiveRev != 1 || rev.N != 1 {
		t.Fatalf("journey %+v rev %+v", j, rev)
	}
	got, err := s.JourneyFor("megan", "invoice")
	if err != nil || got.ID != j.ID || got.LiveRev != 1 || !got.Created.Equal(t0) {
		t.Fatalf("JourneyFor %+v %v", got, err)
	}
	ds, err := s.Decisions(j.ID)
	if err != nil || len(ds) != 1 || ds[0].Kind != "recorded" || ds[0].RevAfter != 1 || ds[0].RevBefore != 0 ||
		ds[0].Actor != "model" || ds[0].RunID != "run-a" || ds[0].JourneyID != j.ID {
		t.Fatalf("decisions %+v %v", ds, err)
	}
	if _, err := s.JourneyFor("megan", "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing journey: %v", err)
	}
}

func TestBugRevisionNeverGoesLive(t *testing.T) {
	s := open(t)
	addRev(t, s, RevisionFull, "a", t0)
	j, rev := addRev(t, s, RevisionBug, "b", t0.Add(time.Hour))
	if j.LiveRev != 1 || rev.N != 2 {
		t.Fatalf("journey %+v rev %+v", j, rev)
	}
	live, err := s.Revision(j.ID, 0)
	if err != nil || live.N != 1 || live.Code != "a" {
		t.Fatalf("live %+v %v", live, err)
	}
	bug, err := s.Revision(j.ID, 2)
	if err != nil || bug.Kind != RevisionBug || bug.BugID != "bug-b" || bug.BugStep != 3 {
		t.Fatalf("bug rev %+v %v", bug, err)
	}
	ds, _ := s.Decisions(j.ID)
	if ds[0].RevBefore != 1 || ds[0].RevAfter != 1 {
		t.Fatalf("a bug revision moved the live one: %+v", ds[0])
	}
}

func TestBugRevisionOnNewJourneyLeavesNoLive(t *testing.T) {
	s := open(t)
	j, rev := addRev(t, s, RevisionBug, "a", t0)
	if j.LiveRev != 0 || rev.N != 1 {
		t.Fatalf("journey %+v rev %+v", j, rev)
	}
	if _, err := s.Revision(j.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("live of bug-only journey: %v", err)
	}
	if _, err := s.Revision(j.ID, 7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing revision: %v", err)
	}
}

func TestRevisionNumbersOnlyGrow(t *testing.T) {
	s := open(t)
	addRev(t, s, RevisionFull, "a", t0)
	addRev(t, s, RevisionBug, "b", t0.Add(time.Minute))
	j, _ := addRev(t, s, RevisionFull, "c", t0.Add(2*time.Minute))
	revs, err := s.Revisions(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ns []int
	for _, r := range revs {
		ns = append(ns, r.N)
	}
	if !slices.Equal(ns, []int{3, 2, 1}) {
		t.Fatalf("revisions %v, want newest first", ns)
	}
	if revs[0].Parent != 1 || revs[1].Parent != 1 || revs[2].Parent != 0 {
		t.Fatalf("parents %d %d %d", revs[0].Parent, revs[1].Parent, revs[2].Parent)
	}
	if j.LiveRev != 3 {
		t.Fatalf("live %d", j.LiveRev)
	}
	all, err := s.Journeys()
	if err != nil || len(all) != 1 || all[0].ID != j.ID {
		t.Fatalf("journeys %+v %v", all, err)
	}
}

func TestDecisionsKeepEvidenceAndOrder(t *testing.T) {
	s := open(t)
	j, _ := addRev(t, s, RevisionFull, "a", t0)
	if err := s.AddDecision(Decision{JourneyID: j.ID, RunID: "r2", Kind: "confirmed", Reason: "same flow", Actor: "model",
		Evidence: []string{"trace.zip", "step-2.png"}, RevBefore: 1, RevAfter: 1, Created: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDecision(Decision{JourneyID: "nope", Kind: "confirmed", Reason: "x", Actor: "model", Created: t0}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("decision on missing journey: %v", err)
	}
	ds, err := s.Decisions(j.ID)
	if err != nil || len(ds) != 2 {
		t.Fatalf("%+v %v", ds, err)
	}
	if ds[0].Kind != "confirmed" || !slices.Equal(ds[0].Evidence, []string{"trace.zip", "step-2.png"}) || ds[0].ID == "" {
		t.Fatalf("newest %+v", ds[0])
	}
	if ds[1].Kind != "recorded" || ds[1].Evidence == nil {
		t.Fatalf("oldest %+v (evidence must be an empty list, not nil)", ds[1])
	}
}

func TestCountReplay(t *testing.T) {
	s := open(t)
	j, _ := addRev(t, s, RevisionFull, "a", t0)
	if err := s.CountReplay(j.ID, true, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.CountReplay(j.ID, false, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Journey(j.ID)
	if err != nil || got.Passed != 1 || got.Failed != 1 || !got.LastReplay.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.CountReplay("nope", true, t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestEnsureJourneyAndRunDecided(t *testing.T) {
	s := open(t)
	j, err := s.EnsureJourney("megan", "invoice", t0)
	if err != nil || j.ID == "" || j.LiveRev != 0 || j.State != JourneyLive {
		t.Fatalf("%+v %v", j, err)
	}
	again, err := s.EnsureJourney("megan", "invoice", t0.Add(time.Hour))
	if err != nil || again.ID != j.ID || !again.Created.Equal(t0) {
		t.Fatalf("second ensure %+v %v", again, err)
	}
	if ok, err := s.RunDecided("r1"); err != nil || ok {
		t.Fatalf("undecided: %v %v", ok, err)
	}
	if err := s.AddDecision(Decision{JourneyID: j.ID, RunID: "r1", Kind: "not recorded", Reason: "x", Actor: ActorModel, Created: t0}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.RunDecided("r1"); err != nil || !ok {
		t.Fatalf("decided: %v %v", ok, err)
	}
}

func TestRollbackCopiesOldCode(t *testing.T) {
	s := open(t)
	addRev(t, s, RevisionFull, "a", t0)
	j, _ := addRev(t, s, RevisionFull, "b", t0.Add(time.Minute))
	at := t0.Add(time.Hour)
	j, rev, err := s.Rollback(j.ID, 1, Decision{Kind: "rolled back", Reason: "Rolled back to revision 1 by kate", Actor: "kate", Created: at})
	if err != nil {
		t.Fatal(err)
	}
	if rev.N != 3 || rev.Kind != RevisionFull || rev.Code != "a" || rev.Parent != 2 || rev.Reason != "Rolled back to revision 1 by kate" || j.LiveRev != 3 {
		t.Fatalf("journey %+v rev %+v", j, rev)
	}
	if got, _ := s.Journey(j.ID); got.LiveRev != 3 || !got.LastExplored.Equal(t0.Add(time.Minute)) {
		t.Fatalf("stored journey %+v (a rollback is not exploring)", got)
	}
	ds, _ := s.Decisions(j.ID)
	if ds[0].Kind != "rolled back" || ds[0].RevBefore != 2 || ds[0].RevAfter != 3 || ds[0].Actor != "kate" {
		t.Fatalf("decision %+v", ds[0])
	}
	if _, _, err := s.Rollback(j.ID, 9, Decision{Kind: "rolled back", Actor: "kate", Created: at}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing revision: %v", err)
	}
	if _, _, err := s.Rollback("nope", 1, Decision{Kind: "rolled back", Actor: "kate", Created: at}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing journey: %v", err)
	}
}

func TestRollbackRefusesBugRevision(t *testing.T) {
	s := open(t)
	addRev(t, s, RevisionFull, "a", t0)
	j, _ := addRev(t, s, RevisionBug, "b", t0.Add(time.Minute))
	if _, _, err := s.Rollback(j.ID, 2, Decision{Kind: "rolled back", Actor: "kate", Created: t0}); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("rollback to a bug revision: %v", err)
	}
	if revs, _ := s.Revisions(j.ID); len(revs) != 2 {
		t.Fatalf("revisions %+v", revs)
	}
}

func TestDeleteJourneyKeepsDecisions(t *testing.T) {
	s := open(t)
	j, _ := addRev(t, s, RevisionFull, "a", t0)
	if err := s.DeleteJourney(j.ID, Decision{Kind: "deleted", Reason: "Deleted by kate", Actor: "kate", Created: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Journey(j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("journey still there: %v", err)
	}
	if revs, _ := s.Revisions(j.ID); len(revs) != 0 {
		t.Fatalf("revisions kept: %+v", revs)
	}
	ds, _ := s.Decisions(j.ID)
	if len(ds) != 2 || ds[0].Kind != "deleted" || ds[0].Actor != "kate" || ds[0].RevBefore != 1 || ds[1].Kind != "recorded" {
		t.Fatalf("decisions %+v", ds)
	}
	if err := s.DeleteJourney(j.ID, Decision{Kind: "deleted", Actor: "kate", Created: t0}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestDeleteJourneyDropsQueuedReplays(t *testing.T) {
	s := open(t)
	j, _ := addRev(t, s, RevisionFull, "a", t0)
	for _, q := range []Queued{
		{ID: "rp", Persona: "megan", Story: "invoice", Kind: RunReplay, JourneyID: j.ID, Seed: 3, Created: t0},
		{ID: "ai", Persona: "megan", Story: "invoice", Created: t0},
	} {
		if err := s.Enqueue(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteJourney(j.ID, Decision{Kind: "deleted", Actor: "kate", Created: t0}); err != nil {
		t.Fatal(err)
	}
	if q, _ := s.Queue(); len(q) != 1 || q[0].ID != "ai" {
		t.Fatalf("queue %+v", q)
	}
}

func TestSetJourneyStateRecords(t *testing.T) {
	s := open(t)
	j, _ := addRev(t, s, RevisionFull, "a", t0)
	if err := s.SetJourneyState(j.ID, JourneyPaused, Decision{Kind: "paused", Reason: "Paused by kate", Actor: "kate", Created: t0}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Journey(j.ID); got.State != JourneyPaused {
		t.Fatalf("state %s", got.State)
	}
	if ds, _ := s.Decisions(j.ID); ds[0].Kind != "paused" || ds[0].RevBefore != 1 || ds[0].RevAfter != 1 {
		t.Fatalf("decision %+v", ds[0])
	}
	if err := s.SetJourneyState("nope", JourneyLive, Decision{Kind: "resumed", Actor: "kate", Created: t0}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing journey: %v", err)
	}
}

func TestFollowReplayIsOneStep(t *testing.T) {
	s := open(t)
	j, _ := addRev(t, s, RevisionFull, "a", t0)
	for _, r := range []Run{
		{ID: "rp", Token: "t-rp", Persona: "megan", Story: "invoice", State: RunRunning, Started: t0, Chain: "rp", Kind: RunReplay, JourneyID: j.ID, Revision: 1, Seed: 5},
		{ID: "ai", Token: "t-ai", Persona: "megan", Story: "invoice", State: RunRunning, Started: t0, Chain: "ai"},
	} {
		if err := s.CreateRun(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetReplayResult("rp", []byte(`{"status":"failed","step":2}`)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"rp", "ai"} {
		if _, err := s.FinishRun(id, RunFailed, "Failed at step 2: Pay", t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	un, err := s.UnfollowedReplays()
	if err != nil || len(un) != 1 || un[0].ID != "rp" || string(un[0].ReplayResult) != `{"status":"failed","step":2}` {
		t.Fatalf("unfollowed %+v %v", un, err)
	}
	streak := 2
	at := t0.Add(time.Hour)
	err = s.FollowReplay("rp", ReplayFollow{
		Next:     []Queued{{ID: "q", Persona: "megan", Story: "invoice", Kind: RunReplay, JourneyID: j.ID, Seed: 5, Created: at}},
		Decisions: []Decision{{JourneyID: j.ID, Kind: "copilot called", Reason: "r", Actor: ActorDirector, Created: at}},
		State:    JourneyRepairing, FlakyStreak: &streak, CopilotAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if un, _ := s.UnfollowedReplays(); len(un) != 0 {
		t.Fatalf("still unfollowed %+v", un)
	}
	got, _ := s.Journey(j.ID)
	if got.State != JourneyRepairing || got.FlakyStreak != 2 || !got.CopilotAt.Equal(at) {
		t.Fatalf("journey %+v", got)
	}
	if q, _ := s.Queue(); len(q) != 1 || q[0].ID != "q" {
		t.Fatalf("queue %+v", q)
	}
	if ds, _ := s.Decisions(j.ID); ds[0].Kind != "copilot called" || ds[0].RevBefore != 1 || ds[0].RevAfter != 1 {
		t.Fatalf("decision %+v", ds[0])
	}
	if err := s.SetBlocked(j.ID, 3, "bug-1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Journey(j.ID); got.BlockedStep != 3 || got.BlockedBug != "bug-1" {
		t.Fatalf("blocked %+v", got)
	}
	if err := s.FollowReplay("rp", ReplayFollow{ClearBlocked: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second follow: %v", err)
	}
}

// Replays finished before replays were followed must not be acted on.
func TestOldReplaysCountAsFollowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bp.db")
	s := reopen(t, path)
	if err := s.CreateRun(Run{ID: "rp", Token: "t", State: RunRunning, Started: t0, Chain: "rp", Kind: RunReplay, JourneyID: "j", Seed: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishRun("rp", RunFailed, "x", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE runs SET followed = 0; ALTER TABLE runs DROP COLUMN replay_result`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = reopen(t, path)
	if un, err := s.UnfollowedReplays(); err != nil || len(un) != 0 {
		t.Fatalf("old replay unfollowed: %+v %v", un, err)
	}
}
