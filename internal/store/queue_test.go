package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func reopen(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ids(qs []Queued) []string {
	var out []string
	for _, q := range qs {
		out = append(out, q.ID)
	}
	return out
}

func TestMigrateExistingRunsNotFollowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bp.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE runs (
  id TEXT PRIMARY KEY, token TEXT NOT NULL UNIQUE,
  persona TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT '', story TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL, outcome TEXT NOT NULL DEFAULT '', session TEXT NOT NULL DEFAULT '',
  started INTEGER NOT NULL DEFAULT 0, finished INTEGER NOT NULL DEFAULT 0);
INSERT INTO runs(id, token, state, finished) VALUES('old', 'tok', 'done', 5)`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	for i := 0; i < 2; i++ {
		s := reopen(t, path)
		u, err := s.Unfollowed()
		if err != nil || len(u) != 0 {
			t.Fatalf("open %d: unfollowed %v err %v", i, u, err)
		}
		r, err := s.RunByID("old")
		if err != nil || r.Chain != "old" {
			t.Fatalf("open %d: %+v err %v", i, r, err)
		}
		s.Close()
	}
}

func TestRunChainFieldsRoundTrip(t *testing.T) {
	s := open(t)
	t0 := time.Now().UTC().Truncate(time.Second)
	a := Run{ID: "a", Token: "ta", State: RunDone, Started: t0, Chain: "j1"}
	b := Run{ID: "b", Token: "tb", State: RunPending, Started: t0.Add(time.Second), Chain: "j1",
		Follows: "a", Via: "handoff", Context: []byte(`{"kind":"handoff"}`)}
	c := Run{ID: "c", Token: "tc", State: RunPending, Started: t0, Chain: "j2"}
	for _, r := range []Run{b, a, c} {
		if err := s.CreateRun(r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RunByID("b")
	if err != nil || !reflect.DeepEqual(got, b) {
		t.Fatalf("got %+v err %v want %+v", got, err, b)
	}
	list, err := s.RunsInChain("j1")
	if err != nil || len(list) != 2 || list[0].ID != "a" || list[1].ID != "b" {
		t.Fatalf("chain: %+v err %v", list, err)
	}
}

func TestQueueFIFOAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bp.db")
	s := reopen(t, path)
	at := time.Now().UTC().Truncate(time.Second)
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Enqueue(Queued{ID: id, Created: at, Reason: "r"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Enqueue(Queued{}); err == nil {
		t.Fatal("empty id accepted")
	}
	want := []string{"a", "b", "c"}
	if q, _ := s.Queue(); !reflect.DeepEqual(ids(q), want) {
		t.Fatalf("got %v", ids(q))
	}
	s.Close()
	s = reopen(t, path)
	if q, _ := s.Queue(); !reflect.DeepEqual(ids(q), want) {
		t.Fatalf("after reopen %v", ids(q))
	}
}

func TestCreateRunFromConsumesOnce(t *testing.T) {
	s := open(t)
	if err := s.Enqueue(Queued{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRunFrom(Run{ID: "r1", Token: "t1", State: RunPending}, "a"); err != nil {
		t.Fatal(err)
	}
	if q, _ := s.Queue(); len(q) != 0 {
		t.Fatalf("queue %v", q)
	}
	if err := s.CreateRunFrom(Run{ID: "r2", Token: "t2", State: RunPending}, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v", err)
	}
	if _, err := s.RunByID("r2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("r2 exists: %v", err)
	}
}

func TestFollowUpAtomicAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bp.db")
	s := reopen(t, path)
	if err := s.CreateRun(Run{ID: "r", Token: "t", State: RunDone, Finished: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Unfollowed(); len(u) != 1 {
		t.Fatalf("unfollowed %v", u)
	}
	if err := s.FollowUp("r", []Queued{{ID: "q"}}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = reopen(t, path)
	if u, _ := s.Unfollowed(); len(u) != 0 {
		t.Fatalf("unfollowed %v", u)
	}
	if q, _ := s.Queue(); len(q) != 1 || q[0].ID != "q" {
		t.Fatalf("queue %v", q)
	}
	if err := s.FollowUp("missing", []Queued{{ID: "z"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v", err)
	}
	if q, _ := s.Queue(); len(q) != 1 {
		t.Fatalf("queue %v", q)
	}
}

func TestUnfollowedOnlyDoneAndWaiting(t *testing.T) {
	s := open(t)
	for i, st := range []RunState{RunStuck, RunFailed, RunRunning, RunPending, RunDone, RunWaiting} {
		id := string(st)
		if err := s.CreateRun(Run{ID: id, Token: id, State: st, Finished: time.Unix(int64(10-i), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.Unfollowed()
	if err != nil || len(u) != 2 || u[0].ID != "waiting" || u[1].ID != "done" {
		t.Fatalf("got %+v err %v", u, err)
	}
	for _, st := range []RunState{RunDone, RunStuck, RunFailed, RunWaiting} {
		if !st.Final() {
			t.Fatal(st)
		}
	}
	if RunPending.Final() || RunRunning.Final() {
		t.Fatal("active is final")
	}
}

func TestDeleteQueuedMissing(t *testing.T) {
	if err := open(t).DeleteQueued("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestAddColumnsAtomicAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bp.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE runs (id TEXT PRIMARY KEY, token TEXT NOT NULL UNIQUE, state TEXT NOT NULL);
INSERT INTO runs VALUES('old','t','done')`); err != nil {
		t.Fatal(err)
	}
	added, err := addColumns(db)
	if err != nil || !added {
		t.Fatalf("first: %v %v", added, err)
	}
	var followed int
	var chain string
	if err := db.QueryRow(`SELECT followed, chain FROM runs`).Scan(&followed, &chain); err != nil || followed != 1 || chain != "old" {
		t.Fatalf("%d %q %v", followed, chain, err)
	}
	if added, err := addColumns(db); err != nil || added {
		t.Fatalf("second: %v %v", added, err)
	}
}

func TestAddColumnsAddsWaitsAfterID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bp.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// A waits table from before after_id, with one open wait.
	if _, err := db.Exec(`CREATE TABLE waits (run_id TEXT PRIMARY KEY, kind TEXT NOT NULL DEFAULT '', sender TEXT NOT NULL DEFAULT '',
  subject TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT '', story TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL DEFAULT 0,
  began INTEGER NOT NULL DEFAULT 0, deadline INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '',
  ended INTEGER NOT NULL DEFAULT 0);
INSERT INTO waits(run_id, kind, subject, status) VALUES('w1','email','Hi','open')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, err := s.WaitFor("w1")
	if err != nil || w.Subject != "Hi" || w.AfterID != "" || w.Status != WaitOpen {
		t.Fatalf("%+v %v", w, err)
	}
}

func TestQueueKeepsReplayFields(t *testing.T) {
	s := reopen(t, filepath.Join(t.TempDir(), "bp.db"))
	q := Queued{ID: "q1", Persona: "ann", Role: "buyer", Story: "checkout", Reason: "Scheduled replay",
		Kind: RunReplay, JourneyID: "j1", Revision: 2, Seed: 77, Created: time.Unix(5, 0).UTC()}
	if err := s.Enqueue(q); err != nil {
		t.Fatal(err)
	}
	got, err := s.Queue()
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Kind != RunReplay || got[0].JourneyID != "j1" || got[0].Revision != 2 || got[0].Seed != 77 {
		t.Fatalf("queued %+v", got[0])
	}
}
