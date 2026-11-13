package sched

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// pathLauncher is a launcher with no cluster: it records started runs and
// keeps no sessions, so the next run can start as soon as one ends.
type pathLauncher struct {
	mu      sync.Mutex
	started []store.Run
}

func (l *pathLauncher) Start(_ context.Context, r store.Run, _ project.Project) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.started = append(l.started, r)
	return "s-" + r.ID, nil
}
func (l *pathLauncher) Stop(context.Context, string) error                  { return nil }
func (l *pathLauncher) SessionName(id string) string                        { return "s-" + id }
func (l *pathLauncher) Sessions(context.Context) (map[string]string, error) { return nil, nil }
func (l *pathLauncher) LivePods(context.Context) (int, error)               { return 0, nil }
func (l *pathLauncher) Phase(context.Context, string) (string, bool, string, error) {
	return "Running", true, "", nil
}
func (l *pathLauncher) Stage(context.Context, string) (string, string, error) {
	return "running", "", nil
}

func (l *pathLauncher) last() store.Run {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.started[len(l.started)-1]
}

// The whole path with the real runs service: a scheduled replay fails, its
// same-seed check fails too, the copilot run starts with the failure in its
// context, decides "bug", and the next failure at that step calls no one.
func TestCopilotPathEndToEnd(t *testing.T) {
	st := openStore(t)
	p, err := project.Load([]byte(exploreYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutProject(p); err != nil {
		t.Fatal(err)
	}
	l := &pathLauncher{}
	svc := &runs.Service{Store: st, Launcher: l, Artifacts: artifacts.NewFS(t.TempDir(), 1<<20)}
	clk := &clock{t: time.Now().UTC()}
	s := &Scheduler{Store: st, Runs: svc, Now: clk.Now, Sources: []Source{}}
	j, _, err := st.AddRevision("Ada", "book", store.Revision{Kind: store.RevisionFull, Code: "export default async function journey() {}", Reason: "r", Created: clk.Now()},
		store.Decision{Kind: "recorded", Actor: store.ActorModel, Created: clk.Now(), Explored: true})
	if err != nil {
		t.Fatal(err)
	}
	tick := func() {
		t.Helper()
		if err := s.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	failAt3 := runs.ReplayResult{Status: "failed", Step: 3, StepName: "Pay", Error: "no Pay button"}

	// A scheduled replay, queued as the scheduler queues it, fails.
	q, err := svc.EnqueueReplay(j.ID, 0, 0, store.ActorDirector, clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	tick()
	first := l.last()
	if first.Kind != store.RunReplay || first.Seed != q.Seed {
		t.Fatalf("first run %+v", first)
	}
	failAt3.Seed = first.Seed
	if err := svc.FinishReplay(first.Token, failAt3, clk.Now()); err != nil {
		t.Fatal(err)
	}

	// The check replays the same revision and seed; it fails too.
	tick()
	check := l.last()
	if check.Via != "flake check" || check.Seed != first.Seed || check.Revision != first.Revision || check.Follows != first.ID {
		t.Fatalf("check %+v", check)
	}
	if err := svc.FinishReplay(check.Token, failAt3, clk.Now()); err != nil {
		t.Fatal(err)
	}

	// The copilot starts: an AI run with the failure in its context.
	tick()
	cp := l.last()
	if cp.Kind != "" || cp.Via != "copilot" || !runs.IsCopilot(cp) {
		t.Fatalf("copilot %+v", cp)
	}
	if got, _ := st.Journey(j.ID); got.State != store.JourneyRepairing {
		t.Fatalf("state %s", got.State)
	}
	if err := st.AddBug(store.Bug{ID: "b1", RunID: cp.ID, Summary: "Pay button gone", Created: clk.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecideJourney(cp.Token, runs.Decide{Kind: "bug", Reason: "the Pay button is gone", BugID: "b1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Finish(context.Background(), cp.Token, store.RunDone, "reported the bug"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Journey(j.ID)
	if got.State != store.JourneyLive || got.BlockedStep != 3 || got.BlockedBug != "b1" {
		t.Fatalf("after the decision %+v", got)
	}

	// The next replay fails at the same step: a row, nobody called.
	clk.Add(time.Hour)
	if _, err := svc.EnqueueReplay(j.ID, 0, 0, store.ActorDirector, clk.Now()); err != nil {
		t.Fatal(err)
	}
	tick()
	again := l.last()
	if err := svc.FinishReplay(again.Token, failAt3, clk.Now()); err != nil {
		t.Fatal(err)
	}
	n := len(l.started)
	tick()
	tick()
	if len(l.started) != n {
		t.Fatalf("a run started after a known-bug failure: %+v", l.last())
	}
	ds, _ := st.Decisions(j.ID)
	if ds[0].Kind != "still failing at a known bug" {
		t.Fatalf("decision %+v", ds[0])
	}
}
