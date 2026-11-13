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

// replay queues a replay of journeyID and starts it, as the scheduler does.
func replay(t *testing.T, svc *Service, journeyID string, revision int, seed int64, actor string) store.Run {
	t.Helper()
	q, err := svc.EnqueueReplay(journeyID, revision, seed, actor, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.StartQueued(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEnqueueReplayThenStart(t *testing.T) {
	svc, fl := newSvc(t)
	j := recorded(t, svc, 2)
	q, err := svc.EnqueueReplay(j.ID, 0, 0, "kate@shop.test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if q.Kind != store.RunReplay || q.JourneyID != j.ID || q.Revision != 0 || q.Seed < 1 || q.Seed > MaxSeed ||
		q.Persona != "ann" || q.Story != "checkout" || q.Role != "buyer" || q.Chain != "" {
		t.Fatalf("queued %+v", q)
	}
	if got, _ := svc.Store.Queue(); len(got) != 1 || got[0].Seed != q.Seed {
		t.Fatalf("queue %+v", got)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if ds[0].Kind != "replayed" || ds[0].Actor != "kate@shop.test" || ds[0].RunID != "" || ds[0].Reason != "Replay of revision 2 asked" {
		t.Fatalf("decision %+v", ds[0])
	}
	r, err := svc.StartQueued(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != store.RunReplay || r.Revision != 2 || r.Seed != q.Seed || r.JourneyID != j.ID || r.Persona != "ann" || r.Story != "checkout" ||
		r.Role != "buyer" || r.State != store.RunRunning || len(fl.started) != 1 {
		t.Fatalf("run %+v started %v", r, fl.started)
	}
	got, _ := svc.Store.RunByID(r.ID)
	if got.Kind != store.RunReplay || got.Revision != 2 || got.Seed != r.Seed || got.JourneyID != j.ID {
		t.Fatalf("stored %+v", got)
	}
	if qs, _ := svc.Store.Queue(); len(qs) != 0 {
		t.Fatalf("item not taken: %+v", qs)
	}
	if _, err := svc.Store.FinishRun(r.ID, store.RunDone, "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	old := replay(t, svc, j.ID, 1, 99, "owner")
	if old.Revision != 1 || old.Seed != 99 {
		t.Fatalf("old revision %+v", old)
	}
}

func TestEnqueueReplayRefusesStaleJourney(t *testing.T) {
	svc, fl := newSvc(t)
	j := recorded(t, svc, 2)
	if _, err := svc.EnqueueReplay(j.ID, 9, 0, "owner", time.Now()); !errors.Is(err, ErrStaleJourney) {
		t.Fatalf("revision 9 of 2: %v", err)
	}
	if _, err := svc.EnqueueReplay("nope", 0, 0, "owner", time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing journey: %v", err)
	}
	if _, err := svc.EnqueueReplay(j.ID, 0, MaxSeed+1, "owner", time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("seed past MaxSeed: %v", err)
	}
	p, _ := svc.Store.Project()
	p.Roles[0].Stories = []project.Story{{Name: "refund", Goal: "g"}}
	if err := svc.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnqueueReplay(j.ID, 0, 0, "owner", time.Now()); !errors.Is(err, ErrStaleJourney) {
		t.Fatalf("story removed: %v", err)
	}
	if runs, _ := svc.Store.Runs(10); len(runs) != 0 || len(fl.started) != 0 {
		t.Fatalf("a refused replay made runs %+v", runs)
	}
	if qs, _ := svc.Store.Queue(); len(qs) != 0 {
		t.Fatalf("a refused replay queued %+v", qs)
	}
}

func TestEnqueueReplayNeedsALiveRevision(t *testing.T) {
	svc, _ := newSvc(t)
	j, err := svc.Store.EnsureJourney("ann", "checkout", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnqueueReplay(j.ID, 0, 0, "owner", time.Now()); !errors.Is(err, ErrStaleJourney) {
		t.Fatalf("journey without a live revision: %v", err)
	}
}

// A journey paused (or its story removed) while its replay waits in the
// queue: the item is refused as invalid, so the scheduler drops it.
func TestQueuedReplayOfPausedJourneyIsDropped(t *testing.T) {
	svc, fl := newSvc(t)
	j := recorded(t, svc, 1)
	q, err := svc.EnqueueReplay(j.ID, 0, 0, "owner", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.SetJourneyState(j.ID, store.JourneyPaused, store.Decision{Kind: "paused", Reason: "Paused by owner", Actor: "owner", Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartQueued(context.Background(), q); !errors.Is(err, ErrInvalid) {
		t.Fatalf("paused journey: %v", err)
	}
	if _, err := svc.EnqueueReplay(j.ID, 0, 0, "owner", time.Now()); !errors.Is(err, ErrStaleJourney) {
		t.Fatalf("enqueue of a paused journey: %v", err)
	}
	q.JourneyID = "gone"
	if _, err := svc.StartQueued(context.Background(), q); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing journey: %v", err)
	}
	if runs, _ := svc.Store.Runs(10); len(runs) != 0 || len(fl.started) != 0 {
		t.Fatalf("runs %+v", runs)
	}
}

func TestFinishReplayPassedAndFailed(t *testing.T) {
	svc, fl := newSvc(t)
	j := recorded(t, svc, 1)
	r := replay(t, svc, j.ID, 0, 0, "owner")
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
	r2 := replay(t, svc, j.ID, 0, 0, "owner")
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
		r := replay(t, svc, j.ID, 0, 0, "owner")
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
	r := replay(t, svc, j.ID, 0, 0, "owner")
	if _, err := svc.Store.FinishRun(r.ID, store.RunDone, "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	q, err := svc.EnqueueReplay(j.ID, 0, 0, "owner", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartQueued(context.Background(), q); !errors.Is(err, ErrPaused) {
		t.Fatalf("paused replay: %v", err)
	}
	if qs, _ := svc.Store.Queue(); len(qs) != 1 {
		t.Fatalf("a paused board drops the item: %+v", qs)
	}
}

func TestFinishReplayKeepsTheResult(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	r := replay(t, svc, j.ID, 0, 3, "owner")
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

// A replay pod idles after bp-replay exits, so a replay that never reports is
// caught by its own timeout: the journey's 10 minutes plus start-up, well
// before an AI run's.
func TestReplayTimesOutSooner(t *testing.T) {
	svc, fl := newSvc(t)
	j := recorded(t, svc, 1)
	r := replay(t, svc, j.ID, 0, 0, "owner")
	fl.phases = map[string]phaseInfo{r.Session: {phase: "Running"}} // the pod idles after bp-replay
	if err := svc.Reconcile(context.Background(), r.Started.Add(ReplayTimeout-time.Second), time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Store.RunByID(r.ID); got.State != store.RunRunning {
		t.Fatalf("ended early: %+v", got)
	}
	if err := svc.Reconcile(context.Background(), r.Started.Add(ReplayTimeout+time.Second), time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Store.RunByID(r.ID); got.State != store.RunFailed || got.Outcome != "The replay stopped without a result" {
		t.Fatalf("after the replay timeout: %+v", got)
	}
}
