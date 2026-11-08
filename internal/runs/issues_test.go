package runs

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

var at = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// report stores a bug from a finished run of persona doing story and returns its issue id.
func report(t *testing.T, svc *Service, run, persona, story, issue string, when time.Time) string {
	t.Helper()
	if err := svc.Store.CreateRun(store.Run{ID: run, Token: "tok-" + run, Persona: persona, Role: "buyer", Story: story,
		State: store.RunDone, Started: when, Journey: run}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AddBug(store.Bug{ID: "b-" + run, RunID: run, Summary: "total wrong", Step: "basket",
		Evidence: "bug-1.png: says £0", Created: when, IssueID: issue}); err != nil {
		t.Fatal(err)
	}
	bugs, _ := svc.Store.Bugs(run)
	return bugs[0].IssueID
}

func TestMarkFixedQueuesCheck(t *testing.T) {
	svc, _ := newSvc(t)
	id := report(t, svc, "r1", "ann", "checkout", "", at)
	if err := svc.SetIssueStatus(id, store.IssueFixed, "", at); err != nil {
		t.Fatal(err)
	}
	is, _ := svc.Store.Issue(id)
	q, _ := svc.Store.Queue()
	if is.Status != store.IssueVerifying || len(q) != 1 {
		t.Fatalf("issue %+v queue %+v", is, q)
	}
	var c store.RunContext
	if err := json.Unmarshal(q[0].Context, &c); err != nil {
		t.Fatal(err)
	}
	if q[0].Persona != "ann" || q[0].Story != "checkout" || q[0].Role != "buyer" || q[0].Journey != q[0].ID ||
		c.Kind != "verify" || c.Verify == nil || c.Verify.IssueID != id || c.Verify.Summary != "total wrong" ||
		c.Verify.Step != "basket" || c.Verify.Evidence != "bug-1.png: says £0" || !strings.HasPrefix(q[0].Reason, "Checks the fix: ") {
		t.Fatalf("queued %+v context %+v", q[0], c)
	}
}

func TestMarkFixedPicksLatestLivingReport(t *testing.T) {
	svc, _ := newSvc(t)
	id := report(t, svc, "r1", "ann", "checkout", "", at)
	report(t, svc, "r2", "gone", "checkout", id, at.Add(time.Hour))
	if err := svc.SetIssueStatus(id, store.IssueFixed, "", at); err != nil {
		t.Fatal(err)
	}
	if q, _ := svc.Store.Queue(); len(q) != 1 || q[0].Persona != "ann" {
		t.Fatalf("queue %+v", q)
	}
}

func TestMarkFixedNobodyLeft(t *testing.T) {
	svc, _ := newSvc(t)
	id := report(t, svc, "r1", "ann", "removed-story", "", at)
	if err := svc.SetIssueStatus(id, store.IssueFixed, "", at); err != nil {
		t.Fatal(err)
	}
	is, _ := svc.Store.Issue(id)
	q, _ := svc.Store.Queue()
	if is.Status != store.IssueFixed || is.CheckNote != store.NoCheckNobody || len(q) != 0 {
		t.Fatalf("issue %+v queue %+v", is, q)
	}
}

func TestMarkFixedWhileVerifyingNoop(t *testing.T) {
	svc, _ := newSvc(t)
	id := report(t, svc, "r1", "ann", "checkout", "", at)
	for i := 0; i < 2; i++ {
		if err := svc.SetIssueStatus(id, store.IssueFixed, "", at); err != nil {
			t.Fatal(err)
		}
	}
	if q, _ := svc.Store.Queue(); len(q) != 1 {
		t.Fatalf("queue %+v", q)
	}
}

func TestSetIssueStatusRejects(t *testing.T) {
	svc, _ := newSvc(t)
	id := report(t, svc, "r1", "ann", "checkout", "", at)
	for _, st := range []store.IssueStatus{store.IssueVerifying, store.IssueClosed, "bogus"} {
		if err := svc.SetIssueStatus(id, st, "", at); err == nil {
			t.Errorf("%s accepted", st)
		}
	}
}

func TestMarkFixedClipsContext(t *testing.T) {
	svc, _ := newSvc(t)
	if err := svc.Store.CreateRun(store.Run{ID: "r1", Token: "tok-r1", Persona: "ann", Role: "buyer", Story: "checkout",
		State: store.RunDone, Started: at, Journey: "r1"}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("y", 5000)
	if err := svc.Store.AddBug(store.Bug{ID: "b1", RunID: "r1", Summary: long, Step: long, Evidence: long, Created: at}); err != nil {
		t.Fatal(err)
	}
	bugs, _ := svc.Store.Bugs("r1")
	if err := svc.SetIssueStatus(bugs[0].IssueID, store.IssueFixed, "", at); err != nil {
		t.Fatal(err)
	}
	q, _ := svc.Store.Queue()
	var c store.RunContext
	json.Unmarshal(q[0].Context, &c)
	for _, f := range []string{c.Verify.Summary, c.Verify.Step, c.Verify.Evidence} {
		if len([]rune(f)) > maxVerifyText {
			t.Fatalf("field %d runes", len([]rune(f)))
		}
	}
}
