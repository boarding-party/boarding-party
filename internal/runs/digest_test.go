package runs

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

// digestRun stores a finished run of ann's checkout.
func digestRun(t *testing.T, svc *Service, id string, state store.RunState, finished time.Time) {
	t.Helper()
	ctx, _ := json.Marshal(store.RunContext{Kind: "x"})
	if err := svc.Store.CreateRun(store.Run{ID: id, Token: "tok-" + id, Persona: "ann", Role: "buyer", Story: "checkout",
		State: state, Started: finished.Add(-time.Minute), Finished: finished, Chain: id, Context: ctx,
		Outcome: "secret outcome text " + id}); err != nil {
		t.Fatal(err)
	}
}

func digestBug(t *testing.T, svc *Service, id, run string, at time.Time) string {
	t.Helper()
	if err := svc.Store.AddBug(store.Bug{ID: id, RunID: run, Summary: "Total wrong " + id, Severity: "high",
		Evidence: "secret evidence " + id, Created: at}); err != nil {
		t.Fatal(err)
	}
	b, err := svc.Store.Report(id)
	if err != nil {
		t.Fatal(err)
	}
	return b.IssueID
}

func TestDigestNewBugs(t *testing.T) {
	svc, _ := newSvc(t)
	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	digestRun(t, svc, "r1", store.RunDone, now.Add(-48*time.Hour))
	digestBug(t, svc, "old", "r1", now.Add(-48*time.Hour))
	id := digestBug(t, svc, "new", "r1", now.Add(-time.Hour))
	d, err := svc.Digest(since, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.NewBugs.Items) != 1 || d.NewBugs.Items[0].ID != id || d.NewBugs.Items[0].Title != "Total wrong new" ||
		d.NewBugs.Items[0].Severity != "high" {
		t.Fatalf("new bugs %+v", d.NewBugs)
	}
	if !d.Since.Equal(since) || !d.Until.Equal(now) {
		t.Fatalf("window %s..%s", d.Since, d.Until)
	}
}

// checked settles a fix check of a new issue with verdict v at at.
func checked(t *testing.T, svc *Service, bug string, v *store.Verdict, at time.Time) string {
	t.Helper()
	digestRun(t, svc, "r-"+bug, store.RunDone, at.Add(-48*time.Hour))
	id := digestBug(t, svc, bug, "r-"+bug, at.Add(-48*time.Hour))
	ctx, _ := json.Marshal(store.RunContext{Kind: "verify", Verify: &store.ContextVerify{IssueID: id, Summary: "x"}})
	if ok, err := svc.Store.StartCheck(id, store.Queued{ID: "q-" + bug, Chain: "q-" + bug, Persona: "ann", Role: "buyer",
		Story: "checkout", Context: ctx, Created: at.Add(-time.Hour)}, at.Add(-time.Hour)); err != nil || !ok {
		t.Fatalf("start check %v %v", ok, err)
	}
	if err := svc.Store.CreateRun(store.Run{ID: "v-" + bug, Token: "tok-v-" + bug, Persona: "ann", Role: "buyer", Story: "checkout",
		State: store.RunRunning, Started: at.Add(-time.Minute), Chain: "q-" + bug, Context: ctx}); err != nil {
		t.Fatal(err)
	}
	if won, err := svc.Store.FinishRunChecked("v-"+bug, store.RunDone, "out", at, v); err != nil || !won {
		t.Fatalf("finish %v %v", won, err)
	}
	return id
}

