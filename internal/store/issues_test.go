package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// withRun creates a finished run of persona doing story.
func withRun(t *testing.T, s *Store, id, persona, story string) {
	t.Helper()
	if err := s.CreateRun(Run{ID: id, Token: "tok-" + id, Persona: persona, Role: "visitor", Story: story,
		State: RunDone, Started: t0, Journey: id}); err != nil {
		t.Fatal(err)
	}
}

func addBug(t *testing.T, s *Store, b Bug) {
	t.Helper()
	if err := s.AddBug(b); err != nil {
		t.Fatal(err)
	}
}

func onlyIssue(t *testing.T, s *Store) Issue {
	t.Helper()
	is, err := s.Issues()
	if err != nil || len(is) != 1 {
		t.Fatalf("issues %+v %v", is, err)
	}
	return is[0]
}

func TestReportOpensIssue(t *testing.T) {
	s := open(t)
	withRun(t, s, "r1", "Ada", "todos")
	addBug(t, s, Bug{ID: "b1", RunID: "r1", Summary: "counter wrong", Severity: "high", Created: t0})
	is := onlyIssue(t, s)
	if is.Status != IssueOpen || is.Summary != "counter wrong" || is.Reports != 1 ||
		len(is.Personas) != 1 || is.Personas[0] != "Ada" || !is.LastSeen.Equal(t0) {
		t.Fatalf("issue %+v", is)
	}
	bugs, _ := s.Bugs("r1")
	if bugs[0].IssueID != is.ID {
		t.Fatalf("bug issue %q, want %q", bugs[0].IssueID, is.ID)
	}
}

func TestReportAttaches(t *testing.T) {
	s := open(t)
	withRun(t, s, "r1", "Ada", "todos")
	withRun(t, s, "r2", "Bob", "todos")
	addBug(t, s, Bug{ID: "b1", RunID: "r1", Summary: "x", Severity: "low", Created: t0})
	first := onlyIssue(t, s)
	later := t0.Add(time.Hour)
	addBug(t, s, Bug{ID: "b2", RunID: "r2", Summary: "x again", Severity: "low", Created: later, IssueID: first.ID})
	is := onlyIssue(t, s)
	if is.Reports != 2 || !is.Updated.Equal(later) || !is.LastSeen.Equal(later) || len(is.Personas) != 2 || is.Summary != "x" {
		t.Fatalf("issue %+v", is)
	}
}

func TestReportUnknownIssueOpensNew(t *testing.T) {
	s := open(t)
	withRun(t, s, "r1", "Ada", "todos")
	addBug(t, s, Bug{ID: "b1", RunID: "r1", Summary: "x", Created: t0})
	addBug(t, s, Bug{ID: "b2", RunID: "r1", Summary: "y", Created: t0, IssueID: "nope"})
	if is, _ := s.Issues(); len(is) != 2 {
		t.Fatalf("issues %+v", is)
	}
}

func TestReportReopensClosed(t *testing.T) {
	s := open(t)
	withRun(t, s, "r1", "Ada", "todos")
	addBug(t, s, Bug{ID: "b1", RunID: "r1", Summary: "x", Created: t0})
	id := onlyIssue(t, s).ID
	if err := s.SetIssueStatus(id, IssueClosed, "", t0); err != nil {
		t.Fatal(err)
	}
	at := t0.Add(time.Hour)
	addBug(t, s, Bug{ID: "b2", RunID: "r1", Summary: "x", Created: at, IssueID: id})
	if is := onlyIssue(t, s); is.Status != IssueOpen || !is.Reopened.Equal(at) {
		t.Fatalf("issue %+v", is)
	}
	for i, st := range []IssueStatus{IssueNotABug, IssueVerifying} {
		if _, err := s.db.Exec(`UPDATE issues SET status=?`, string(st)); err != nil {
			t.Fatal(err)
		}
		addBug(t, s, Bug{ID: "b3" + string(rune('a'+i)), RunID: "r1", Summary: "x", Created: at, IssueID: id})
		if is := onlyIssue(t, s); is.Status != st {
			t.Fatalf("after attach to %s: %+v", st, is)
		}
	}
}

