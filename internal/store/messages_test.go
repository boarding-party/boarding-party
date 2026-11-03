package store

import (
	"errors"
	"testing"
	"time"
)

func msg(id string, uid uint32, persona, filtered string, stored time.Time) Message {
	return Message{ID: id, UIDValidity: 1, UID: uid, Persona: persona, From: "a@x.test",
		To: []string{persona + "@in.test"}, Subject: "s " + id, Text: "body", Links: []string{"https://x.test/v"},
		Received: stored, Stored: stored, Filtered: filtered}
}

func TestAddMessageDedupes(t *testing.T) {
	s := open(t)
	now := time.Now().UTC()
	if added, err := s.AddMessage(msg("m1", 5, "ada", "", now)); err != nil || !added {
		t.Fatalf("first add: %v %v", added, err)
	}
	if added, err := s.AddMessage(msg("m2", 5, "ada", "", now)); err != nil || added {
		t.Fatalf("dup add: %v %v", added, err)
	}
	got, err := s.Messages(MessageQuery{})
	if err != nil || len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("got %+v %v", got, err)
	}
	if got[0].To[0] != "ada@in.test" || got[0].Links[0] != "https://x.test/v" {
		t.Fatalf("json fields lost: %+v", got[0])
	}
}

func TestMessagesFilters(t *testing.T) {
	s := open(t)
	base := time.Now().UTC().Truncate(time.Second)
	for i, m := range []Message{
		msg("old", 1, "ada", "", base.Add(-48*time.Hour)),
		msg("a1", 2, "ada", "", base.Add(-2*time.Minute)),
		msg("a2", 3, "ada", "", base.Add(-time.Minute)),
		msg("spam", 4, "ada", "unverified", base),
		msg("b1", 5, "bob", "", base),
	} {
		_ = i
		if _, err := s.AddMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(q MessageQuery) []string {
		ms, err := s.Messages(q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range ms {
			out = append(out, m.ID)
		}
		return out
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v want %v", got, want)
			}
		}
	}
	eq(ids(MessageQuery{Persona: "ada"}), "a2", "a1", "old")
	eq(ids(MessageQuery{Persona: "ada", Filtered: true}), "spam")
	eq(ids(MessageQuery{Persona: "ada", Since: base.Add(-time.Hour)}), "a2", "a1")
	eq(ids(MessageQuery{Persona: "ada", Limit: 1}), "a2")
	if n := len(ids(MessageQuery{})); n != 4 {
		t.Fatalf("all unfiltered = %d", n)
	}
}

func TestMessageNotFound(t *testing.T) {
	s := open(t)
	if _, err := s.Message("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	s.AddMessage(msg("m1", 1, "ada", "", time.Now()))
	if m, err := s.Message("m1"); err != nil || m.Persona != "ada" {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestMailCursorRoundTrip(t *testing.T) {
	s := open(t)
	if _, _, ok, err := s.MailCursor(); err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if err := s.SetMailCursor(7, 42); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMailCursor(8, 43); err != nil {
		t.Fatal(err)
	}
	v, l, ok, err := s.MailCursor()
	if err != nil || !ok || v != 8 || l != 43 {
		t.Fatalf("%d %d %v %v", v, l, ok, err)
	}
}

func TestPruneMessages(t *testing.T) {
	s := open(t)
	now := time.Now().UTC()
	s.AddMessage(msg("old", 1, "ada", "", now.Add(-72*time.Hour)))
	s.AddMessage(msg("new", 2, "ada", "", now))
	n, err := s.PruneMessages(now.Add(-24 * time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := s.Message("old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old survived: %v", err)
	}
}
