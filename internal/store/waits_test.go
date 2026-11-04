package store

import (
	"testing"
	"time"
)

func waitRun(t *testing.T, s *Store, id string, state RunState) {
	t.Helper()
	if err := s.CreateRun(Run{ID: id, Token: "tok-" + id, State: state, Started: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func countWaits(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM waits`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFinishRunWaitingOnlyActive(t *testing.T) {
	s := open(t)
	waitRun(t, s, "r1", RunRunning)
	fin := time.Unix(100, 5).UTC()
	w := Wait{Kind: "email", From: "a@b.c", Subject: "Hi", AfterID: "m9", Deadline: fin.Add(time.Hour)}
	ok, err := s.FinishRunWaiting("r1", "waiting for mail", fin, w)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	r, _ := s.RunByID("r1")
	if r.State != RunWaiting || r.Outcome != "waiting for mail" {
		t.Fatalf("run %+v", r)
	}
	got, err := s.WaitFor("r1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != WaitOpen || !got.Began.Equal(fin) || got.RunID != "r1" || got.From != "a@b.c" || got.AfterID != "m9" || !got.Deadline.Equal(w.Deadline) {
		t.Fatalf("wait %+v", got)
	}
	ok, err = s.FinishRunWaiting("r1", "again", fin, w)
	if err != nil || ok {
		t.Fatalf("second: ok=%v err=%v", ok, err)
	}
	if n := countWaits(t, s); n != 1 {
		t.Fatalf("waits %d", n)
	}

	waitRun(t, s, "r2", RunDone)
	ok, err = s.FinishRunWaiting("r2", "x", fin, w)
	if err != nil || ok {
		t.Fatalf("done run: ok=%v err=%v", ok, err)
	}
	if n := countWaits(t, s); n != 1 {
		t.Fatalf("waits %d", n)
	}
}

func startWait(t *testing.T, s *Store, id string, began time.Time) {
	t.Helper()
	waitRun(t, s, id, RunRunning)
	if ok, err := s.FinishRunWaiting(id, "", began, Wait{Kind: "time"}); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestResolveWaitAtMostOnce(t *testing.T) {
	s := open(t)
	startWait(t, s, "r1", time.Unix(10, 0))
	res := Resolution{Status: WaitMet, Detail: "mail came", Ended: time.Unix(20, 0), Next: &Queued{ID: "q1", Persona: "ada", Created: time.Unix(20, 0)}}
	ok, err := s.ResolveWait("r1", res)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	ok, err = s.ResolveWait("r1", res)
	if err != nil || ok {
		t.Fatalf("second: ok=%v err=%v", ok, err)
	}
	q, _ := s.Queue()
	if len(q) != 1 || q[0].ID != "q1" {
		t.Fatalf("queue %v", q)
	}
	w, _ := s.WaitFor("r1")
	if w.Status != WaitMet || w.Detail != "mail came" || !w.Ended.Equal(time.Unix(20, 0)) {
		t.Fatalf("wait %+v", w)
	}
	r, _ := s.RunByID("r1")
	if r.State != RunWaiting {
		t.Fatalf("met must not change run state: %s", r.State)
	}
}

func TestResolveWaitStuckMovesRunAndFiles(t *testing.T) {
	s := open(t)
	startWait(t, s, "r1", time.Unix(10, 0))
	ok, err := s.ResolveWait("r1", Resolution{
		Status: WaitStuck, Detail: "no mail", Ended: time.Unix(30, 0),
		Finding: &Bug{ID: "b1", RunID: "r1", Summary: "mail never arrived", Created: time.Unix(30, 0)},
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	r, _ := s.RunByID("r1")
	if r.State != RunStuck {
		t.Fatalf("state %s", r.State)
	}
	bugs, _ := s.Bugs("r1")
	if len(bugs) != 1 || bugs[0].ID != "b1" {
		t.Fatalf("bugs %v", bugs)
	}
	open, err := s.OpenWaits()
	if err != nil || len(open) != 0 {
		t.Fatalf("open %v err %v", open, err)
	}
}

func TestOpenWaitsOldestFirst(t *testing.T) {
	s := open(t)
	startWait(t, s, "late", time.Unix(50, 0))
	startWait(t, s, "early", time.Unix(5, 0))
	got, err := s.OpenWaits()
	if err != nil || len(got) != 2 || got[0].RunID != "early" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestWaitsForMap(t *testing.T) {
	s := open(t)
	startWait(t, s, "r1", time.Unix(10, 0))
	waitRun(t, s, "r2", RunDone)
	m, err := s.WaitsFor([]string{"r1", "r2"})
	if err != nil || len(m) != 1 {
		t.Fatalf("%v %v", m, err)
	}
	if _, ok := m["r1"]; !ok {
		t.Fatal("r1 missing")
	}
	if m, err := s.WaitsFor(nil); err != nil || len(m) != 0 {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := s.WaitFor("r2"); err != ErrNotFound {
		t.Fatalf("err %v", err)
	}
}
