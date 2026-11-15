package runs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// seed creates a run in the given state started `age` before t0.
func seed(t *testing.T, svc *Service, id string, state store.RunState, session string, age time.Duration) {
	t.Helper()
	r := store.Run{ID: id, Token: "tok-" + id, Persona: "ann", Role: "buyer", Story: "checkout",
		State: state, Session: session, Started: t0.Add(-age)}
	if err := svc.Store.CreateRun(r); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, svc *Service, id string) store.Run {
	t.Helper()
	r, err := svc.Store.RunByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReconcileTimesOutSilentRun(t *testing.T) {
	svc, fl := newSvc(t)
	fl.phases = map[string]phaseInfo{"s1": {"Running", ""}}
	seed(t, svc, "r1", store.RunRunning, "s1", 21*time.Minute)
	if err := svc.Reconcile(context.Background(), t0, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	r := get(t, svc, "r1")
	if r.State != store.RunFailed || r.Outcome != "no finish within 20m0s" {
		t.Errorf("got %s %q", r.State, r.Outcome)
	}
	if len(fl.stopped) != 1 || fl.stopped[0] != "s1" {
		t.Errorf("stopped = %v", fl.stopped)
	}
}

func TestReconcileSessionGoneFailsRun(t *testing.T) {
	svc, fl := newSvc(t)
	seed(t, svc, "r1", store.RunRunning, "s1", time.Minute)
	if err := svc.Reconcile(context.Background(), t0, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	r := get(t, svc, "r1")
	if r.State != store.RunFailed || r.Outcome != "lost on restart" {
		t.Errorf("got %s %q", r.State, r.Outcome)
	}
	if len(fl.stopped) != 1 {
		t.Errorf("stopped = %v", fl.stopped)
	}
}

func TestReconcileLeavesHealthyRun(t *testing.T) {
	svc, fl := newSvc(t)
	fl.phases = map[string]phaseInfo{"s1": {"Running", ""}}
	seed(t, svc, "r1", store.RunRunning, "s1", 5*time.Minute)
	if err := svc.Reconcile(context.Background(), t0, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	if r := get(t, svc, "r1"); r.State != store.RunRunning {
		t.Errorf("state = %s", r.State)
	}
	if len(fl.stopped) != 0 {
		t.Errorf("stopped = %v", fl.stopped)
	}
}

func TestReconcileSessionFailedPhase(t *testing.T) {
	svc, fl := newSvc(t)
	fl.phases = map[string]phaseInfo{"s1": {"Failed", "pod OOMKilled"}}
	seed(t, svc, "r1", store.RunRunning, "s1", time.Minute)
	if err := svc.Reconcile(context.Background(), t0, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	r := get(t, svc, "r1")
	if r.State != store.RunFailed || !strings.Contains(r.Outcome, "pod OOMKilled") {
		t.Errorf("got %s %q", r.State, r.Outcome)
	}
	if len(fl.stopped) != 1 {
		t.Errorf("stopped = %v", fl.stopped)
	}
}

func TestReconcileDonePhaseWithoutFinishFails(t *testing.T) {
	svc, fl := newSvc(t)
	fl.phases = map[string]phaseInfo{"s1": {"Done", ""}}
	seed(t, svc, "r1", store.RunRunning, "s1", time.Minute)
	if err := svc.Reconcile(context.Background(), t0, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	r := get(t, svc, "r1")
	if r.State != store.RunFailed || !strings.Contains(r.Outcome, "without finish_run") {
		t.Errorf("got %s %q", r.State, r.Outcome)
	}
}

func TestReconcilePendingWithoutSessionLeftAloneUntilTimeout(t *testing.T) {
	svc, fl := newSvc(t)
	seed(t, svc, "young", store.RunPending, "", time.Minute)
	seed(t, svc, "old", store.RunPending, "", 21*time.Minute)
	if err := svc.Reconcile(context.Background(), t0, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	if r := get(t, svc, "young"); r.State != store.RunPending {
		t.Errorf("young state = %s", r.State)
	}
	if r := get(t, svc, "old"); r.State != store.RunFailed {
		t.Errorf("old state = %s", r.State)
	}
	if len(fl.stopped) != 1 || fl.stopped[0] != "sess-old" {
		t.Errorf("only the timed-out run's possible orphan is stopped: %v", fl.stopped)
	}
}

func TestReconcileDoesNotOverwriteFinishedRun(t *testing.T) {
	svc, _ := newSvc(t)
	seed(t, svc, "r1", store.RunRunning, "s1", 30*time.Minute)
	won, err := svc.Store.FinishRun("r1", store.RunDone, "all good", t0)
	if err != nil || !won {
		t.Fatal(won, err)
	}
	if err := svc.Reconcile(context.Background(), t0, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	if r := get(t, svc, "r1"); r.State != store.RunDone || r.Outcome != "all good" {
		t.Errorf("got %s %q", r.State, r.Outcome)
	}
}

func TestReconcileOrphanStopsDeterministicSession(t *testing.T) {
	svc, fl := newSvc(t)
	seed(t, svc, "r1", store.RunPending, "", 21*time.Minute)
	if err := svc.Reconcile(context.Background(), t0, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	if len(fl.stopped) != 1 || fl.stopped[0] != "sess-r1" {
		t.Errorf("stopped = %v", fl.stopped)
	}
}

func TestReconcileUnknownPhasePastTimeoutFails(t *testing.T) {
	svc, fl := newSvc(t)
	fl.phases = map[string]phaseInfo{"s1": {"", ""}}
	seed(t, svc, "r1", store.RunRunning, "s1", 21*time.Minute)
	if err := svc.Reconcile(context.Background(), t0, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	if r := get(t, svc, "r1"); r.State != store.RunFailed || r.Outcome != "no finish within 20m0s" {
		t.Errorf("got %s %q", r.State, r.Outcome)
	}
}

func TestReconcileReapsTerminalAndUnknownSessions(t *testing.T) {
	svc, fl := newSvc(t)
	seed(t, svc, "done1", store.RunDone, "s-done", time.Hour)
	seed(t, svc, "stuck1", store.RunStuck, "s-stuck", time.Hour)
	seed(t, svc, "fail1", store.RunFailed, "s-fail", time.Hour)
	seed(t, svc, "live1", store.RunRunning, "s-live", time.Minute)
	fl.phases = map[string]phaseInfo{"s-live": {"Running", ""}}
	fl.sessions = map[string]string{
		"s-done": "done1", "s-stuck": "stuck1", "s-fail": "fail1",
		"s-live": "live1", "s-orphan": "nosuchrun",
	}
	if err := svc.Reconcile(context.Background(), t0, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range fl.stopped {
		got[s] = true
	}
	for _, want := range []string{"s-done", "s-stuck", "s-fail", "s-orphan"} {
		if !got[want] {
			t.Errorf("%s not reaped (stopped %v)", want, fl.stopped)
		}
	}
	if got["s-live"] {
		t.Errorf("active run's session was stopped")
	}
}

func TestReapStopsWaitingSession(t *testing.T) {
	svc, fl := newSvc(t)
	r, _ := svc.Start(context.Background(), "ann", "checkout")
	if err := svc.FinishWaiting(context.Background(), r.Token, store.Wait{Kind: "time", At: time.Now().Add(time.Hour), Deadline: time.Now().Add(2 * time.Hour)}, "later"); err != nil {
		t.Fatal(err)
	}
	fl.stopped = nil
	fl.sessions = map[string]string{r.Session: r.ID}
	if err := svc.Reconcile(context.Background(), time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
	if len(fl.stopped) != 1 || fl.stopped[0] != r.Session {
		t.Fatalf("stopped: %v", fl.stopped)
	}
}

// A Spot node taken back (or an evicted pod) replaces a run's pod: the
// Deployment starts the agent again. The run carries on, with the restart
// on record and the timeout counted from it.
func reconcileAt(t *testing.T, svc *Service, at time.Time) {
	t.Helper()
	if err := svc.Reconcile(context.Background(), at, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileRecordsAReplacedPod(t *testing.T) {
	svc, fl := newSvc(t)
	fl.phases = map[string]phaseInfo{"s1": {"Running", ""}}
	fl.podNames = map[string]string{"s1": "p1"}
	seed(t, svc, "r1", store.RunRunning, "s1", time.Minute)
	reconcileAt(t, svc, t0)
	if r := get(t, svc, "r1"); len(r.Restarts) != 0 {
		t.Fatalf("first pod counted as a restart: %v", r.Restarts)
	}
	fl.podNames["s1"] = "p2"
	reconcileAt(t, svc, t0.Add(time.Minute))
	r := get(t, svc, "r1")
	if r.State != store.RunRunning || len(r.Restarts) != 1 || !r.Restarts[0].Equal(t0.Add(time.Minute)) {
		t.Fatalf("state %s restarts %v, want running with one restart at t0+1m", r.State, r.Restarts)
	}
	reconcileAt(t, svc, t0.Add(2*time.Minute))
	if r := get(t, svc, "r1"); len(r.Restarts) != 1 {
		t.Fatalf("the same pod counted again: %v", r.Restarts)
	}
}

func TestReconcileTimesOutFromTheLastRestart(t *testing.T) {
	svc, fl := newSvc(t)
	fl.phases = map[string]phaseInfo{"s1": {"Running", ""}}
	fl.podNames = map[string]string{"s1": "p1"}
	seed(t, svc, "r1", store.RunRunning, "s1", 19*time.Minute)
	reconcileAt(t, svc, t0)
	fl.podNames["s1"] = "p2"
	reconcileAt(t, svc, t0)
	reconcileAt(t, svc, t0.Add(5*time.Minute)) // 24 minutes since start
	if r := get(t, svc, "r1"); r.State != store.RunRunning {
		t.Fatalf("state %s %q: the agent started again 5 minutes ago", r.State, r.Outcome)
	}
	reconcileAt(t, svc, t0.Add(21*time.Minute))
	if r := get(t, svc, "r1"); r.State != store.RunFailed || r.Outcome != "no finish within 20m0s" {
		t.Fatalf("got %s %q", r.State, r.Outcome)
	}
}

func TestReconcileFailsAPodReplacedTooOften(t *testing.T) {
	svc, fl := newSvc(t)
	fl.phases = map[string]phaseInfo{"s1": {"Running", ""}}
	fl.podNames = map[string]string{"s1": "p0"}
	seed(t, svc, "r1", store.RunRunning, "s1", time.Minute)
	reconcileAt(t, svc, t0)
	for i, pod := range []string{"p1", "p2", "p3"} {
		fl.podNames["s1"] = pod
		reconcileAt(t, svc, t0.Add(time.Duration(i+1)*time.Minute))
	}
	r := get(t, svc, "r1")
	if r.State != store.RunFailed || !strings.Contains(r.Outcome, "replaced 3 times") {
		t.Fatalf("got %s %q, want failed after the third replacement", r.State, r.Outcome)
	}
}
