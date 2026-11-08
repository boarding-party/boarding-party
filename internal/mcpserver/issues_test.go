package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

// issueFrom stores a report from run and returns its issue id.
func issueFrom(t *testing.T, st *store.Store, runID, bug, summary string) string {
	t.Helper()
	if err := st.AddBug(store.Bug{ID: bug, RunID: runID, Summary: summary, Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, b := range mustBugs(t, st, runID) {
		if b.ID == bug {
			return b.IssueID
		}
	}
	t.Fatalf("bug %s missing", bug)
	return ""
}

func mustBugs(t *testing.T, st *store.Store, runID string) []store.Bug {
	t.Helper()
	bugs, err := st.Bugs(runID)
	if err != nil {
		t.Fatal(err)
	}
	return bugs
}

func TestNextAssignmentKnownIssues(t *testing.T) {
	_, st, srv, run := setup(t)
	cs := connect(t, srv, run.Token)
	var a struct {
		KnownIssues []map[string]any `json:"knownIssues"`
	}
	text, _ := call(t, cs, "next_assignment", nil)
	if err := json.Unmarshal([]byte(text), &a); err != nil || a.KnownIssues == nil || len(a.KnownIssues) != 0 {
		t.Fatalf("empty store: %s %v", text, err)
	}
	nab := issueFrom(t, st, run.ID, "b1", "invoice number prefilled")
	if err := st.SetIssueStatus(nab, store.IssueNotABug, "site default", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	opn := issueFrom(t, st, run.ID, "b2", "footer unreadable")
	text, _ = call(t, cs, "next_assignment", nil)
	if err := json.Unmarshal([]byte(text), &a); err != nil || len(a.KnownIssues) != 2 {
		t.Fatalf("assignment %s %v", text, err)
	}
	k0, k1 := a.KnownIssues[0], a.KnownIssues[1]
	if k0["id"] != nab || k0["status"] != "not_a_bug" || k0["reason"] != "site default" || k0["summary"] != "invoice number prefilled" ||
		k1["id"] != opn || k1["status"] != "open" {
		t.Fatalf("known %+v", a.KnownIssues)
	}
}

func TestReportBugSameAs(t *testing.T) {
	_, st, srv, run := setup(t)
	cs := connect(t, srv, run.Token)
	id := issueFrom(t, st, run.ID, "b1", "footer unreadable")
	text, isErr := call(t, cs, "report_bug", map[string]any{"summary": "footer tiny", "step": "pdf", "evidence": "bug-1.png", "note": "", "severity_guess": "low", "same_as": id})
	if isErr {
		t.Fatalf("report_bug: %s", text)
	}
	for _, b := range mustBugs(t, st, run.ID) {
		if b.ID == text && b.IssueID != id {
			t.Fatalf("bug %+v not attached to %s", b, id)
		}
	}
	if is, _ := st.Issues(); len(is) != 1 {
		t.Fatalf("issues %+v", is)
	}
}

// checkRun adds an active fix-check run for issue and returns its token.
func checkRun(t *testing.T, st *store.Store, issue string) string {
	t.Helper()
	ok, err := st.StartCheck(issue, store.Queued{ID: "q1", Journey: "q1", Created: time.Now().UTC()}, time.Now().UTC())
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	ctx, _ := json.Marshal(store.RunContext{Kind: "verify", Verify: &store.ContextVerify{IssueID: issue, Summary: "x"}})
	if err := st.CreateRun(store.Run{ID: "v1", Token: "tok-v1", Persona: "ann", Role: "buyer", Story: "checkout",
		State: store.RunRunning, Started: time.Now().UTC(), Journey: "q1", Context: ctx}); err != nil {
		t.Fatal(err)
	}
	return "tok-v1"
}

func TestFinishVerify(t *testing.T) {
	_, st, srv, run := setup(t)
	id := issueFrom(t, st, run.ID, "b1", "total wrong")
	cs := connect(t, srv, checkRun(t, st, id))
	for _, bad := range []map[string]any{
		{"outcome": "done", "summary": "s"},
		{"outcome": "done", "summary": "s", "verify": map[string]any{"still_there": false}},
		{"outcome": "done", "summary": "s", "verify": map[string]any{"reached": true}},
		{"outcome": "stuck", "summary": "s", "verify": map[string]any{"reached": false}},
	} {
		if text, isErr := call(t, cs, "finish_run", bad); !isErr || !strings.Contains(text, "verify") {
			t.Errorf("%v accepted: %q", bad, text)
		}
	}
	if text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "done", "summary": "s",
		"verify": map[string]any{"reached": true, "still_there": false, "note": "total right"}}); isErr {
		t.Fatalf("finish: %s", text)
	}
	if is, _ := st.Issue(id); is.Status != store.IssueClosed || is.CheckNote != "Gone: total right" {
		t.Fatalf("issue %+v", is)
	}
	plain := connect(t, srv, run.Token)
	if text, isErr := call(t, plain, "finish_run", map[string]any{"outcome": "done", "summary": "s",
		"verify": map[string]any{"reached": true, "still_there": false}}); !isErr || text != errNotCheck {
		t.Fatalf("plain run accepted verify: %q", text)
	}
}

func TestKnownIssuesClipped(t *testing.T) {
	_, st, srv, run := setup(t)
	issueFrom(t, st, run.ID, "b1", strings.Repeat("x", 5000))
	text, _ := call(t, connect(t, srv, run.Token), "next_assignment", nil)
	var a struct {
		KnownIssues []knownIssue `json:"knownIssues"`
	}
	if err := json.Unmarshal([]byte(text), &a); err != nil || len(a.KnownIssues) != 1 || len([]rune(a.KnownIssues[0].Summary)) > maxKnownIssueText {
		t.Fatalf("known %d runes, err %v", len([]rune(a.KnownIssues[0].Summary)), err)
	}
}
