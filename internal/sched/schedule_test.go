package sched

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

const activityYAML = `name: p
sites: ["https://x.test"]
timezone: Europe/London
roles:
  - name: patient
    activity: every 1 h
    personas: [{name: Ada}]
    stories:
      - {name: book, goal: book a slot}
      - {name: cancel, goal: cancel a slot}
`

func half() float64 { return 0.5 }

func newSchedFix(t *testing.T, yml string) *fix {
	t.Helper()
	f := newFix(t, yml)
	f.s.Rand = half
	return f
}

func (f *fix) due(persona string) store.Due {
	f.t.Helper()
	d, err := f.st.Dues()
	if err != nil {
		f.t.Fatal(err)
	}
	return d[persona]
}

// fire runs only the fire phase, so queued items stay queued.
func (f *fix) fire() {
	f.t.Helper()
	if err := f.s.fire(context.Background(), f.clk.Now()); err != nil {
		f.t.Fatal(err)
	}
}

func TestScheduleFirstSightSetsDue(t *testing.T) {
	f := newSchedFix(t, activityYAML)
	f.tick()
	if len(f.queue()) != 0 || len(f.starter.Calls()) != 0 {
		t.Fatal("first sight must not fire")
	}
	if d := f.due("Ada"); !d.At.Equal(now.Add(time.Hour)) || d.Activity != "every 1 h" || d.NextStory != 0 {
		t.Fatalf("due = %+v", d)
	}
}

func TestScheduleQueuesRoundRobin(t *testing.T) {
	f := newSchedFix(t, activityYAML)
	f.fire()
	f.clk.Add(time.Hour)
	f.fire()
	q := f.queue()
	if len(q) != 1 || q[0].Story != "book" || q[0].Reason != "Scheduled: every 1 h" || q[0].Persona != "Ada" || q[0].Role != "patient" {
		t.Fatalf("queue = %+v", q)
	}
	f.tick() // starts it
	if c := f.starter.Calls(); len(c) != 1 || c[0].Story != "book" {
		t.Fatalf("calls = %+v", c)
	}
	if ok, err := f.st.FinishRun("s1", store.RunDone, "ok", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.clk.Add(time.Hour)
	f.fire()
	if q := f.queue(); len(q) != 1 || q[0].Story != "cancel" {
		t.Fatalf("queue = %+v", q)
	}
}

func TestScheduleOverdueQueuesOnce(t *testing.T) {
	f := newSchedFix(t, activityYAML)
	f.fire()
	f.clk.Add(10 * time.Hour)
	f.fire()
	f.fire()
	if q := f.queue(); len(q) != 1 {
		t.Fatalf("queue = %+v", q)
	}
	if d := f.due("Ada"); !d.At.Equal(f.clk.Now().Add(time.Hour)) {
		t.Fatalf("due = %v, now %v", d.At, f.clk.Now())
	}
}

func TestScheduleSkipsBusyPersona(t *testing.T) {
	f := newSchedFix(t, activityYAML)
	f.fire()
	if err := f.st.Enqueue(store.Queued{ID: "manual", Persona: "Ada", Role: "patient", Story: "cancel", Created: now}); err != nil {
		t.Fatal(err)
	}
	f.clk.Add(time.Hour)
	f.fire()
	if q := f.queue(); len(q) != 1 || q[0].ID != "manual" {
		t.Fatalf("queue = %+v", q)
	}
	if d := f.due("Ada"); !d.At.Equal(f.clk.Now().Add(time.Hour)) || d.NextStory != 0 {
		t.Fatalf("due = %+v", d)
	}
}

func TestScheduleSkipsStuckAndWaitingStories(t *testing.T) {
	f := newSchedFix(t, activityYAML)
	f.fire()
	f.running("old", "Ada", "patient", "book", "j0")
	if ok, err := f.st.FinishRun("old", store.RunStuck, "stuck", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.clk.Add(time.Hour)
	f.fire()
	if q := f.queue(); len(q) != 1 || q[0].Story != "cancel" {
		t.Fatalf("queue = %+v", q)
	}
	if err := f.st.DeleteQueued(f.queue()[0].ID); err != nil {
		t.Fatal(err)
	}

	// A wait on cancel alone leaves book eligible.
	g := newSchedFix(t, activityYAML)
	g.fire()
	g.waiting("w0", "Ada", "patient", "cancel", "j0", timeWait(g.clk.Now().Add(24*time.Hour)))
	g.clk.Add(time.Hour)
	g.fire()
	if q := g.queue(); len(q) != 1 || q[0].Story != "book" {
		t.Fatalf("queue = %+v", q)
	}

	// cancel is parked on an open wait of Ada's own: both stories blocked.
	f.waiting("w1", "Ada", "patient", "cancel", "j1", timeWait(f.clk.Now().Add(24*time.Hour)))
	f.clk.Add(time.Hour)
	f.fire()
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("queue = %+v", q)
	}
}

func TestScheduleGatedSkips(t *testing.T) {
	f := newSchedFix(t, activityYAML)
	f.fire()
	f.starter.SetGate(runs.ErrPaused)
	f.clk.Add(2 * time.Hour)
	f.fire()
	if len(f.queue()) != 0 {
		t.Fatalf("queue = %+v", f.queue())
	}
	if d := f.due("Ada"); !d.At.Equal(f.clk.Now().Add(time.Hour)) {
		t.Fatalf("due = %v", d.At)
	}
}

func TestScheduleActivityChangeResets(t *testing.T) {
	f := newSchedFix(t, activityYAML)
	f.fire()
	p, _ := f.st.Project()
	p.Roles[0].Activity = "daily at 09:00"
	if err := f.st.PutProject(p); err != nil {
		t.Fatal(err)
	}
	f.clk.Add(2 * time.Hour) // would be overdue under the old pattern
	f.fire()
	if len(f.queue()) != 0 {
		t.Fatalf("queue = %+v", f.queue())
	}
	d := f.due("Ada")
	want := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC) // London is UTC in November
	if d.Activity != "daily at 09:00" || !d.At.Equal(want) {
		t.Fatalf("due = %+v, want %v", d, want)
	}
}

