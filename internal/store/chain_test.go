package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A database from before the rename keeps its chains: the columns move
// from journey to chain, data intact, and the director opens it.
func TestOpenRenamesJourneyToChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bp.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE runs (
  id TEXT PRIMARY KEY, token TEXT NOT NULL UNIQUE, persona TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT '',
  story TEXT NOT NULL DEFAULT '', state TEXT NOT NULL, outcome TEXT NOT NULL DEFAULT '', session TEXT NOT NULL DEFAULT '',
  started INTEGER NOT NULL DEFAULT 0, finished INTEGER NOT NULL DEFAULT 0, journey TEXT NOT NULL DEFAULT '',
  follows TEXT NOT NULL DEFAULT '', via TEXT NOT NULL DEFAULT '', context TEXT NOT NULL DEFAULT '',
  followed INTEGER NOT NULL DEFAULT 0);
CREATE INDEX runs_journey ON runs(journey);
CREATE TABLE queue (id TEXT PRIMARY KEY, persona TEXT NOT NULL DEFAULT '', role TEXT NOT NULL DEFAULT '',
  story TEXT NOT NULL DEFAULT '', journey TEXT NOT NULL DEFAULT '', follows TEXT NOT NULL DEFAULT '',
  via TEXT NOT NULL DEFAULT '', context TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL DEFAULT 0);
CREATE TABLE issues (id TEXT PRIMARY KEY, summary TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL DEFAULT 0, updated INTEGER NOT NULL DEFAULT 0, reopened INTEGER NOT NULL DEFAULT 0,
  check_journey TEXT NOT NULL DEFAULT '', check_note TEXT NOT NULL DEFAULT '');
INSERT INTO runs(id, token, state, journey, followed) VALUES('r1', 't1', 'done', 'j1', 1);
INSERT INTO queue(id, journey) VALUES('q1', 'j1');
INSERT INTO issues(id, status, check_journey) VALUES('i1', 'verifying', 'j1');`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.RunByID("r1")
	if err != nil || r.Chain != "j1" {
		t.Fatalf("run chain %q, %v", r.Chain, err)
	}
	qs, err := s.Queue()
	if err != nil || len(qs) != 1 || qs[0].Chain != "j1" {
		t.Fatalf("queue %+v, %v", qs, err)
	}
	i, err := s.Issue("i1")
	if err != nil || i.CheckChain != "j1" {
		t.Fatalf("issue check chain %q, %v", i.CheckChain, err)
	}
	for table, gone := range map[string]string{"runs": "journey", "queue": "journey", "issues": "check_journey"} {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, gone).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s still has %s (%d, %v)", table, gone, n, err)
		}
	}
	var idx int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='runs_chain'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatalf("index runs_chain: %d %v", idx, err)
	}
}
