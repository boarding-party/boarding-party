package mcpserver

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

type leadFix struct {
	st  *store.Store
	srv *httptest.Server
}

// newLeadFix stores a project with lead Kate (lead given), hand-made Tom and
// Kate's child Ana.
func newLeadFix(t *testing.T, lead project.Lead) leadFix {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	big := 100.0
	p := project.Project{Name: "p", Sites: []string{"https://x.test"}, DailyBudget: &big, Roles: []project.Role{{
		Name: "owner",
		Personas: []project.Persona{
			{Name: "Kate", Identity: map[string]string{"email": "megan+kate@x.io"}, Lead: &lead},
			{Name: "Tom", Traits: []string{"careful"}},
		},
		Stories: []project.Story{{Name: "invoice", Goal: "make an invoice"}},
	}}}
	if err := st.PutProject(p); err != nil {
		t.Fatal(err)
	}
	if err := st.AddChild(store.Child{Name: "Ana", Lead: "Kate", Reason: "phones", Persona: project.Persona{Name: "Ana"},
		Stories: []project.Story{{Name: "pay", Goal: "pay an invoice"}}, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	svc := &runs.Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), 1024), Launcher: &fakeLauncher{}}
	srv := httptest.NewServer(Handler(svc, st, director, nil))
	t.Cleanup(srv.Close)
	return leadFix{st: st, srv: srv}
}