func TestScheduleDailyEarlyJitterFiresOnce(t *testing.T) {
	const daily = `name: p
sites: ["https://x.test"]
timezone: Europe/London
roles:
  - name: patient
    activity: daily at 09:00
    personas: [{name: Ada}]
    stories:
      - {name: book, goal: book a slot}
`
	f := newFix(t, daily)
	f.s.Rand = func() float64 { return 0.1 } // 8 minutes early
	f.clk.t = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	f.fire() // first sight: tomorrow 08:52
	early := time.Date(2026, 11, 5, 8, 52, 0, 0, time.UTC)
	if d := f.due("Ada"); !d.At.Equal(early) {
		t.Fatalf("due = %v, want %v", d.At, early)
	}
	f.clk.t = early
	f.fire()
	q := f.queue()
	if len(q) != 1 {
		t.Fatalf("queue = %+v", q)
	}
	if err := f.st.DeleteQueued(q[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{early.Add(8 * time.Minute), early.Add(13 * time.Minute)} {
		f.clk.t = at
		f.fire()
		if q := f.queue(); len(q) != 0 {
			t.Fatalf("at %v: queue = %+v", at, q)
		}
	}
	tomorrow := time.Date(2026, 11, 6, 8, 52, 0, 0, time.UTC)
	if d := f.due("Ada"); !d.At.Equal(tomorrow) {
		t.Fatalf("due = %v, want %v", d.At, tomorrow)
	}
}

func TestDrainOverBudgetKeepsQueue(t *testing.T) {
	f := newSchedFix(t, activityYAML)
	if err := f.st.Enqueue(store.Queued{ID: "q1", Persona: "Ada", Role: "patient", Story: "book", Created: now}); err != nil {
		t.Fatal(err)
	}
	f.starter.SetErr(fmt.Errorf("today's spend: %w", runs.ErrOverBudget))
	if err := f.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick = %v", err)
	}
	f.starter.SetErr(runs.ErrPaused)
	if err := f.s.Tick(context.Background()); err != nil {
		t.Fatalf("Tick = %v", err)
	}
	if q := f.queue(); len(q) != 1 {
		t.Fatalf("queue = %+v", q)
	}
}

func TestScheduleActivityRemovedThenRestoredStartsFresh(t *testing.T) {
	f := newSchedFix(t, activityYAML)
	f.fire()
	p, _ := f.st.Project()
	p.Roles[0].Activity = ""
	if err := f.st.PutProject(p); err != nil {
		t.Fatal(err)
	}
	f.clk.Add(5 * time.Hour) // the old due passes while there is no activity
	f.fire()
	if d, _ := f.st.Dues(); len(d) != 0 {
		t.Fatalf("dues = %+v", d)
	}
	p.Roles[0].Activity = "every 1 h"
	if err := f.st.PutProject(p); err != nil {
		t.Fatal(err)
	}
	f.fire()
	if len(f.queue()) != 0 {
		t.Fatalf("restored activity caught up: %+v", f.queue())
	}
	if d := f.due("Ada"); !d.At.Equal(f.clk.Now().Add(time.Hour)) {
		t.Fatalf("due = %v", d.At)
	}
}

const triggeredActivityYAML = `name: p
sites: ["https://x.test"]
timezone: Europe/London
roles:
  - name: patient
    activity: every 1 h
    personas: [{name: Ada}]
    stories:
      - {name: book, goal: book a slot}
      - name: confirm
        goal: confirm the sign-up
        trigger: {message: {subject: confirm}}
`

// A story with a trigger runs on its trigger instead of the activity schedule.
func TestScheduleLeavesOutTriggeredStories(t *testing.T) {
	f := newSchedFix(t, triggeredActivityYAML)
	f.fire()
	for i := 0; i < 3; i++ {
		f.clk.Add(time.Hour)
		f.fire()
		q := f.queue()
		if len(q) != 1 || q[0].Story != "book" {
			t.Fatalf("fire %d: queue = %+v", i, q)
		}
		if err := f.st.DeleteQueued(q[0].ID); err != nil {
			t.Fatal(err)
		}
	}
	if d := f.due("Ada"); d.NextStory != 0 {
		t.Fatalf("due = %+v", d)
	}
	// book is stuck: the triggered story is not offered in its place.
	f.running("old", "Ada", "patient", "book", "j0")
	if ok, err := f.st.FinishRun("old", store.RunStuck, "stuck", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.clk.Add(time.Hour)
	f.fire()
	if q := f.queue(); len(q) != 0 {
		t.Fatalf("stuck: queue = %+v", q)
	}
}

func TestScheduleRoleWithOnlyTriggeredStoriesHasNoDue(t *testing.T) {
	f := newSchedFix(t, triggeredActivityYAML)
	f.fire()
	if d, _ := f.st.Dues(); len(d) != 1 {
		t.Fatalf("dues = %+v", d)
	}
	p, _ := f.st.Project()
	p.Roles[0].Stories = p.Roles[0].Stories[1:]
	if err := f.st.PutProject(p); err != nil {
		t.Fatal(err)
	}
	f.clk.Add(2 * time.Hour)
	f.fire()
	if len(f.queue()) != 0 {
		t.Fatalf("queue = %+v", f.queue())
	}
	if d, _ := f.st.Dues(); len(d) != 0 {
		t.Fatalf("dues = %+v", d)
	}
}

func TestDrainSkipsOverLeadBudget(t *testing.T) {
	f := newSchedFix(t, activityYAML)
	for _, q := range []store.Queued{
		{ID: "q1", Persona: "Ana", Role: "Ana", Story: "pay", Created: now},
		{ID: "q2", Persona: "Ada", Role: "patient", Story: "book", Created: now.Add(time.Second)},
	} {
		if err := f.st.Enqueue(q); err != nil {
			t.Fatal(err)
		}
	}
	f.starter.errs = []error{fmt.Errorf("Kate: %w", runs.ErrLeadOverBudget)}
	if err := f.s.drain(context.Background()); err != nil {
		t.Fatalf("drain = %v", err)
	}
	calls := f.starter.Calls()
	if len(calls) != 2 || calls[1].ID != "q2" {
		t.Fatalf("calls = %+v", calls)
	}
	if q := f.queue(); len(q) != 1 || q[0].ID != "q1" {
		t.Fatalf("queue = %+v", q)
	}
}
