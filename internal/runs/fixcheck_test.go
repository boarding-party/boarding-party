package runs

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

// linked reports a bug from ann's checkout and saves a bug revision of her
// journey that shows it at step 3. It returns the issue, the journey and the
// bug revision's number.
func linked(t *testing.T, svc *Service) (string, store.Journey, int) {
	t.Helper()
	id := report(t, svc, "r1", "ann", "checkout", "", at)
	recorded(t, svc, 1)
	j, rev, err := svc.Store.AddRevision("ann", "checkout", store.Revision{Kind: store.RevisionBug, Code: journeyCode, Reason: "bug",
		RunID: "r1", BugID: "b-r1", BugStep: 3, Created: at}, store.Decision{Kind: "bug revision recorded", Actor: store.ActorModel, Created: at})
	if err != nil {
		t.Fatal(err)
	}
	return id, j, rev.N
}

// fixCheck marks issue id fixed and starts the queued check, as the
// scheduler does.
func fixCheck(t *testing.T, svc *Service, id string) store.Run {
	t.Helper()
	if err := svc.SetIssueStatus(id, store.IssueFixed, "", at); err != nil {
		t.Fatal(err)
	}
	q, _ := svc.Store.Queue()
	if len(q) != 1 {
		t.Fatalf("queue %+v", q)
	}
	r, err := svc.StartQueued(context.Background(), q[0])
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func ctxOf(t *testing.T, raw []byte) store.RunContext {
	t.Helper()
	var c store.RunContext
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func issueNow(t *testing.T, svc *Service, id string) store.Issue {
	t.Helper()
	is, err := svc.Store.Issue(id)
	if err != nil {
		t.Fatal(err)
	}
	return is
}

func TestMarkFixedQueuesReplayForLinkedReport(t *testing.T) {
	svc, _ := newSvc(t)
	id, j, n := linked(t, svc)
	if err := svc.SetIssueStatus(id, store.IssueFixed, "", at); err != nil {
		t.Fatal(err)
	}
	q, _ := svc.Store.Queue()
	if len(q) != 1 || q[0].Kind != store.RunReplay || q[0].JourneyID != j.ID || q[0].Revision != n || q[0].Seed < 1 || q[0].Seed > MaxSeed ||
		q[0].Chain != q[0].ID || q[0].Persona != "ann" || q[0].Story != "checkout" || q[0].Role != "buyer" {
		t.Fatalf("queue %+v", q)
	}
	c := ctxOf(t, q[0].Context)
	if c.Kind != "verify" || c.Verify == nil || c.Verify.IssueID != id || c.Verify.JourneyID != j.ID || c.Verify.Revision != n ||
		c.Verify.JourneyStep != 3 || c.Verify.Summary != "total wrong" {
		t.Fatalf("context %+v", c.Verify)
	}
	if is := issueNow(t, svc, id); is.Status != store.IssueVerifying || is.CheckChain != q[0].Chain {
		t.Fatalf("issue %+v", is)
	}
}

func TestMarkFixedPrefersLinkedReport(t *testing.T) {
	svc, _ := newSvc(t)
	id, j, _ := linked(t, svc)
	report(t, svc, "r2", "ann", "checkout", id, at.Add(time.Hour)) // newer, not linked
	if err := svc.SetIssueStatus(id, store.IssueFixed, "", at); err != nil {
		t.Fatal(err)
	}
	if q, _ := svc.Store.Queue(); len(q) != 1 || q[0].Kind != store.RunReplay || q[0].JourneyID != j.ID {
		t.Fatalf("queue %+v", q)
	}
}

func TestMarkFixedFallsBackToAIWhenJourneyPaused(t *testing.T) {
	svc, _ := newSvc(t)
	id, j, _ := linked(t, svc)
	if err := svc.Store.SetJourneyState(j.ID, store.JourneyPaused, store.Decision{Kind: "paused", Actor: "kate", Created: at}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetIssueStatus(id, store.IssueFixed, "", at); err != nil {
		t.Fatal(err)
	}
	q, _ := svc.Store.Queue()
	if len(q) != 1 || q[0].Kind == store.RunReplay || q[0].Persona != "ann" || !strings.HasPrefix(q[0].Reason, "Checks the fix: ") {
		t.Fatalf("queue %+v", q)
	}
	if c := ctxOf(t, q[0].Context); c.Verify == nil || c.Verify.JourneyStep != 0 {
		t.Fatalf("an AI check carries a journey step: %+v", c.Verify)
	}
}

// fixCheckRow checks journey j's one fix-check decision, which a settled fix check wrote.
func fixCheckRow(t *testing.T, svc *Service, j store.Journey, runID, reason string) {
	t.Helper()
	ds, _ := svc.Store.Decisions(j.ID)
	var got []store.Decision
	for _, d := range ds {
		if d.Kind == "fix check" {
			got = append(got, d)
		}
	}
	if len(got) != 1 || got[0].Actor != store.ActorDirector || got[0].RunID != runID || got[0].Reason != reason {
		t.Fatalf("fix-check decisions %+v", got)
	}
}

func TestReplayFixCheckStillThere(t *testing.T) {
	svc, _ := newSvc(t)
	id, j, _ := linked(t, svc)
	r := fixCheck(t, svc, id)
	if err := svc.FinishReplay(r.Token, ReplayResult{Status: "failed", Step: 3, StepName: "Pay", Error: "expected £540", Seed: r.Seed}, at); err != nil {
		t.Fatal(err)
	}
	is := issueNow(t, svc, id)
	if is.Status != store.IssueOpen || is.CheckNote != "Still there: The replay failed at step 3 again: Pay" || is.Reopened.IsZero() {
		t.Fatalf("issue %+v", is)
	}
	fixCheckRow(t, svc, j, r.ID, "Issue "+id+": Still there")
	if jj, _ := svc.Store.Journey(j.ID); jj.Passed+jj.Failed != 0 {
		t.Fatalf("a fix-check replay counted on the journey's record: %+v", jj)
	}
	if q, _ := svc.Store.Queue(); len(q) != 0 {
		t.Fatalf("queue %+v", q)
	}
}

func TestReplayFixCheckGone(t *testing.T) {
	for name, res := range map[string]ReplayResult{
		"passed":         {Status: "passed"},
		"failed after N": {Status: "failed", Step: 5, StepName: "Download", Error: "timeout"},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := newSvc(t)
			id, j, _ := linked(t, svc)
			r := fixCheck(t, svc, id)
			res.Seed = r.Seed
			if err := svc.FinishReplay(r.Token, res, at); err != nil {
				t.Fatal(err)
			}
			if is := issueNow(t, svc, id); is.Status != store.IssueClosed || is.CheckNote != "Gone: The replay got past step 3." {
				t.Fatalf("issue %+v", is)
			}
			fixCheckRow(t, svc, j, r.ID, "Issue "+id+": Gone")
		})
	}
}

func TestReplayFixCheckHandsOver(t *testing.T) {
	svc, _ := newSvc(t)
	id, j, n := linked(t, svc)
	r := fixCheck(t, svc, id)
	if err := svc.FinishReplay(r.Token, ReplayResult{Status: "failed", Step: 2, StepName: "Fill", Error: strings.Repeat("e", 400), Seed: r.Seed}, at); err != nil {
		t.Fatal(err)
	}
	if is := issueNow(t, svc, id); is.Status != store.IssueVerifying || is.CheckChain != r.Chain {
		t.Fatalf("issue %+v", is)
	}
	q, _ := svc.Store.Queue()
	if len(q) != 1 || q[0].Kind == store.RunReplay || q[0].Chain != r.Chain || q[0].Follows != r.ID || q[0].Persona != "ann" ||
		q[0].Story != "checkout" || q[0].Role != "buyer" {
		t.Fatalf("queue %+v", q)
	}
	c := ctxOf(t, q[0].Context)
	want := "A replay of revision " + strconv.Itoa(n) + " stopped at step 2 (Fill) before step 3: "
	if c.Kind != "verify" || c.Verify == nil || c.Verify.IssueID != id || !strings.HasPrefix(c.Verify.ReplayNote, want) ||
		len(c.Verify.ReplayNote) > len(want)+300 || c.Verify.JourneyStep != 0 || c.Verify.Summary != "total wrong" {
		t.Fatalf("context %+v", c.Verify)
	}
	fixCheckRow(t, svc, j, r.ID, "Issue "+id+": handed to an AI run")
	// The AI run settles the check in the same chain.
	ai, err := svc.StartQueued(context.Background(), q[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishChecked(context.Background(), ai.Token, store.RunDone, "done", &store.Verdict{Reached: true, Note: "total is right"}); err != nil {
		t.Fatal(err)
	}
	if is := issueNow(t, svc, id); is.Status != store.IssueClosed || is.CheckNote != "Gone: total is right" {
		t.Fatalf("issue after the AI run %+v", is)
	}
}

func TestFixCheckReplayWithoutResultHandsOver(t *testing.T) {
	svc, fl := newSvc(t)
	id, j, n := linked(t, svc)
	r := fixCheck(t, svc, id)
	fl.phases = map[string]phaseInfo{r.Session: {phase: "Failed", msg: "pod died"}}
	if err := svc.Reconcile(context.Background(), time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
	q, _ := svc.Store.Queue()
	if len(q) != 1 || q[0].Kind == store.RunReplay || q[0].Chain != r.Chain {
		t.Fatalf("queue %+v", q)
	}
	if c := ctxOf(t, q[0].Context); c.Verify == nil || c.Verify.ReplayNote != "A replay of revision "+strconv.Itoa(n)+" stopped without a result." {
		t.Fatalf("context %+v", c.Verify)
	}
	if is := issueNow(t, svc, id); is.Status != store.IssueVerifying {
		t.Fatalf("issue %+v", is)
	}
	fixCheckRow(t, svc, j, r.ID, "Issue "+id+": handed to an AI run")
	if jj, _ := svc.Store.Journey(j.ID); jj.Failed != 0 {
		t.Fatalf("counted: %+v", jj)
	}
}

func TestFixCheckReplayContinuationSettles(t *testing.T) {
	svc, _ := newSvc(t)
	id, _, _ := linked(t, svc)
	r := fixCheck(t, svc, id)
	res := ReplayResult{Status: "waiting", Step: 1, Seed: r.Seed, Wait: &ReplayWait{N: 1, Until: json.RawMessage(`{"time":{"after":"1h"}}`)}}
	w := store.Wait{Kind: "time", At: at.Add(time.Hour), Deadline: at.Add(time.Hour)}
	if err := svc.FinishReplayWaiting(r.Token, res, w, "Waiting", at); err != nil {
		t.Fatal(err)
	}
	if is := issueNow(t, svc, id); is.Status != store.IssueVerifying {
		t.Fatalf("a waiting replay settled the check: %+v", is)
	}
	// The continuation as the scheduler queues it: the same chain, with the verify context.
	was := ctxOf(t, r.Context)
	ctx, _ := json.Marshal(store.RunContext{Kind: "continue", PreviousRun: r.ID, Verify: was.Verify, Resume: &store.ContextResume{After: 1}})
	next := store.Queued{ID: "cont", Persona: r.Persona, Role: r.Role, Story: r.Story, Chain: r.Chain, Follows: r.ID, Via: "continue",
		Context: ctx, Created: at, Kind: store.RunReplay, JourneyID: r.JourneyID, Revision: r.Revision, Seed: r.Seed}
	if err := svc.Store.Enqueue(next); err != nil {
		t.Fatal(err)
	}
	r2, err := svc.StartQueued(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishReplay(r2.Token, ReplayResult{Status: "failed", Step: 3, StepName: "Pay", Seed: r.Seed}, at); err != nil {
		t.Fatal(err)
	}
	if is := issueNow(t, svc, id); is.Status != store.IssueOpen {
		t.Fatalf("issue %+v", is)
	}
}

func TestDroppedFixCheckReplaySettles(t *testing.T) {
	for _, how := range []string{"taken off the queue", "journey deleted"} {
		t.Run(how, func(t *testing.T) {
			svc, _ := newSvc(t)
			id, j, _ := linked(t, svc)
			if err := svc.SetIssueStatus(id, store.IssueFixed, "", at); err != nil {
				t.Fatal(err)
			}
			q, _ := svc.Store.Queue()
			var err error
			if how == "journey deleted" {
				err = svc.Store.DeleteJourney(j.ID, store.Decision{Kind: "deleted", Actor: "kate", Created: at})
			} else {
				err = svc.Store.DeleteQueued(q[0].ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if is := issueNow(t, svc, id); is.Status != store.IssueFixed || is.CheckNote != "Could not check: the check was taken off the queue." {
				t.Fatalf("issue %+v", is)
			}
		})
	}
}

func TestFixCheckReplayPastStepThenWaitIsGone(t *testing.T) {
	svc, _ := newSvc(t)
	id, j, _ := linked(t, svc)
	r := fixCheck(t, svc, id)
	res := ReplayResult{Status: "waiting", Step: 4, Seed: r.Seed, Wait: &ReplayWait{N: 1, Until: json.RawMessage(`{"time":{"after":"1d"}}`)}}
	w := store.Wait{Kind: "time", At: at.Add(24 * time.Hour), Deadline: at.Add(24 * time.Hour)}
	if err := svc.FinishReplayWaiting(r.Token, res, w, "Waiting", at); err != nil {
		t.Fatal(err)
	}
	if is := issueNow(t, svc, id); is.Status != store.IssueClosed || is.CheckNote != "Gone: The replay got past step 3." {
		t.Fatalf("issue %+v", is)
	}
	if got, _ := svc.Store.RunByID(r.ID); got.State != store.RunDone {
		t.Fatalf("run %+v", got)
	}
	if ow, err := svc.Store.WaitFor(r.ID); err == nil && ow.Status == store.WaitOpen {
		t.Fatalf("parked anyway: %+v", ow)
	}
	fixCheckRow(t, svc, j, r.ID, "Issue "+id+": Gone")
}