// run stores a run in state; its token is "tok-"+id.
func (f leadFix) run(t *testing.T, id, persona, role, story string, state store.RunState) {
	t.Helper()
	at := time.Now().Add(-time.Minute)
	if err := f.st.CreateRun(store.Run{ID: id, Token: "tok-" + id, Persona: persona, Role: role, Story: story,
		State: store.RunRunning, Started: at, Journey: id}); err != nil {
		t.Fatal(err)
	}
	if state != store.RunRunning {
		if _, err := f.st.FinishRun(id, state, "outcome of "+id, at.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
}

func allPowers() project.Lead {
	return project.Lead{SeeRuns: true, CreatePersonas: true, StartRuns: true, TidyBugs: true}
}

func TestLeadAssignment(t *testing.T) {
	f := newLeadFix(t, project.Lead{Brief: "find invoice bugs", CreatePersonas: true, StartRuns: true})
	f.run(t, "c1", "Ana", "Ana", "pay", store.RunDone)
	f.run(t, "t1", "Tom", "owner", "invoice", store.RunDone)
	f.run(t, "l1", "Kate", "owner", project.LeadStory, store.RunRunning)
	cs := connect(t, f.srv, "tok-l1")
	text, isErr := call(t, cs, "next_assignment", nil)
	if isErr {
		t.Fatal(text)
	}
	var a struct {
		Lead struct {
			Name, Brief string
			Powers      []string
		}
		Budget struct {
			DailyBudget float64 `json:"dailyBudget"`
			DailyRuns   int     `json:"dailyRuns"`
			RunsToday   int     `json:"runsToday"`
			MaxChildren int     `json:"maxChildren"`
		}
		Inbox    bool
		Children []struct {
			Name       string
			Reason     string
			RecentRuns []struct{ ID, State string } `json:"recentRuns"`
		}
		Personas   []struct{ Name string }
		RecentRuns []any `json:"recentRuns"`
		Issues     []any
	}
	if err := json.Unmarshal([]byte(text), &a); err != nil {
		t.Fatal(err)
	}
	if a.Lead.Name != "Kate" || a.Lead.Brief != "find invoice bugs" || strings.Join(a.Lead.Powers, ",") != "createPersonas,startRuns" {
		t.Fatalf("lead %+v", a.Lead)
	}
	if a.Budget.DailyBudget != 5 || a.Budget.DailyRuns != 10 || a.Budget.RunsToday != 2 || a.Budget.MaxChildren != 6 || !a.Inbox {
		t.Fatalf("budget %+v inbox %v", a.Budget, a.Inbox)
	}
	if len(a.Children) != 1 || a.Children[0].Name != "Ana" || a.Children[0].Reason != "phones" ||
		len(a.Children[0].RecentRuns) != 1 || a.Children[0].RecentRuns[0].ID != "c1" {
		t.Fatalf("children %+v", a.Children)
	}
	if len(a.Personas) != 1 || a.Personas[0].Name != "Tom" {
		t.Fatalf("hand-made personas %+v", a.Personas)
	}
	if a.RecentRuns != nil || a.Issues != nil {
		t.Fatalf("runs or issues shown without seeRuns: %s", text)
	}
}

func TestLeadToolsNotAllowed(t *testing.T) {
	f := newLeadFix(t, project.Lead{SeeRuns: true})
	args := map[string]any{"name": "Bo", "traits": []string{}, "stories": []map[string]any{{"name": "pay", "goal": "pay"}}, "reason": "r"}
	for _, tok := range []struct{ id, persona, role, story string }{
		{"t1", "Tom", "owner", "invoice"},
		{"c1", "Ana", "Ana", "pay"},
		{"l1", "Kate", "owner", project.LeadStory},
	} {
		f.run(t, tok.id, tok.persona, tok.role, tok.story, store.RunRunning)
		text, isErr := call(t, connect(t, f.srv, "tok-"+tok.id), "child_create", args)
		if !isErr || !strings.Contains(text, "not allowed") {
			t.Fatalf("%s: child_create = %q %v", tok.persona, text, isErr)
		}
		if _, err := f.st.FinishRun(tok.id, store.RunDone, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.st.Child("Bo"); err == nil {
		t.Fatal("child created")
	}
}

func TestChildCreate(t *testing.T) {
	l := allPowers()
	l.MaxChildren = 2
	f := newLeadFix(t, l)
	f.run(t, "l1", "Kate", "owner", project.LeadStory, store.RunRunning)
	cs := connect(t, f.srv, "tok-l1")
	mk := func(name string, profile map[string]any) (string, bool) {
		args := map[string]any{"name": name, "traits": []string{"phone", "hurried"},
			"stories": []map[string]any{{"name": "pay", "goal": "pay an invoice on a phone"}}, "reason": "nobody tried a phone"}
		if profile != nil {
			args["profile"] = profile
		}
		return call(t, cs, "child_create", args)
	}
	if text, isErr := mk("Priya Shah", map[string]any{"device": "iphone-15"}); isErr {
		t.Fatal(text)
	}
	c, err := f.st.Child("Priya Shah")
	if err != nil || c.Lead != "Kate" || c.Persona.Identity["email"] != "megan+priya.shah@x.io" || len(c.Persona.Identity["password"]) != 16 ||
		c.Persona.Profile.Device != "iphone-15" || c.Stories[0].Goal != "pay an invoice on a phone" {
		t.Fatalf("child %+v %v", c, err)
	}
	log, _ := f.st.LeadLog("Kate", 5)
	if len(log) != 1 || log[0].Action != "create" || log[0].Subject != "Priya Shah" || log[0].Reason != "nobody tried a phone" || log[0].RunID != "l1" {
		t.Fatalf("log %+v", log)
	}
	for _, bad := range []struct {
		name    string
		profile map[string]any
		want    string
	}{
		{"Tom", nil, "taken"},
		{"Ana", nil, "taken"},
		{"bad name!", nil, "name"},
		{"Ola", map[string]any{"device": "toaster"}, "toaster"},
		{"Ola", nil, "2 personas"}, // Ana and Priya: the cap
	} {
		text, isErr := mk(bad.name, bad.profile)
		if !isErr || !strings.Contains(text, bad.want) {
			t.Fatalf("%s: %q %v, want %q", bad.name, text, isErr, bad.want)
		}
	}
}

func TestChildChangeAndStop(t *testing.T) {
	f := newLeadFix(t, allPowers())
	f.run(t, "l1", "Kate", "owner", project.LeadStory, store.RunRunning)
	cs := connect(t, f.srv, "tok-l1")
	if text, isErr := call(t, cs, "child_change", map[string]any{"name": "Ana",
		"stories": []map[string]any{{"name": "refund", "goal": "ask for a refund"}}, "reason": "pay works"}); isErr {
		t.Fatal(text)
	}
	if c, _ := f.st.Child("Ana"); c.Stories[0].Name != "refund" {
		t.Fatalf("not changed: %+v", c)
	}
	if text, isErr := call(t, cs, "child_change", map[string]any{"name": "Tom", "traits": []string{"x"}, "reason": "r"}); !isErr {
		t.Fatalf("changed a hand-made persona: %s", text)
	}
	if text, isErr := call(t, cs, "child_stop", map[string]any{"name": "Ana", "reason": "nothing new"}); isErr {
		t.Fatal(text)
	}
	if c, _ := f.st.Child("Ana"); c.Stopped.IsZero() {
		t.Fatal("not stopped")
	}
	if text, isErr := call(t, cs, "child_run", map[string]any{"name": "Ana", "story": "refund", "reason": "r"}); !isErr {
		t.Fatalf("ran a stopped child: %s", text)
	}
}

func TestChildRunQueues(t *testing.T) {
	f := newLeadFix(t, allPowers())
	f.run(t, "l1", "Kate", "owner", project.LeadStory, store.RunRunning)
	cs := connect(t, f.srv, "tok-l1")
	if text, isErr := call(t, cs, "child_run", map[string]any{"name": "Ana", "story": "pay", "reason": "first look"}); isErr {
		t.Fatal(text)
	}
	q, _ := f.st.Queue()
	if len(q) != 1 || q[0].Persona != "Ana" || q[0].Role != "Ana" || q[0].Story != "pay" || q[0].Reason != "Queued by Kate: first look" {
		t.Fatalf("queue %+v", q)
	}
	if text, isErr := call(t, cs, "child_run", map[string]any{"name": "Ana", "story": "pay", "reason": "again"}); !isErr {
		t.Fatalf("queued twice: %s", text)
	}
	if text, isErr := call(t, cs, "child_run", map[string]any{"name": "Ana", "story": "nope", "reason": "r"}); !isErr {
		t.Fatalf("unknown story: %s", text)
	}
	if log, _ := f.st.LeadLog("Kate", 5); len(log) != 1 || log[0].Action != "run" || log[0].Subject != "Ana/pay" {
		t.Fatalf("log %+v", log)
	}
}

func TestRunsListAndRead(t *testing.T) {
	f := newLeadFix(t, project.Lead{CreatePersonas: true})
	f.run(t, "c1", "Ana", "Ana", "pay", store.RunDone)
	f.run(t, "t1", "Tom", "owner", "invoice", store.RunDone)
	if err := f.st.AddBug(store.Bug{ID: "b1", RunID: "c1", Summary: "pay button dead", Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	f.run(t, "l1", "Kate", "owner", project.LeadStory, store.RunRunning)
	cs := connect(t, f.srv, "tok-l1")
	text, isErr := call(t, cs, "runs_list", map[string]any{})
	if isErr || !strings.Contains(text, `"c1"`) || strings.Contains(text, `"t1"`) {
		t.Fatalf("runs_list without seeRuns: %s", text)
	}
	text, isErr = call(t, cs, "run_read", map[string]any{"id": "c1"})
	if isErr || !strings.Contains(text, "pay button dead") {
		t.Fatalf("run_read: %s", text)
	}
	if text, isErr := call(t, cs, "run_read", map[string]any{"id": "t1"}); !isErr || !strings.Contains(text, "not allowed") {
		t.Fatalf("read another persona's run: %s", text)
	}
	if text, isErr := call(t, cs, "issues_list", map[string]any{}); !isErr || !strings.Contains(text, "not allowed") {
		t.Fatalf("issues without seeRuns: %s", text)
	}
}

func TestReportJoin(t *testing.T) {
	f := newLeadFix(t, allPowers())
	f.run(t, "c1", "Ana", "Ana", "pay", store.RunDone)
	a := issueFrom(t, f.st, "c1", "b1", "footer tiny")
	b := issueFrom(t, f.st, "c1", "b2", "footer unreadable")
	f.run(t, "l1", "Kate", "owner", project.LeadStory, store.RunRunning)
	cs := connect(t, f.srv, "tok-l1")
	if text, isErr := call(t, cs, "report_join", map[string]any{"report_id": "b1", "issue_id": b, "reason": "same footer"}); isErr {
		t.Fatal(text)
	}
	if _, err := f.st.Issue(a); err == nil {
		t.Fatal("emptied issue kept")
	}
	if text, isErr := call(t, cs, "report_join", map[string]any{"report_id": "b1", "issue_id": "nope", "reason": "r"}); !isErr {
		t.Fatalf("unknown issue: %s", text)
	}
}

func TestLeadReasonRequired(t *testing.T) {
	f := newLeadFix(t, allPowers())
	f.run(t, "l1", "Kate", "owner", project.LeadStory, store.RunRunning)
	cs := connect(t, f.srv, "tok-l1")
	for _, reason := range []string{"", strings.Repeat("x", 201)} {
		if text, isErr := call(t, cs, "child_stop", map[string]any{"name": "Ana", "reason": reason}); !isErr || !strings.Contains(text, "reason") {
			t.Fatalf("reason %d chars: %s", len(reason), text)
		}
	}
}

func TestLeadNoReportBug(t *testing.T) {
	f := newLeadFix(t, allPowers())
	f.run(t, "l1", "Kate", "owner", project.LeadStory, store.RunRunning)
	text, isErr := call(t, connect(t, f.srv, "tok-l1"), "report_bug",
		map[string]any{"summary": "s", "step": "x", "evidence": "e", "note": "", "severity_guess": "low"})
	if !isErr || !strings.Contains(text, "a lead reports no bugs") {
		t.Fatalf("report_bug on a lead run: %s", text)
	}
}

func TestLeadFinishNoWaiting(t *testing.T) {
	f := newLeadFix(t, allPowers())
	f.run(t, "l1", "Kate", "owner", project.LeadStory, store.RunRunning)
	cs := connect(t, f.srv, "tok-l1")
	text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "waiting", "summary": "s",
		"until": map[string]any{"time": map[string]any{"after": "1h"}}})
	if !isErr || !strings.Contains(text, "lead") {
		t.Fatalf("waiting lead: %s", text)
	}
	if text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "done", "summary": "made Ana"}); isErr {
		t.Fatal(text)
	}
	r, _ := f.st.RunByID("l1")
	if r.State != store.RunDone {
		t.Fatalf("state %s", r.State)
	}
}
