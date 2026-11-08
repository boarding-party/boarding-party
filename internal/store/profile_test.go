package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPersonaProfileRoundTrip(t *testing.T) {
	s := open(t)
	p, _, err := s.PersonaProfile("Ada")
	if err != nil || p != nil {
		t.Fatalf("want nil, got %s %v", p, err)
	}
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	if err := s.SavePersonaProfile("Ada", json.RawMessage(`{"cookies":[],"origins":[]}`), now); err != nil {
		t.Fatal(err)
	}
	want := `{"cookies":[{"name":"sid"}],"origins":[]}`
	if err := s.SavePersonaProfile("Ada", json.RawMessage(want), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	p, saved, err := s.PersonaProfile("Ada")
	if err != nil || string(p) != want || !saved.Equal(now.Add(time.Hour)) {
		t.Fatalf("got %s %v %v", p, saved, err)
	}
	if err := s.DeletePersonaProfile("Ada"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePersonaProfile("Ada"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if p, _, _ := s.PersonaProfile("Ada"); p != nil {
		t.Fatalf("after delete got %s", p)
	}
}
