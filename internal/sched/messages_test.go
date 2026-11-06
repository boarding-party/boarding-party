package sched

import (
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

const messageYAML = `name: p
sites: ["https://x.test"]
roles:
  - name: patient
    personas: [{name: Ada}]
    stories:
      - {name: lone, goal: g}
      - name: confirm
        goal: confirm the sign-up
        trigger: {message: {from: smesolutions.uk, subject: confirm}}
`

func newMsgFix(t *testing.T) *fix {
	t.Helper()
	f := newFix(t, messageYAML)
	f.s.Sources = []Source{&Messages{Store: f.st}}
	return f
}

func (f *fix) deliver(id, persona, from, subject string) {
	f.t.Helper()
	if _, err := f.st.AddMessage(store.Message{ID: id, UID: uint32(len(id)) + uint32(id[len(id)-1]), UIDValidity: 1,
		Persona: persona, From: from, Subject: subject, Stored: f.clk.Now()}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fix) unfired() int {
	f.t.Helper()
	ms, err := f.st.UnfiredMessages(f.clk.Now().Add(-LookBack), 10)
	if err != nil {
		f.t.Fatal(err)
	}
	return len(ms)
}

func (f *fix) firedStory(id string) (string, bool) {
	f.t.Helper()
	m, err := f.st.MessageFired([]string{id})
	if err != nil {
		f.t.Fatal(err)
	}
	s, ok := m[id]
	return s, ok
}

func TestMessageFiresMatchingStory(t *testing.T) {
	f := newMsgFix(t)
	f.deliver("m1", "Ada", "hello@smesolutions.uk", "Confirm your sign-up")
	f.fire()
	q := f.queue()
	if len(q) != 1 || q[0].Persona != "Ada" || q[0].Role != "patient" || q[0].Story != "confirm" || q[0].Reason != "Email: Confirm your sign-up" {
		t.Fatalf("queue = %+v", q)
	}
	c := ctxOf(t, q[0])
	if c.Kind != "message" || c.MessageID != "m1" || c.Arrived != "Email from hello@smesolutions.uk: Confirm your sign-up" {
		t.Fatalf("context = %+v", c)
	}
	if s, ok := f.firedStory("m1"); !ok || s != "confirm" {
		t.Fatalf("fired = %q %v", s, ok)
	}
	f.fire()
	if len(f.queue()) != 1 {
		t.Fatalf("queue = %+v", f.queue())
	}
}

func TestMessageNoMatchIsMarked(t *testing.T) {
	f := newMsgFix(t)
	f.deliver("m1", "Ada", "news@elsewhere.test", "Confirm your sign-up")
	f.fire()
	if len(f.queue()) != 0 {
		t.Fatalf("queue = %+v", f.queue())
	}
	if f.unfired() != 0 {
		t.Fatal("message offered again")
	}
	if s, ok := f.firedStory("m1"); !ok || s != "" {
		t.Fatalf("fired = %q %v", s, ok)
	}
}

func TestMessageUnknownPersonaIsMarked(t *testing.T) {
	f := newMsgFix(t)
	f.deliver("m1", "Gone", "hello@smesolutions.uk", "Confirm")
	f.fire()
	if len(f.queue()) != 0 || f.unfired() != 0 {
		t.Fatalf("queue = %+v", f.queue())
	}
}

// A burst starts one run; the second message waits for the persona to be free.
func TestMessageBurstFiresOnce(t *testing.T) {
	f := newMsgFix(t)
	f.deliver("m1", "Ada", "hello@smesolutions.uk", "Confirm one")
	f.deliver("m2", "Ada", "hello@smesolutions.uk", "Confirm two")
	f.fire()
	if q := f.queue(); len(q) != 1 || ctxOf(t, q[0]).MessageID != "m1" {
		t.Fatalf("queue = %+v", q)
	}
	if s, _ := f.firedStory("m1"); s != "confirm" {
		t.Fatalf("m1 story = %q", s)
	}
	if _, ok := f.firedStory("m2"); ok {
		t.Fatal("m2 settled while Ada was busy")
	}
	f.tick() // starts m1's run
	f.fire()
	if len(f.queue()) != 0 {
		t.Fatalf("queued while running: %+v", f.queue())
	}
	if ok, err := f.st.FinishRun("s1", store.RunDone, "ok", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.clk.Add(time.Minute)
	f.fire()
	if q := f.queue(); len(q) != 1 || ctxOf(t, q[0]).MessageID != "m2" {
		t.Fatalf("queue = %+v", q)
	}
	if s, _ := f.firedStory("m2"); s != "confirm" {
		t.Fatalf("m2 story = %q", s)
	}
}

func TestMessageWhileBusyFiresLater(t *testing.T) {
	f := newMsgFix(t)
	f.running("r1", "Ada", "patient", "lone", "j1")
	f.deliver("m1", "Ada", "hello@smesolutions.uk", "Confirm your sign-up")
	f.fire()
	if len(f.queue()) != 0 || f.unfired() != 1 {
		t.Fatalf("queue = %+v unfired = %d", f.queue(), f.unfired())
	}
	if ok, err := f.st.FinishRun("r1", store.RunDone, "ok", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.clk.Add(time.Minute)
	f.fire()
	if q := f.queue(); len(q) != 1 || q[0].Story != "confirm" || ctxOf(t, q[0]).MessageID != "m1" {
		t.Fatalf("queue = %+v", q)
	}
}

// The message that ends a wait belongs to the continuation: it never starts a
// triggered story, not even after the continuation finishes.
func TestMessageResolvingWaitDoesNotTrigger(t *testing.T) {
	f := newMsgFix(t)
	f.waiting("p1", "Ada", "patient", "lone", "j1", store.Wait{Kind: "email", Subject: "confirm", Deadline: now.Add(24 * time.Hour)})
	f.clk.Add(time.Minute)
	f.deliver("m1", "Ada", "hello@smesolutions.uk", "Confirm your sign-up")
	f.clk.Add(time.Minute)
	f.tick()
	calls := f.starter.Calls()
	if len(calls) != 1 || calls[0].Via != "continue" || calls[0].Story != "lone" {
		t.Fatalf("calls = %+v queue = %+v", calls, f.queue())
	}
	if len(f.queue()) != 0 {
		t.Fatalf("queue = %+v", f.queue())
	}
	if s, ok := f.firedStory("m1"); !ok || s != "" {
		t.Fatalf("fired = %q %v", s, ok)
	}
	if ok, err := f.st.FinishRun("s1", store.RunDone, "ok", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for i := 0; i < 3; i++ {
		f.clk.Add(time.Minute)
		f.tick()
	}
	if calls := f.starter.Calls(); len(calls) != 1 || len(f.queue()) != 0 {
		t.Fatalf("calls = %+v queue = %+v", calls, f.queue())
	}
}

func TestMessageRespectsStuckAndPaused(t *testing.T) {
	f := newMsgFix(t)
	if err := f.st.CreateRun(store.Run{ID: "old", Token: "t", Persona: "Ada", Role: "patient", Story: "confirm",
		State: store.RunStuck, Started: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f.deliver("m1", "Ada", "hello@smesolutions.uk", "Confirm")
	f.fire()
	if len(f.queue()) != 0 || f.unfired() != 0 {
		t.Fatalf("stuck: queue = %+v", f.queue())
	}

	g := newMsgFix(t)
	g.starter.SetGate(runs.ErrPaused)
	g.deliver("m1", "Ada", "hello@smesolutions.uk", "Confirm")
	g.fire()
	if len(g.queue()) != 0 || g.unfired() != 0 {
		t.Fatalf("paused: queue = %+v", g.queue())
	}
}
