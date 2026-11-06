package store

import (
	"testing"
	"time"
)

func TestEventsUpsertRefires(t *testing.T) {
	s := open(t)
	ten := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	e := Event{Persona: "ada", UID: "u1", Title: "Standup", Start: ten, End: ten.Add(time.Hour), Location: "Room", URL: "https://x.test", MessageID: "m1"}
	if err := s.UpsertEvent(e); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueEvents(ten.Add(-time.Minute), 2*time.Minute)
	if err != nil || len(due) != 1 || due[0].Title != "Standup" || due[0].Location != "Room" || !due[0].End.Equal(ten.Add(time.Hour)) {
		t.Fatalf("due = %+v %v", due, err)
	}
	if err := s.MarkEventFired("ada", "u1", ten, "s"); err != nil {
		t.Fatal(err)
	}
	if due, _ = s.DueEvents(ten.Add(-time.Minute), 2*time.Minute); len(due) != 0 {
		t.Fatalf("fired event still due: %+v", due)
	}
	if err := s.UpsertEvent(e); err != nil { // unchanged re-sync keeps fired state
		t.Fatal(err)
	}
	if due, _ = s.DueEvents(ten.Add(-time.Minute), 2*time.Minute); len(due) != 0 {
		t.Fatalf("re-upsert refired: %+v", due)
	}
	e.Start, e.Title = ten.Add(time.Hour), "Moved"
	if err := s.UpsertEvent(e); err != nil {
		t.Fatal(err)
	}
	due, _ = s.DueEvents(ten.Add(59*time.Minute), 2*time.Minute)
	if len(due) != 1 || due[0].Title != "Moved" || !due[0].FiredStart.Equal(ten) || due[0].FiredStory != "s" {
		t.Fatalf("moved event: %+v", due)
	}
	got, err := s.Events("ada", ten, ten.Add(3*time.Hour))
	if err != nil || len(got) != 1 {
		t.Fatalf("events = %+v %v", got, err)
	}
	if got, _ = s.Events("bo", ten, ten.Add(3*time.Hour)); len(got) != 0 {
		t.Fatalf("other persona: %+v", got)
	}
	if got, _ = s.Events("", ten, ten.Add(3*time.Hour)); len(got) != 1 {
		t.Fatalf("all personas: %+v", got)
	}
	if err := s.DeleteEvent("ada", "u1"); err != nil {
		t.Fatal(err)
	}
	if due, _ = s.DueEvents(ten.Add(59*time.Minute), 2*time.Minute); len(due) != 0 {
		t.Fatalf("after delete: %+v", due)
	}
}

func TestDueEventsSkipsStale(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if err := s.UpsertEvent(Event{Persona: "ada", UID: "old", Title: "Gone", Start: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEvent(Event{Persona: "ada", UID: "late", Title: "Late", Start: now.Add(-30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueEvents(now, 2*time.Minute)
	if err != nil || len(due) != 1 || due[0].UID != "late" {
		t.Fatalf("due = %+v %v", due, err)
	}
}
