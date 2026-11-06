package store

import (
	"testing"
	"time"
)

func TestPausedRoundTrip(t *testing.T) {
	s := open(t)
	if p, err := s.Paused(); err != nil || p {
		t.Fatalf("default = %v, %v", p, err)
	}
	if err := s.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Paused(); !p {
		t.Fatal("want paused")
	}
	if err := s.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Paused(); p {
		t.Fatal("want resumed")
	}
}

func TestRunsSince(t *testing.T) {
	s := open(t)
	now := time.Now().UTC()
	for id, at := range map[string]time.Time{"old": now.Add(-25 * time.Hour), "new": now.Add(-time.Hour)} {
		if err := s.CreateRun(Run{ID: id, Token: id, Persona: "a", Role: "r", Story: "s", State: RunDone, Started: at}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RunsSince(now.Add(-24 * time.Hour))
	if err != nil || len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("got %v, %v", got, err)
	}
}
