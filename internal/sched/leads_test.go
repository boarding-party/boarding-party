package sched

import (
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// leadYAML has lead Kate (wakes at wake) and Tom in an active role, and a
// second role whose first persona is a lead.
func leadYAML(wake string) string {
	return `name: p
sites: ["https://x.test"]
timezone: Europe/London
roles:
  - name: owner
    activity: every 1 h
    personas:
      - {name: Kate, lead: {brief: find bugs, createPersonas: true, wakeAt: "` + wake + `"}}
      - {name: Tom}
    stories:
      - {name: invoice, goal: make an invoice, handoffs: [{role: clerk, story: file}]}
  - name: clerk
    personas:
      - {name: Lee, lead: {brief: b, seeRuns: true}}
      - {name: Sam}
    stories:
      - {name: file, goal: file it}
`
}

func leadFix(t *testing.T, wake string) *fix {
	t.Helper()
	f := newSchedFix(t, leadYAML(wake))
	if err := f.st.AddChild(store.Child{Name: "Ana", Lead: "Kate", Persona: project.Persona{Name: "Ana"},
		Stories: []project.Story{{Name: "pay", Goal: "pay"}}, Created: now}); err != nil {
		t.Fatal(err)
	}
	return f
}

// leadRuns returns the queued lead runs, by persona.
func (f *fix) leadRuns() map[string]store.Queued {
	out := map[string]store.Queued{}
	for _, q := range f.queue() {
		if q.Story == project.LeadStory {
			out[q.Persona] = q
		}
	}
	return out
}

// pastLeadRuns records a finished lead run of each lead at t.
func (f *fix) pastLeadRun(id, lead, role string, t time.Time) {
	f.t.Helper()
	if err := f.st.CreateRun(store.Run{ID: id, Token: "tok-" + id, Persona: lead, Role: role, Story: project.LeadStory,
		State: store.RunRunning, Started: t, Chain: id}); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.st.FinishRun(id, store.RunDone, "ok", t.Add(time.Minute)); err != nil {
		f.t.Fatal(err)
	}
}

func TestScheduleSkipsLeads(t *testing.T) {
	f := leadFix(t, "23:00")
	f.fire()
	f.clk.Add(2 * time.Hour)
	f.fire()
	for _, q := range f.queue() {
		if q.Persona == "Kate" && q.Story != project.LeadStory {
			t.Fatalf("lead got a browser run: %+v", q)
		}
	}
	if _, err := f.st.Dues(); err != nil {
		t.Fatal(err)
	}
	if d, _ := f.st.Dues(); hasDue(d, "Kate") {
		t.Fatal("lead has a schedule due")
	}
}

func hasDue(ds map[string]store.Due, name string) bool { _, ok := ds[name]; return ok }

func TestHandoffSkipsLead(t *testing.T) {
	f := leadFix(t, "23:00")
	f.done("r1", "Tom", "owner", "invoice", "r1", "made it")
	f.tick()
	var got string
	for _, q := range append(f.queue(), f.starter.Calls()...) {
		if q.Story == "file" {
			got = q.Persona
		}
	}
	if got != "Sam" {
		t.Fatalf("handoff went to %q", got)
	}
}

func TestWakeFirst(t *testing.T) {
	f := leadFix(t, "23:00")
	if err := f.s.wakeLeads(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	q, ok := f.leadRuns()["Kate"]
	if !ok || q.Role != "owner" || q.Reason != "Lead: First wake" {
		t.Fatalf("lead runs %+v", f.leadRuns())
	}
	if _, ok := f.leadRuns()["Lee"]; !ok {
		t.Fatal("Lee did not wake")
	}
}

func TestWakeAfterChildren(t *testing.T) {
	f := leadFix(t, "23:00")
	f.pastLeadRun("l1", "Kate", "owner", now.Add(-time.Hour))
	f.pastLeadRun("l2", "Lee", "clerk", now.Add(-time.Hour))
	f.done("c1", "Ana", "Ana", "pay", "c1", "paid")
	if err := f.s.wakeLeads(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	lr := f.leadRuns()
	if q, ok := lr["Kate"]; !ok || q.Reason != "Lead: Children finished" {
		t.Fatalf("Kate %+v", lr)
	}
	if _, ok := lr["Lee"]; ok {
		t.Fatal("Lee has no children and woke")
	}
}

func TestNoWakeWhileChildQueued(t *testing.T) {
	f := leadFix(t, "23:00")
	if err := f.st.Enqueue(store.Queued{ID: "q1", Persona: "Ana", Role: "Ana", Story: "pay", Created: now}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.wakeLeads(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.leadRuns()["Kate"]; ok {
		t.Fatal("woke while a child is queued")
	}
}

func TestWakeDaily(t *testing.T) {
	f := leadFix(t, "11:55")
	f.pastLeadRun("l1", "Kate", "owner", now.Add(-24*time.Hour))
	if err := f.s.wakeLeads(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if q, ok := f.leadRuns()["Kate"]; !ok || q.Reason != "Lead: Daily wake" {
		t.Fatalf("not woken daily: %+v", f.leadRuns())
	}
	g := leadFix(t, "12:05")
	g.pastLeadRun("l1", "Kate", "owner", now.Add(-24*time.Hour))
	if err := g.s.wakeLeads(g.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.leadRuns()["Kate"]; ok {
		t.Fatal("woke before its time")
	}
}

func TestNoWakeLoop(t *testing.T) {
	f := leadFix(t, "11:55")
	f.pastLeadRun("l1", "Kate", "owner", now.Add(-time.Minute))
	f.pastLeadRun("l2", "Lee", "clerk", now.Add(-time.Minute))
	for _, d := range []time.Duration{30 * time.Minute, 3 * time.Hour} {
		f.clk.Add(d)
		if err := f.s.wakeLeads(f.clk.Now()); err != nil {
			t.Fatal(err)
		}
		if lr := f.leadRuns(); len(lr) != 0 {
			t.Fatalf("woke again: %+v", lr)
		}
	}
}

func TestNoWakeWhenGated(t *testing.T) {
	f := leadFix(t, "23:00")
	f.starter.SetGate(runs.ErrPaused)
	if err := f.s.wakeLeads(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if lr := f.leadRuns(); len(lr) != 0 {
		t.Fatalf("woke while paused: %+v", lr)
	}
}

func TestNoWakeOverLeadBudget(t *testing.T) {
	f := leadFix(t, "23:00")
	f.starter.leadGate = runs.ErrLeadOverBudget
	if err := f.s.wakeLeads(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if lr := f.leadRuns(); len(lr) != 0 {
		t.Fatalf("woke over budget: %+v", lr)
	}
}
