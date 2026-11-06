package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// fakeSource returns fixed fires and records what Settle was told.
type fakeSource struct {
	name    string
	fires   []Fire
	err     error
	settled []string
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Due(context.Context, project.Project, time.Time) ([]Fire, error) {
	return f.fires, f.err
}
func (f *fakeSource) Settle(_ Fire, story string, _ time.Time) error {
	f.settled = append(f.settled, story)
	return nil
}

func mailFire() Fire {
	return Fire{Source: "mail", Persona: "Cleo", Role: "clinician", Stories: []string{"approve"},
		Reason: "Mail from x", Context: json.RawMessage(`{"body":"mail body"}`)}
}

func TestFireQueuesWithContext(t *testing.T) {
	f := newFix(t, schedYAML)
	src := &fakeSource{name: "mail", fires: []Fire{mailFire()}}
	f.s.Sources = []Source{src}
	if err := f.s.fire(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	q := f.queue()
	if len(q) != 1 || q[0].Story != "approve" || q[0].Persona != "Cleo" || q[0].Role != "clinician" ||
		q[0].Reason != "Mail from x" || string(q[0].Context) != `{"body":"mail body"}` || !q[0].Created.Equal(now) {
		t.Fatalf("queue = %+v", q)
	}
	if fmt.Sprint(src.settled) != "[approve]" {
		t.Fatalf("settled = %q", src.settled)
	}
}

func TestFireSkipsAndSettles(t *testing.T) {
	f := newFix(t, schedYAML)
	src := &fakeSource{name: "mail", fires: []Fire{mailFire()}}
	f.s.Sources = []Source{src}
	f.running("r1", "Cleo", "clinician", "approve", "j1")
	f.tick()
	if len(f.queue()) != 0 || fmt.Sprintf("%q", src.settled) != `[""]` {
		t.Fatalf("busy: queue %+v settled %q", f.queue(), src.settled)
	}

	g := newFix(t, schedYAML)
	src = &fakeSource{name: "mail", fires: []Fire{mailFire()}}
	g.s.Sources = []Source{src}
	g.starter.SetGate(runs.ErrPaused)
	g.tick()
	if len(g.queue()) != 0 || fmt.Sprintf("%q", src.settled) != `[""]` {
		t.Fatalf("gated: queue %+v settled %q", g.queue(), src.settled)
	}
}

func TestFireGateErrorLeavesFiresUnsettled(t *testing.T) {
	f := newFix(t, schedYAML)
	src := &fakeSource{name: "mail", fires: []Fire{mailFire()}}
	f.s.Sources = []Source{src}
	f.starter.SetGate(errors.New("usage unreadable"))
	if err := f.s.Tick(context.Background()); err == nil || !strings.Contains(err.Error(), "usage unreadable") {
		t.Fatalf("tick = %v", err)
	}
	if len(f.queue()) != 0 || len(src.settled) != 0 {
		t.Fatalf("queue %+v settled %q", f.queue(), src.settled)
	}
}

func TestFireSourceErrorDoesNotStopOthers(t *testing.T) {
	f := newFix(t, schedYAML)
	bad := &fakeSource{name: "bad", err: errors.New("boom")}
	good := &fakeSource{name: "mail", fires: []Fire{mailFire()}}
	f.s.Sources = []Source{bad, good}
	err := f.s.Tick(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if q := f.queue(); len(q) != 0 {
		// Tick also drains; the fake starter consumed the item.
		t.Fatalf("queue = %+v", q)
	}
	if calls := f.starter.Calls(); len(calls) != 1 || calls[0].Story != "approve" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestFireSchedulerErrorLeavesFireUnsettled(t *testing.T) {
	f := newFix(t, schedYAML)
	first := &fakeSource{name: "a", fires: []Fire{
		{Source: "a", Persona: "Ada", Role: "patient", Stories: []string{"lone"}, Reason: "r"},
	}}
	second := &fakeSource{name: "b", fires: []Fire{mailFire()}}
	f.s.Sources = []Source{first, second}
	// Enqueue fails for Ada's item only: a duplicate id already in the queue.
	f.s.NewID = func() string { return "dup" }
	if err := f.st.Enqueue(store.Queued{ID: "dup", Persona: "Zed", Role: "x", Story: "y", Created: now}); err != nil {
		t.Fatal(err)
	}
	err := f.s.fire(context.Background(), now)
	if err == nil {
		t.Fatal("want the enqueue error")
	}
	if len(first.settled) != 0 {
		t.Fatalf("failed fire was settled: %q", first.settled)
	}
	// The second source's item also uses id "dup", so it fails too; give it a fresh id.
	f.s.NewID = func() string { return "ok" }
	first.fires = nil
	if err := f.s.fire(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(second.settled) != "[approve]" {
		t.Fatalf("second settled = %q", second.settled)
	}
}

const allTriggersYAML = `name: p
sites: ["https://x.test"]
roles:
  - name: patient
    activity: every 1 h
    personas: [{name: Ada}]
    stories:
      - {name: book, goal: book a slot}
      - name: confirm
        goal: confirm the sign-up
        trigger: {message: {subject: confirm}}
      - name: visit
        goal: join the video appointment
        trigger: {calendar: {title: appointment, lead: 5m}}
`

// The calendar wins a persona that every source wants in one tick; the
// message fires once the persona is free and the schedule at its next slot.
func TestTriggersCalendarFirst(t *testing.T) {
	f := newFix(t, allTriggersYAML)
	f.s.Sources = Triggers(f.st, half)
	f.fire() // first sight sets the due
	f.clk.Add(time.Hour)
	f.deliver("m1", "Ada", "hello@x.test", "Please confirm")
	f.event("Ada", "e1", "Video appointment", f.clk.Now().Add(5*time.Minute))
	f.fire()
	if q := f.queue(); len(q) != 1 || q[0].Story != "visit" {
		t.Fatalf("queue = %+v", q)
	}
	if f.unfired() != 1 {
		t.Fatal("message settled while Ada was busy")
	}
	f.tick() // starts visit
	if ok, err := f.st.FinishRun("s1", store.RunDone, "ok", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.clk.Add(time.Minute)
	f.fire()
	if q := f.queue(); len(q) != 1 || q[0].Story != "confirm" {
		t.Fatalf("queue = %+v", q)
	}
	f.tick() // starts confirm
	if ok, err := f.st.FinishRun("s2", store.RunDone, "ok", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.clk.Add(time.Hour)
	f.fire()
	if q := f.queue(); len(q) != 1 || q[0].Story != "book" {
		t.Fatalf("queue = %+v", q)
	}
}
