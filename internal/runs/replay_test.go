package runs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// recorded gives ann's checkout journey n full revisions and returns it.
func recorded(t *testing.T, svc *Service, n int) store.Journey {
	t.Helper()
	var j store.Journey
	for i := 0; i < n; i++ {
		var err error
		j, _, err = svc.Store.AddRevision("ann", "checkout", store.Revision{Kind: store.RevisionFull, Code: journeyCode, Reason: "r", RunID: "old", Created: time.Now().UTC()},
			store.Decision{Kind: "recorded", Reason: "r", Actor: store.ActorModel, Created: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
	}
	return j
}

func TestStartReplayRunsTheLiveRevision(t *testing.T) {
	svc, fl := newSvc(t)
	j := recorded(t, svc, 2)
	r, err := svc.StartReplay(context.Background(), j.ID, 0, 0, "kate@shop.test")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != store.RunReplay || r.Revision != 2 || r.Seed == 0 || r.JourneyID != j.ID || r.Persona != "ann" || r.Story != "checkout" ||
		r.Role != "buyer" || r.State != store.RunRunning || len(fl.started) != 1 {
		t.Fatalf("run %+v started %v", r, fl.started)
	}
	got, _ := svc.Store.RunByID(r.ID)
	if got.Kind != store.RunReplay || got.Revision != 2 || got.Seed != r.Seed || got.JourneyID != j.ID {
		t.Fatalf("stored %+v", got)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if ds[0].Kind != "replayed" || ds[0].Actor != "kate@shop.test" || ds[0].RunID != r.ID || ds[0].Reason != "Replayed revision 2" {
		t.Fatalf("decision %+v", ds[0])
	}
	if _, err := svc.Store.FinishRun(r.ID, store.RunDone, "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	old, err := svc.StartReplay(context.Background(), j.ID, 1, 99, "owner")
	if err != nil || old.Revision != 1 || old.Seed != 99 {
		t.Fatalf("old revision %+v %v", old, err)
	}
}

func TestStartReplayRefusesStaleJourney(t *testing.T) {
	svc, fl := newSvc(t)
	j := recorded(t, svc, 2)
	if _, err := svc.StartReplay(context.Background(), j.ID, 9, 0, "owner"); !errors.Is(err, ErrStaleJourney) {
		t.Fatalf("revision 9 of 2: %v", err)
	}
	if _, err := svc.StartReplay(context.Background(), "nope", 0, 0, "owner"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing journey: %v", err)
	}
	p, _ := svc.Store.Project()
	p.Roles[0].Stories = []project.Story{{Name: "refund", Goal: "g"}}
	if err := svc.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartReplay(context.Background(), j.ID, 0, 0, "owner"); !errors.Is(err, ErrStaleJourney) {
		t.Fatalf("story removed: %v", err)
	}
	if runs, _ := svc.Store.Runs(10); len(runs) != 0 || len(fl.started) != 0 {
		t.Fatalf("a refused replay made runs %+v", runs)
	}
}

func TestStartReplayNeedsALiveRevision(t *testing.T) {
	svc, _ := newSvc(t)
	j, err := svc.Store.EnsureJourney("ann", "checkout", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartReplay(context.Background(), j.ID, 0, 0, "owner"); !errors.Is(err, ErrStaleJourney) {
		t.Fatalf("journey without a live revision: %v", err)
	}
}

func TestFinishReplayPassedAndFailed(t *testing.T) {
	svc, fl := newSvc(t)
	j := recorded(t, svc, 1)
	r, err := svc.StartReplay(context.Background(), j.ID, 0, 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishReplay(r.Token, ReplayResult{Status: "passed", Seed: r.Seed}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Store.RunByID(r.ID)
	if got.State != store.RunDone || got.Outcome != "Passed" || len(fl.stopped) != 1 {
		t.Fatalf("passed run %+v stopped %v", got, fl.stopped)
	}
	if err := svc.FinishReplay(r.Token, ReplayResult{Status: "passed"}, time.Now()); !errors.Is(err, ErrNotActive) {
		t.Fatalf("second report: %v", err)
	}
	r2, err := svc.StartReplay(context.Background(), j.ID, 0, 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishReplay(r2.Token, ReplayResult{Status: "failed", Step: 4, StepName: "Fill the business", Error: "locator not found"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Store.RunByID(r2.ID)
	if got.State != store.RunFailed || got.Outcome != "Failed at step 4: Fill the business\nlocator not found" {
		t.Fatalf("failed run %+v", got)
	}
	jj, _ := svc.Store.Journey(j.ID)
	if jj.Passed != 1 || jj.Failed != 1 {
		t.Fatalf("counters %+v", jj)
	}
	if err := svc.FinishReplay(r2.Token, ReplayResult{Status: "weird"}, time.Now()); err == nil {
		t.Fatal("bad status accepted")
	}
}

func TestFinishReplayRefusesAnAIRun(t *testing.T) {
	svc, _ := newSvc(t)
	r := startedRun(t, svc)
	if err := svc.FinishReplay(r.Token, ReplayResult{Status: "passed"}, time.Now()); err == nil {
		t.Fatal("an AI run took a replay result")
	}
}

func TestReplayWithoutResultFails(t *testing.T) {
	for _, phase := range []string{"Failed", "Done", "gone"} {
		svc, fl := newSvc(t)
		j := recorded(t, svc, 1)
		r, err := svc.StartReplay(context.Background(), j.ID, 0, 0, "owner")
		if err != nil {
			t.Fatal(err)
		}
		fl.phases = map[string]phaseInfo{}
		if phase != "gone" {
			fl.phases[r.Session] = phaseInfo{phase: phase, msg: "pod died"}
		}
		if err := svc.Reconcile(context.Background(), time.Now(), time.Hour); err != nil {
			t.Fatal(err)
		}
		got, _ := svc.Store.RunByID(r.ID)
		if got.State != store.RunFailed || got.Outcome != "The replay stopped without a result" {
			t.Fatalf("%s: %+v", phase, got)
		}
		if jj, _ := svc.Store.Journey(j.ID); jj.Failed != 1 {
			t.Fatalf("%s: counters %+v", phase, jj)
		}
	}
}

func TestReplayIgnoresBudgetButNotPause(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	spendRunning(t, svc, "spent", time.Now().Add(-time.Second))
	if _, err := svc.Start(context.Background(), "ann", "checkout"); !errors.Is(err, ErrOverBudget) {
		t.Fatalf("AI run over budget: %v", err)
	}
	r, err := svc.StartReplay(context.Background(), j.ID, 0, 0, "owner")
	if err != nil {
		t.Fatalf("replay over budget: %v", err)
	}
	if _, err := svc.Store.FinishRun(r.ID, store.RunDone, "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartReplay(context.Background(), j.ID, 0, 0, "owner"); !errors.Is(err, ErrPaused) {
		t.Fatalf("paused replay: %v", err)
	}
}

func TestFinishReplayKeepsTheResult(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	r, err := svc.StartReplay(context.Background(), j.ID, 0, 3, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishReplay(r.Token, ReplayResult{Status: "passed", Seed: 3, Inputs: map[string]any{"int1": 4}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rc, err := svc.Artifacts.Open(r.ID, ReplayResultName)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var got ReplayResult
	if err := json.NewDecoder(rc).Decode(&got); err != nil || got.Seed != 3 || got.Inputs["int1"] != float64(4) {
		t.Fatalf("%+v %v", got, err)
	}
}