func TestDigestFixChecks(t *testing.T) {
	svc, _ := newSvc(t)
	now := time.Now().UTC()
	gone := checked(t, svc, "gone", &store.Verdict{Reached: true, Note: "fine now"}, now.Add(-2*time.Hour))
	still := checked(t, svc, "still", &store.Verdict{Reached: true, StillThere: true, Note: "same"}, now.Add(-time.Hour))
	checked(t, svc, "early", &store.Verdict{Reached: true, Note: "fine"}, now.Add(-30*time.Hour))
	d, err := svc.Digest(now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	fc := d.FixChecks.Items
	if len(fc) != 2 || fc[0].IssueID != gone || !fc[0].Passed || fc[1].IssueID != still || fc[1].Passed || fc[1].Title != "Total wrong still" {
		t.Fatalf("fix checks %+v", fc)
	}
}

func TestDigestJourneys(t *testing.T) {
	svc, _ := newSvc(t)
	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	j, err := svc.Store.EnsureJourney("ann", "checkout", now.Add(-72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []store.Decision{
		{Kind: "copilot called", Created: now.Add(-3 * time.Hour)},
		{Kind: "copilot called", Created: now.Add(-2 * time.Hour)},
		{Kind: DecideSiteChanged, Created: now.Add(-time.Hour)},
		{Kind: "replayed", Created: now.Add(-time.Hour)},
		{Kind: "rolled back", Created: now.Add(-30 * time.Hour)},
	} {
		d.JourneyID, d.Actor, d.Reason = j.ID, store.ActorDirector, "why"
		if err := svc.Store.AddDecision(d); err != nil {
			t.Fatal(err)
		}
	}
	d, err := svc.Digest(since, now)
	if err != nil {
		t.Fatal(err)
	}
	b, r := d.Journeys.Broke.Items, d.Journeys.Repaired.Items
	if len(b) != 1 || b[0].ID != j.ID || b[0].Persona != "ann" || b[0].Story != "checkout" {
		t.Fatalf("broke %+v, want the journey once", b)
	}
	if len(r) != 1 || r[0].ID != j.ID {
		t.Fatalf("repaired %+v", r)
	}
}

func TestDigestRuns(t *testing.T) {
	svc, _ := newSvc(t)
	now := time.Now().UTC()
	digestRun(t, svc, "a", store.RunDone, now.Add(-3*time.Hour))
	digestRun(t, svc, "b", store.RunDone, now.Add(-2*time.Hour))
	digestRun(t, svc, "c", store.RunStuck, now.Add(-time.Hour))
	digestRun(t, svc, "old", store.RunFailed, now.Add(-30*time.Hour))
	d, err := svc.Digest(now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	s := d.Runs.Stuck.Items
	if d.Runs.Finished != 3 || len(s) != 1 || s[0].ID != "c" || s[0].State != "stuck" || s[0].Persona != "ann" {
		t.Fatalf("runs %+v", d.Runs)
	}
}

func TestDigestSpendIsYesterday(t *testing.T) {
	svc, _ := newSvc(t)
	p, _ := svc.Store.Project()
	loc := p.Location()
	today := DayStart(time.Now(), loc)
	now := today.Add(8 * time.Hour)
	spendRunning(t, svc, "yesterday", today.Add(-2*time.Hour))
	spendRunning(t, svc, "today", today.Add(time.Hour))
	one, err := svc.SpentSince(today)
	if err != nil || one <= 0 {
		t.Fatalf("today's spend %v %v", one, err)
	}
	d, err := svc.Digest(now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if d.Spent != one || d.Budget != p.Budget() || d.OverBudget != (one >= p.Budget()) {
		t.Fatalf("spent %v of %v over %v, want %v (one run)", d.Spent, d.Budget, d.OverBudget, one)
	}
}

func TestDigestEmptyWindowHasNoNews(t *testing.T) {
	svc, _ := newSvc(t)
	now := time.Now().UTC()
	d, err := svc.Digest(now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if d.News() {
		t.Fatalf("news in an empty window: %+v", d)
	}
	if d.NewBugs.Items == nil || d.FixChecks.Items == nil || d.Journeys.Broke.Items == nil ||
		d.Journeys.Repaired.Items == nil || d.Runs.Stuck.Items == nil {
		t.Fatalf("nil items: %+v", d)
	}
}

func TestDigestCarriesNoBodies(t *testing.T) {
	svc, _ := newSvc(t)
	now := time.Now().UTC()
	digestRun(t, svc, "r1", store.RunStuck, now.Add(-time.Hour))
	digestBug(t, svc, "b1", "r1", now.Add(-time.Hour))
	d, err := svc.Digest(now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("digest carries a body: %s", raw)
	}
	if len(d.NewBugs.Items) != 1 || len(d.Runs.Stuck.Items) != 1 {
		t.Fatalf("digest %s", raw)
	}
}

func TestDigestFixChecksIgnoreLaterEdits(t *testing.T) {
	svc, _ := newSvc(t)
	now := time.Now().UTC()
	// A check passed long ago; a person touched the issue today.
	old := checked(t, svc, "old", &store.Verdict{Reached: true, Note: "fine"}, now.Add(-72*time.Hour))
	if err := svc.Store.SetIssueStatus(old, store.IssueClosed, "", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A check failed long ago; a person closed it; a new report reopened it today.
	again := checked(t, svc, "again", &store.Verdict{Reached: true, StillThere: true, Note: "same"}, now.Add(-72*time.Hour))
	if err := svc.Store.SetIssueStatus(again, store.IssueClosed, "", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AddBug(store.Bug{ID: "again-2", RunID: "r-again", IssueID: again, Summary: "x", Created: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	d, err := svc.Digest(now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.FixChecks.Items) != 0 {
		t.Fatalf("fix checks %+v, want none: no check ran in the window", d.FixChecks.Items)
	}
}
