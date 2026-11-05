package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

const schedYAML = `name: p
sites: ["https://x.test"]
roles:
  - name: patient
    personas: [{name: Ada}]
    stories:
      - name: book
        goal: book a slot
        handoffs: [{role: clinician, story: approve}]
      - name: refer
        goal: refer a friend
        handoffs: [{role: clinician, story: approve}, {role: admin, story: file}]
      - {name: lone, goal: g}
  - name: clinician
    personas: [{name: Cleo}, {name: Cy}]
    stories:
      - {name: approve, goal: approve it}
  - name: admin
    personas: [{name: Ann}]
    stories:
      - {name: file, goal: file it}
`

const noClinicianYAML = `name: p
sites: ["https://x.test"]
roles:
  - name: patient
    personas: [{name: Ada}]
    stories:
      - name: book
        goal: book a slot
        handoffs: [{role: clinician, story: approve}]
  - name: clinician
    stories:
      - {name: approve, goal: approve it}
`

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeStarter records calls. On success it creates the run from the item, as
// runs.Service does, so the queue row is consumed.
type fakeStarter struct {
	mu     sync.Mutex
	st     *store.Store
	now    func() time.Time
	errs   []error // returned in turn; nil entries succeed
	err    error   // returned once errs is used up
	calls  []store.Queued
	n      int
	called chan struct{}
}

func (f *fakeStarter) StartQueued(ctx context.Context, q store.Queued) (store.Run, error) {
	f.mu.Lock()
	defer func() {
		f.mu.Unlock()
		if f.called != nil {
			select {
			case f.called <- struct{}{}:
			default:
			}
		}
	}()
	f.calls = append(f.calls, q)
	err := f.err
	if len(f.errs) > 0 {
		err, f.errs = f.errs[0], f.errs[1:]
	}
	if err != nil {
		return store.Run{}, err
	}
	f.n++
	id := fmt.Sprintf("s%d", f.n)
	r := store.Run{ID: id, Token: "tok-" + id, Persona: q.Persona, Role: q.Role, Story: q.Story,
		State: store.RunRunning, Started: f.now(), Journey: q.Journey, Follows: q.Follows, Via: q.Via, Context: q.Context}
	if err := f.st.CreateRunFrom(r, q.ID); err != nil {
		return store.Run{}, err
	}
	return r, nil
}

func (f *fakeStarter) Calls() []store.Queued {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.Queued(nil), f.calls...)
}

func (f *fakeStarter) SetErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

type fix struct {
	t       *testing.T
	st      *store.Store
	clk     *clock
	starter *fakeStarter
	s       *Scheduler
	ids     int
}

