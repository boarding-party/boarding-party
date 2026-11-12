package store

import (
	"errors"
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
	j, rev, err := s.AddRevision("megan", "invoice", r, Decision{RunID: r.RunID, Kind: dk, Reason: r.Reason, Actor: "model", Created: at})
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