func TestSetIssueStatusReason(t *testing.T) {
	s := open(t)
	withRun(t, s, "r1", "Ada", "todos")
	addBug(t, s, Bug{ID: "b1", RunID: "r1", Summary: "x", Created: t0})
	id := onlyIssue(t, s).ID
	if err := s.SetIssueStatus(id, IssueNotABug, "site default", t0); err != nil {
		t.Fatal(err)
	}
	if is, _ := s.Issue(id); is.Reason != "site default" || is.Status != IssueNotABug {
		t.Fatalf("issue %+v", is)
	}
	if err := s.SetIssueStatus(id, IssueConfirmed, "ignored", t0); err != nil {
		t.Fatal(err)
	}
	if is, _ := s.Issue(id); is.Reason != "" {
		t.Fatalf("reason kept: %+v", is)
	}
	if err := s.SetIssueStatus("nope", IssueConfirmed, "", t0); err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestStuckFindingOpensIssue(t *testing.T) {
	s := open(t)
	withRun(t, s, "r1", "Ada", "todos")
	if _, err := s.db.Exec(`UPDATE runs SET state='running' WHERE id='r1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishRunWaiting("r1", "waiting", t0, Wait{Kind: "time", Deadline: t0.Add(time.Hour), At: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveWait("r1", Resolution{Status: WaitStuck, Detail: "late", Ended: t0,
		Finding: &Bug{ID: "f1", RunID: "r1", Summary: "late", Severity: "medium", Created: t0}}); err != nil {
		t.Fatal(err)
	}
	if is := onlyIssue(t, s); is.Summary != "late" {
		t.Fatalf("issue %+v", is)
	}
}

func TestKnownIssuesOrder(t *testing.T) {
	s := open(t)
	withRun(t, s, "r1", "Ada", "todos")
	mk := func(bug string, at time.Time, st IssueStatus) string {
		addBug(t, s, Bug{ID: bug, RunID: "r1", Summary: bug, Created: at})
		bugs, _ := s.Bugs("r1")
		id := ""
		for _, b := range bugs {
			if b.ID == bug {
				id = b.IssueID
			}
		}
		if err := s.SetIssueStatus(id, st, "why", at); err != nil {
			t.Fatal(err)
		}
		return id
	}
	now := t0
	nab := mk("nab", now.Add(-50*24*time.Hour), IssueNotABug)
	opn := mk("open", now.Add(-time.Hour), IssueOpen)
	mk("oldclosed", now.Add(-40*24*time.Hour), IssueClosed)
	cl := mk("newclosed", now.Add(-2*24*time.Hour), IssueClosed)
	got, err := s.KnownIssues(now, 100)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, i := range got {
		ids = append(ids, i.ID)
	}
	if len(ids) != 3 || ids[0] != nab || ids[1] != opn || ids[2] != cl {
		t.Fatalf("order %v, want %v", ids, []string{nab, opn, cl})
	}
	if got[0].Reason != "why" {
		t.Fatalf("reason %+v", got[0])
	}
	got, _ = s.KnownIssues(now, 2)
	if len(got) != 2 || got[0].ID != nab || got[1].ID != opn {
		t.Fatalf("limited %+v", got)
	}
}

func TestBugsBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bp.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE bugs (id TEXT PRIMARY KEY, run_id TEXT NOT NULL, summary TEXT NOT NULL DEFAULT '',
		step TEXT NOT NULL DEFAULT '', evidence TEXT NOT NULL DEFAULT '', severity TEXT NOT NULL DEFAULT '',
		note TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL DEFAULT 0);
		INSERT INTO bugs(id, run_id, summary, created) VALUES('b1', 'r1', 'old bug', 1000)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	is, err := s.Issue("b1")
	if err != nil || is.Status != IssueOpen || is.Summary != "old bug" {
		t.Fatalf("issue %+v %v", is, err)
	}
	bugs, _ := s.Bugs("r1")
	if len(bugs) != 1 || bugs[0].IssueID != "b1" {
		t.Fatalf("bugs %+v", bugs)
	}
}