func newFix(t *testing.T, yml string) *fix {
	t.Helper()
	st := openStore(t)
	p, err := project.Load([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutProject(p); err != nil {
		t.Fatal(err)
	}
	f := &fix{t: t, st: st, clk: &clock{t: now}}
	f.starter = &fakeStarter{st: st, now: f.clk.Now}
	var idMu sync.Mutex
	f.s = &Scheduler{Store: st, Runs: f.starter, Now: f.clk.Now, NewID: func() string {
		idMu.Lock()
		defer idMu.Unlock()
		f.ids++
		return fmt.Sprintf("id%d", f.ids)
	}}
	return f
}

func (f *fix) tick() {
	f.t.Helper()
	if err := f.s.Tick(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fix) running(id, persona, role, story, journey string) store.Run {
	f.t.Helper()
	r := store.Run{ID: id, Token: "tok-" + id, Persona: persona, Role: role, Story: story,
		State: store.RunRunning, Started: f.clk.Now(), Journey: journey}
	if err := f.st.CreateRun(r); err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *fix) waiting(id, persona, role, story, journey string, w store.Wait) store.Run {
	f.t.Helper()
	r := f.running(id, persona, role, story, journey)
	if ok, err := f.st.FinishRunWaiting(id, "outcome of "+id, f.clk.Now(), w); err != nil || !ok {
		f.t.Fatal(ok, err)
	}
	r.State, r.Outcome = store.RunWaiting, "outcome of "+id
	return r
}

func (f *fix) done(id, persona, role, story, journey, outcome string) {
	f.t.Helper()
	f.running(id, persona, role, story, journey)
	if ok, err := f.st.FinishRun(id, store.RunDone, outcome, f.clk.Now()); err != nil || !ok {
		f.t.Fatal(ok, err)
	}
}

func (f *fix) queue() []store.Queued {
	f.t.Helper()
	q, err := f.st.Queue()
	if err != nil {
		f.t.Fatal(err)
	}
	return q
}

func (f *fix) wait(runID string) store.Wait {
	f.t.Helper()
	w, err := f.st.WaitFor(runID)
	if err != nil {
		f.t.Fatal(err)
	}
	return w
}

func (f *fix) state(runID string) store.RunState {
	f.t.Helper()
	r, err := f.st.RunByID(runID)
	if err != nil {
		f.t.Fatal(err)
	}
	return r.State
}

func ctxOf(t *testing.T, q store.Queued) store.RunContext {
	t.Helper()
	var c store.RunContext
	if err := json.Unmarshal(q.Context, &c); err != nil {
		t.Fatalf("context %s: %v", q.Context, err)
	}
	return c
}

func timeWait(at time.Time) store.Wait {
	return store.Wait{Kind: "time", At: at, Deadline: at}
}

func TestTickContinuesWhenEmailArrives(t *testing.T) {
	f := newFix(t, schedYAML)
	w := store.Wait{Kind: "email", Subject: "confirm", Deadline: now.Add(24 * time.Hour)}
	f.waiting("p1", "Ada", "patient", "lone", "j1", w)
	f.tick()
	if q := f.queue(); len(q) != 0 || len(f.starter.Calls()) != 0 {
		t.Fatalf("queue %+v calls %+v", q, f.starter.Calls())
	}
	f.clk.Add(time.Minute)
	if _, err := f.st.AddMessage(store.Message{ID: "m1", UID: 1, UIDValidity: 1, Persona: "Ada",
		From: "noreply@x.test", Subject: "Please confirm your booking", Stored: f.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	f.clk.Add(time.Minute)
	f.tick()
	calls := f.starter.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	q := calls[0]
	if q.Via != "continue" || q.Persona != "Ada" || q.Role != "patient" || q.Story != "lone" || q.Journey != "j1" || q.Follows != "p1" {
		t.Fatalf("item = %+v", q)
	}
	c := ctxOf(t, q)
	if c.Kind != "continue" || !strings.Contains(c.Arrived, "Please confirm your booking") || c.MessageID != "m1" ||
		c.PreviousRun != "p1" || c.Previous != "outcome of p1" || c.WaitedFor != Describe(f.wait("p1")) {
		t.Fatalf("context = %+v", c)
	}
	if q.Reason != "Continues after "+c.Arrived {
		t.Fatalf("reason = %q", q.Reason)
	}
	if got := f.wait("p1"); got.Status != store.WaitMet || got.Detail != c.Arrived {
		t.Fatalf("wait = %+v", got)
	}
	if len(f.queue()) != 0 {
		t.Fatal("item not consumed")
	}
}

func TestTickTimesOutToStuck(t *testing.T) {
	f := newFix(t, schedYAML)
	w := store.Wait{Kind: "email", From: "clinic", Deadline: now.Add(time.Hour)}
	f.waiting("p1", "Ada", "patient", "lone", "j1", w)
	f.clk.Add(59 * time.Minute)
	f.tick()
	if f.state("p1") != store.RunWaiting {
		t.Fatal("stuck too early")
	}
	f.clk.Add(time.Minute)
	f.tick()
	if s := f.state("p1"); s != store.RunStuck {
		t.Fatalf("state = %s", s)
	}
	ww := f.wait("p1")
	bugs, err := f.st.Bugs("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(bugs) != 1 || bugs[0].Summary != StuckText(ww) || bugs[0].Step != "waiting" || bugs[0].Severity != "medium" ||
		bugs[0].Note != "outcome of p1" || bugs[0].ID == "" {
		t.Fatalf("bugs = %+v", bugs)
	}
	if ww.Status != store.WaitStuck || ww.Detail != StuckText(ww) {
		t.Fatalf("wait = %+v", ww)
	}
	if len(f.queue()) != 0 || len(f.starter.Calls()) != 0 {
		t.Fatal("something was queued or started")
	}
}

func TestTickTimeWait(t *testing.T) {
	f := newFix(t, schedYAML)
	f.waiting("p1", "Ada", "patient", "lone", "j1", timeWait(now.Add(30*time.Minute)))
	f.clk.Add(29 * time.Minute)
	f.tick()
	if len(f.starter.Calls()) != 0 || f.wait("p1").Status != store.WaitOpen {
		t.Fatal("resolved too early")
	}
	f.clk.Add(time.Minute)
	f.tick()
	calls := f.starter.Calls()
	if len(calls) != 1 || calls[0].Via != "continue" {
		t.Fatalf("calls = %+v", calls)
	}
	if c := ctxOf(t, calls[0]); c.Arrived != "time reached" {
		t.Fatalf("arrived = %q", c.Arrived)
	}
}

func TestHandoffFanOutAndBack(t *testing.T) {
	f := newFix(t, schedYAML)
	f.waiting("p1", "Ada", "patient", "book", "j1",
		store.Wait{Kind: "handoff", Role: "clinician", Story: "approve", Deadline: now.Add(24 * time.Hour)})
	f.tick()
	calls := f.starter.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	h := calls[0]
	if h.Persona != "Cleo" || h.Role != "clinician" || h.Story != "approve" || h.Via != "handoff" ||
		h.Follows != "p1" || h.Journey != "j1" || h.Reason != "Handed off by Ada (book)" {
		t.Fatalf("handoff item = %+v", h)
	}
	c := ctxOf(t, h)
	if c.Kind != "handoff" || c.PreviousRun != "p1" || c.Previous != "outcome of p1" || c.From == nil ||
		*c.From != (store.ContextFrom{Persona: "Ada", Role: "patient", Story: "book", Summary: "outcome of p1"}) {
		t.Fatalf("handoff context = %+v", c)
	}
	f.clk.Add(5 * time.Minute)
	if ok, err := f.st.FinishRun("s1", store.RunDone, "approved the booking", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.tick()
	calls = f.starter.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	back := calls[1]
	if back.Persona != "Ada" || back.Story != "book" || back.Via != "continue" || back.Journey != "j1" || back.Follows != "p1" {
		t.Fatalf("continuation = %+v", back)
	}
	if bc := ctxOf(t, back); !strings.Contains(bc.Arrived, "Cleo") || !strings.Contains(bc.Arrived, "approved the booking") {
		t.Fatalf("arrived = %q", bc.Arrived)
	}
}

func TestDoneFansOutDeclaredHandoffs(t *testing.T) {
	f := newFix(t, schedYAML)
	f.starter.SetErr(runs.ErrBusy)
	f.done("p1", "Ada", "patient", "refer", "j1", "referred Bob")
	f.tick()
	q := f.queue()
	if len(q) != 2 {
		t.Fatalf("queue = %+v", q)
	}
	if q[0].Persona != "Cleo" || q[0].Story != "approve" || q[1].Persona != "Ann" || q[1].Story != "file" {
		t.Fatalf("queue order = %+v", q)
	}
	for _, it := range q {
		if it.Via != "handoff" || it.Follows != "p1" || it.Journey != "j1" || it.Reason != "Handed off by Ada (refer)" {
			t.Fatalf("item = %+v", it)
		}
		if c := ctxOf(t, it); c.From == nil || c.From.Summary != "referred Bob" || c.Previous != "referred Bob" {
			t.Fatalf("context = %+v", c)
		}
	}
	if un, err := f.st.Unfollowed(); err != nil || len(un) != 0 {
		t.Fatalf("unfollowed = %+v %v", un, err)
	}
	f.tick() // a second tick hands off nothing more
	if len(f.queue()) != 2 {
		t.Fatal("handed off twice")
	}
}

func TestHandoffOncePerChain(t *testing.T) {
	f := newFix(t, schedYAML)
	hw := store.Wait{Kind: "handoff", Role: "clinician", Story: "approve", Deadline: now.Add(24 * time.Hour)}
	// Against a started run.
	f.waiting("p1", "Ada", "patient", "book", "j1", hw)
	f.tick()
	if len(f.starter.Calls()) != 1 {
		t.Fatal("first handoff not started")
	}
	f.waiting("p2", "Ada", "patient", "book", "j1", hw)
	f.tick()
	if len(f.starter.Calls()) != 1 || len(f.queue()) != 0 {
		t.Fatalf("second handoff: calls %+v queue %+v", f.starter.Calls(), f.queue())
	}
	// Against a queued item, in one tick.
	f.starter.SetErr(runs.ErrBusy)
	f.waiting("p3", "Ada", "patient", "book", "j2", hw)
	f.waiting("p4", "Ada", "patient", "book", "j2", hw)
	f.tick()
	if q := f.queue(); len(q) != 1 || q[0].Follows != "p3" {
		t.Fatalf("queue = %+v", q)
	}
}

func TestChainCap(t *testing.T) {
	f := newFix(t, schedYAML)
	for i := range MaxChainRuns - 1 {
		f.done(fmt.Sprintf("d%d", i), "Ada", "patient", "lone", "j1", "ok")
	}
	f.waiting("p1", "Ada", "patient", "lone", "j1", timeWait(now.Add(time.Minute)))
	f.clk.Add(time.Minute)
	f.tick()
	if s := f.state("p1"); s != store.RunStuck {
		t.Fatalf("state = %s", s)
	}
	if w := f.wait("p1"); w.Status != store.WaitStuck || w.Detail != "the chain reached 10 runs" {
		t.Fatalf("wait = %+v", w)
	}
	if len(f.queue()) != 0 || len(f.starter.Calls()) != 0 {
		t.Fatal("something was queued or started")
	}
}

func TestChainCapStopsHandoff(t *testing.T) {
	f := newFix(t, schedYAML)
	for i := range MaxChainRuns - 1 {
		f.done(fmt.Sprintf("d%d", i), "Ada", "patient", "lone", "j1", "ok")
	}
	f.waiting("p1", "Ada", "patient", "book", "j1",
		store.Wait{Kind: "handoff", Role: "clinician", Story: "approve", Deadline: now.Add(time.Hour)})
	f.tick()
	if w := f.wait("p1"); w.Status != store.WaitStuck || w.Detail != "the chain reached 10 runs" {
		t.Fatalf("wait = %+v", w)
	}
	if len(f.queue()) != 0 || len(f.starter.Calls()) != 0 {
		t.Fatal("something was queued or started")
	}
}

func TestHandoffNoPersonaStuck(t *testing.T) {
	f := newFix(t, noClinicianYAML)
	f.waiting("p1", "Ada", "patient", "book", "j1",
		store.Wait{Kind: "handoff", Role: "clinician", Story: "approve", Deadline: now.Add(time.Hour)})
	f.tick()
	if s := f.state("p1"); s != store.RunStuck {
		t.Fatalf("state = %s", s)
	}
	want := `nobody in role "clinician" to hand off to`
	if w := f.wait("p1"); w.Status != store.WaitStuck || w.Detail != want {
		t.Fatalf("wait = %+v", w)
	}
	if bugs, _ := f.st.Bugs("p1"); len(bugs) != 1 || bugs[0].Summary != want {
		t.Fatalf("bugs = %+v", bugs)
	}
	if len(f.queue()) != 0 || len(f.starter.Calls()) != 0 {
		t.Fatal("something was queued or started")
	}
}

func TestTickBusyKeepsQueue(t *testing.T) {
	f := newFix(t, schedYAML)
	f.starter.SetErr(runs.ErrBusy)
	f.waiting("p1", "Ada", "patient", "lone", "j1", timeWait(now.Add(time.Minute)))
	f.clk.Add(time.Minute)
	f.tick()
	if len(f.queue()) != 1 || len(f.starter.Calls()) != 1 {
		t.Fatal("busy start should keep the item")
	}
	f.starter.SetErr(runs.ErrNoFence)
	f.tick()
	if len(f.queue()) != 1 || len(f.starter.Calls()) != 2 {
		t.Fatal("no fence should keep the item")
	}
	f.starter.SetErr(nil)
	f.tick()
	if len(f.queue()) != 0 || len(f.starter.Calls()) != 3 {
		t.Fatal("retry did not start the item")
	}
}

func TestTickDropsInvalidQueued(t *testing.T) {
	f := newFix(t, schedYAML)
	for i, p := range []string{"Gone", "Ada"} {
		if err := f.st.Enqueue(store.Queued{ID: fmt.Sprintf("q%d", i), Persona: p, Role: "patient", Story: "lone",
			Journey: "j1", Via: "continue", Created: now.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	f.starter.errs = []error{fmt.Errorf("%w: unknown persona %q", runs.ErrInvalid, "Gone"), nil}
	f.tick()
	calls := f.starter.Calls()
	if len(calls) != 2 || calls[0].ID != "q0" || calls[1].ID != "q1" {
		t.Fatalf("calls = %+v", calls)
	}
	if len(f.queue()) != 0 {
		t.Fatalf("queue = %+v", f.queue())
	}
	if f.state("s1") != store.RunRunning {
		t.Fatal("second item not started")
	}
}

func TestTickStartsOneRunPerTick(t *testing.T) {
	f := newFix(t, schedYAML)
	f.starter.SetErr(runs.ErrBusy)
	f.done("p1", "Ada", "patient", "refer", "j1", "referred")
	f.tick()
	f.starter.SetErr(nil)
	f.tick()
	if len(f.queue()) != 1 {
		t.Fatalf("queue = %+v", f.queue())
	}
}

func TestTickResolvesEachWaitOnce(t *testing.T) {
	f := newFix(t, schedYAML)
	f.starter.SetErr(runs.ErrBusy)
	f.waiting("p1", "Ada", "patient", "lone", "j1", timeWait(now.Add(2*time.Minute)))
	f.clk.Add(time.Second)
	f.waiting("p2", "Ada", "patient", "lone", "j2", timeWait(now.Add(2*time.Minute)))
	f.clk.Add(3 * time.Minute)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- f.s.Tick(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	q := f.queue()
	if len(q) != 2 || q[0].Follows != "p1" || q[1].Follows != "p2" {
		t.Fatalf("queue = %+v", q)
	}
}

func TestPokeRunsTick(t *testing.T) {
	f := newFix(t, schedYAML)
	f.starter.called = make(chan struct{}, 1)
	f.starter.errs = []error{runs.ErrBusy}
	if err := f.st.Enqueue(store.Queued{ID: "q0", Persona: "Ada", Role: "patient", Story: "lone",
		Journey: "j1", Via: "continue", Created: now}); err != nil {
		t.Fatal(err)
	}
	f.s.Every = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case <-f.starter.called: // the first tick, refused as busy
	case <-time.After(time.Second):
		t.Fatal("no tick at start")
	}
	f.s.Poke()
	f.s.Poke() // coalesced, never blocks
	select {
	case <-f.starter.called:
	case <-time.After(time.Second):
		t.Fatal("Poke did not run a tick")
	}
	deadline := time.Now().Add(time.Second)
	for len(f.queue()) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("queued item not started")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func handoffWait() store.Wait {
	return store.Wait{Kind: "handoff", Role: "clinician", Story: "approve", Deadline: now.Add(24 * time.Hour)}
}

func TestHandoffWaitStuckWhenTargetEnds(t *testing.T) {
	for _, st := range []store.RunState{store.RunStuck, store.RunFailed} {
		t.Run(string(st), func(t *testing.T) {
			f := newFix(t, schedYAML)
			f.waiting("p1", "Ada", "patient", "book", "j1", handoffWait())
			f.tick()
			if f.state("s1") != store.RunRunning {
				t.Fatal("handoff not started")
			}
			f.tick() // running: the wait stays open
			if w := f.wait("p1"); w.Status != store.WaitOpen {
				t.Fatalf("closed while target runs: %+v", w)
			}
			f.clk.Add(time.Minute)
			if ok, err := f.st.FinishRun("s1", st, "could not sign in\x1b[2J\nsecond line", f.clk.Now()); err != nil || !ok {
				t.Fatal(ok, err)
			}
			f.tick()
			want := `clinician could not finish "approve": could not sign in [2J`
			if w := f.wait("p1"); w.Status != store.WaitStuck || w.Detail != want {
				t.Fatalf("wait = %+v", w)
			}
			if f.state("p1") != store.RunStuck {
				t.Fatal("waiting run not stuck")
			}
			if bugs, _ := f.st.Bugs("p1"); len(bugs) != 1 || bugs[0].Summary != want {
				t.Fatalf("bugs = %+v", bugs)
			}
		})
	}
}

func TestHandoffWaitStuckWhenQueuedRemoved(t *testing.T) {
	f := newFix(t, schedYAML)
	f.starter.SetErr(runs.ErrBusy)
	f.waiting("p1", "Ada", "patient", "book", "j1", handoffWait())
	f.tick()
	q := f.queue()
	if len(q) != 1 {
		t.Fatalf("queue = %+v", q)
	}
	f.tick() // still queued: the wait stays open
	if w := f.wait("p1"); w.Status != store.WaitOpen {
		t.Fatalf("closed while target queued: %+v", w)
	}
	if err := f.st.DeleteQueued(q[0].ID); err != nil {
		t.Fatal(err)
	}
	f.tick()
	want := `the hand-off to clinician "approve" was removed before it ran`
	if w := f.wait("p1"); w.Status != store.WaitStuck || w.Detail != want {
		t.Fatalf("wait = %+v", w)
	}
}

func TestHandoffWaitOpenUntilFollowed(t *testing.T) {
	f := newFix(t, schedYAML)
	f.waiting("p1", "Ada", "patient", "book", "j1", handoffWait())
	// Before the fan-out has queued anything, the wait must not look dropped.
	if err := f.s.resolveAll(f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if w := f.wait("p1"); w.Status != store.WaitOpen {
		t.Fatalf("wait = %+v", w)
	}
}

func TestHandoffWaitOpenWhileTargetWaits(t *testing.T) {
	f := newFix(t, schedYAML)
	f.waiting("p1", "Ada", "patient", "book", "j1", handoffWait())
	f.tick()
	if ok, err := f.st.FinishRunWaiting("s1", "waits for mail", f.clk.Now(),
		store.Wait{Kind: "time", At: now.Add(time.Hour), Deadline: now.Add(time.Hour)}); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.tick()
	if w := f.wait("p1"); w.Status != store.WaitOpen {
		t.Fatalf("wait = %+v", w)
	}
}

// A target that waited and was continued is represented by its continuation:
// when that fails, the hand-off wait ends.
func TestHandoffWaitStuckWhenContinuationFails(t *testing.T) {
	f := newFix(t, schedYAML)
	f.waiting("p1", "Ada", "patient", "book", "j1", handoffWait())
	f.tick()
	if ok, err := f.st.FinishRunWaiting("s1", "waits a minute", f.clk.Now(), timeWait(now.Add(time.Minute))); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.clk.Add(2 * time.Minute)
	f.tick() // s1's wait is met and its continuation s2 starts
	if f.state("s2") != store.RunRunning {
		t.Fatal("continuation not started")
	}
	if w := f.wait("p1"); w.Status != store.WaitOpen {
		t.Fatalf("closed while continuation runs: %+v", w)
	}
	if ok, err := f.st.FinishRun("s2", store.RunFailed, "page broke", f.clk.Now()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.tick()
	want := `clinician could not finish "approve": page broke`
	if w := f.wait("p1"); w.Status != store.WaitStuck || w.Detail != want {
		t.Fatalf("wait = %+v", w)
	}
}

func TestContextOutcomeCapped(t *testing.T) {
	f := newFix(t, schedYAML)
	f.starter.SetErr(runs.ErrBusy)
	long := "a\x1b[2J\nb" + strings.Repeat("é", 3000)
	f.done("p1", "Ada", "patient", "book", "j1", long)
	f.tick()
	q := f.queue()
	if len(q) != 1 {
		t.Fatalf("queue = %+v", q)
	}
	c := ctxOf(t, q[0])
	for _, s := range []string{c.Previous, c.From.Summary} {
		if n := utf8.RuneCountInString(s); n != MaxContextText {
			t.Fatalf("len %d", n)
		}
		if strings.ContainsRune(s, '\x1b') || !strings.HasPrefix(s, "a [2J\nb") {
			t.Fatalf("not cleaned: %q", s[:12])
		}
	}
}

func TestDrainLogsNoFence(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f := newFix(t, schedYAML)
	f.starter.SetErr(fmt.Errorf("%w: no NetworkPolicy", runs.ErrNoFence))
	if err := f.st.Enqueue(store.Queued{ID: "q0", Persona: "Ada", Role: "patient", Story: "lone",
		Journey: "j1", Via: "continue", Created: now}); err != nil {
		t.Fatal(err)
	}
	f.tick()
	if out := buf.String(); !strings.Contains(out, "Ada/lone") || !strings.Contains(out, "runs are not fenced") {
		t.Fatalf("log = %q", out)
	}
}
