package store

import (
	"errors"
	"testing"
	"time"
)

func TestDues(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 11, 4, 13, 0, 0, 0, time.UTC)
	for _, d := range []Due{{Persona: "Ada", Activity: "every 1 h", At: at, NextStory: 1}, {Persona: "Bo", Activity: "daily at 09:00", At: at}} {
		if err := s.SetDue(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetDue(Due{Persona: "Ada", Activity: "every 2 h", At: at.Add(time.Hour), NextStory: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Dues()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["Ada"].Activity != "every 2 h" || !got["Ada"].At.Equal(at.Add(time.Hour)) || got["Ada"].NextStory != 2 || got["Ada"].Persona != "Ada" {
		t.Fatalf("dues = %+v", got)
	}
	if err := s.DeleteDuesExcept([]string{"Bo"}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Dues(); len(got) != 1 || got["Bo"].Persona != "Bo" {
		t.Fatalf("after delete: %+v", got)
	}
	if err := s.DeleteDuesExcept(nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Dues(); len(got) != 0 {
		t.Fatalf("after delete all: %+v", got)
	}
}

func TestLastRun(t *testing.T) {
	s := open(t)
	if _, err := s.LastRun("Ada", "book"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	t0 := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	for i, r := range []Run{
		{ID: "a", Token: "ta", Persona: "Ada", Role: "patient", Story: "book", State: RunDone, Started: t0},
		{ID: "b", Token: "tb", Persona: "Ada", Role: "patient", Story: "book", State: RunStuck, Started: t0.Add(time.Hour)},
		{ID: "c", Token: "tc", Persona: "Ada", Role: "patient", Story: "cancel", State: RunDone, Started: t0.Add(2 * time.Hour)},
		{ID: "d", Token: "td", Persona: "Bo", Role: "patient", Story: "book", State: RunDone, Started: t0.Add(3 * time.Hour)},
	} {
		if err := s.CreateRun(r); err != nil {
			t.Fatal(i, err)
		}
	}
	r, err := s.LastRun("Ada", "book")
	if err != nil || r.ID != "b" {
		t.Fatalf("run = %+v err %v", r, err)
	}
}
