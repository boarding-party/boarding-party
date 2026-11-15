package store

import (
	"testing"
	"time"
)

func TestDecisionsSinceFiltersKindAndTime(t *testing.T) {
	s := open(t)
	j, _ := addRev(t, s, RevisionFull, "a", t0)
	since := t0.Add(time.Hour)
	for _, d := range []Decision{
		{Kind: "copilot called", Reason: "before", Created: since.Add(-time.Minute)},
		{Kind: "copilot called", Reason: "second", Created: since.Add(2 * time.Minute)},
		{Kind: "rolled back", Reason: "first", Created: since},
		{Kind: "replayed", Reason: "other kind", Created: since.Add(time.Minute)},
	} {
		d.JourneyID, d.Actor = j.ID, ActorDirector
		if err := s.AddDecision(d); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DecisionsSince(since, "copilot called", "rolled back")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Reason != "first" || got[1].Reason != "second" {
		t.Fatalf("decisions %+v, want first then second", got)
	}
	if none, err := s.DecisionsSince(since); err != nil || len(none) != 0 {
		t.Fatalf("no kinds: %+v %v, want none", none, err)
	}
}

func TestRunsFinishedSince(t *testing.T) {
	s := open(t)
	since := t0.Add(time.Hour)
	for _, r := range []Run{
		{ID: "old", State: RunDone, Finished: since.Add(-time.Minute)},
		{ID: "failed", State: RunFailed, Finished: since.Add(2 * time.Minute)},
		{ID: "done", State: RunDone, Finished: since.Add(time.Minute)},
		{ID: "stuck", State: RunStuck, Finished: since.Add(3 * time.Minute)},
		{ID: "running", State: RunRunning},
		{ID: "waiting", State: RunWaiting, Finished: since.Add(time.Minute)},
	} {
		r.Token, r.Started, r.Chain = "tok-"+r.ID, t0, r.ID
		if err := s.CreateRun(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RunsFinishedSince(since)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if len(ids) != 3 || ids[0] != "done" || ids[1] != "failed" || ids[2] != "stuck" {
		t.Fatalf("runs %v, want done failed stuck", ids)
	}
}
