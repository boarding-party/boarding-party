package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestUnfiredMessages(t *testing.T) {
	s := open(t)
	base := time.Now().UTC().Truncate(time.Second)
	for i, m := range []Message{
		msg("old", 1, "ada", "", base.Add(-48*time.Hour)),
		msg("filt", 2, "ada", "spam", base.Add(-3*time.Minute)),
		msg("anon", 3, "", "", base.Add(-2*time.Minute)),
		msg("b", 4, "ada", "", base.Add(-time.Minute)),
		msg("a", 5, "bo", "", base.Add(-4*time.Minute)),
	} {
		if _, err := s.AddMessage(m); err != nil {
			t.Fatal(i, err)
		}
	}
	got, err := s.UnfiredMessages(base.Add(-time.Hour), 5, 10)
	if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("got %+v %v", got, err)
	}
	if err := s.MarkMessageFired("a", "signup", base); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMessageFired("a", "other", base); err != nil {
		t.Fatal(err)
	}
	got, _ = s.UnfiredMessages(base.Add(-time.Hour), 5, 10)
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("after mark: %+v", got)
	}
	if got, _ = s.UnfiredMessages(base.Add(-time.Hour), 5, 0); len(got) != 0 {
		t.Fatalf("limit 0 should return nothing: %+v", got)
	}
	fired, err := s.MessageFired([]string{"a", "b"})
	if err != nil || len(fired) != 1 || fired["a"] != "signup" {
		t.Fatalf("fired = %v %v", fired, err)
	}
	if fired, err = s.MessageFired(nil); err != nil || len(fired) != 0 {
		t.Fatalf("empty: %v %v", fired, err)
	}
}

// One persona's backlog does not hide another persona's mail.
func TestUnfiredMessagesPerPersona(t *testing.T) {
	s := open(t)
	base := time.Now().UTC().Truncate(time.Second)
	for i := range 8 {
		if _, err := s.AddMessage(msg(fmt.Sprintf("ada%d", i), uint32(i+1), "ada", "", base.Add(time.Duration(i-20)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddMessage(msg("bo0", 100, "bo", "", base.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	got, err := s.UnfiredMessages(base.Add(-time.Hour), 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	// Each persona's oldest first, then each one's second, and so on.
	if want := "ada0 bo0 ada1 ada2"; strings.Join(ids, " ") != want {
		t.Fatalf("got %v, want %s", ids, want)
	}
}
