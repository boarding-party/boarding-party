package store

import (
	"database/sql"
	"errors"
	"path/filepath"
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

func TestClearMailCursor(t *testing.T) {
	s := open(t)
	if err := s.ClearMailCursor(); err != nil { // nothing to clear is fine
		t.Fatal(err)
	}
	s.SetMailCursor(7, 42)
	if err := s.ClearMailCursor(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := s.MailCursor(); err != nil || ok {
		t.Fatalf("ok=%v err=%v, want cleared", ok, err)
	}
}

func TestDedupeIncludesMailbox(t *testing.T) {
	s := open(t)
	now := time.Now().UTC()
	a, b := msg("a", 5, "ada", "", now), msg("b", 5, "ada", "", now)
	a.Mailbox, b.Mailbox = "imap.a.test|u", "imap.b.test|u"
	for _, m := range []Message{a, b} {
		if added, err := s.AddMessage(m); err != nil || !added {
			t.Fatalf("%s: added=%v err=%v", m.ID, added, err)
		}
	}
	dup := msg("c", 5, "ada", "", now)
	dup.Mailbox = a.Mailbox
	if added, err := s.AddMessage(dup); err != nil || added {
		t.Fatalf("same mailbox dup: added=%v err=%v", added, err)
	}
}

func TestOpenMigratesMessagesWithoutMailbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE messages (
  id TEXT PRIMARY KEY, uidvalidity INTEGER NOT NULL DEFAULT 0, uid INTEGER NOT NULL DEFAULT 0,
  persona TEXT NOT NULL DEFAULT '', sender TEXT NOT NULL DEFAULT '', recipients TEXT NOT NULL DEFAULT '[]',
  subject TEXT NOT NULL DEFAULT '', body TEXT NOT NULL DEFAULT '', links TEXT NOT NULL DEFAULT '[]',
  received INTEGER NOT NULL DEFAULT 0, stored INTEGER NOT NULL DEFAULT 0, filtered TEXT NOT NULL DEFAULT '',
  UNIQUE(uidvalidity, uid));
CREATE INDEX messages_persona_received ON messages(persona, received);
INSERT INTO messages(id, uidvalidity, uid, persona, subject, stored) VALUES('old', 1, 5, 'ada', 'kept', 100);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err := s.Message("old")
	if err != nil || m.Subject != "kept" || m.UID != 5 {
		t.Fatalf("old row lost: %+v %v", m, err)
	}
	// Unique key is now per mailbox: the same ids from a named mailbox are new; from '' they are duplicates.
	n := msg("new", 5, "ada", "", time.Now())
	n.Mailbox = "h|u"
	if added, err := s.AddMessage(n); err != nil || !added {
		t.Fatalf("new mailbox: %v %v", added, err)
	}
	d := msg("dup", 5, "ada", "", time.Now())
	if added, err := s.AddMessage(d); err != nil || added {
		t.Fatalf("legacy mailbox dup: %v %v", added, err)
	}
	s.Close()
	s2, err := Open(path) // second open: nothing to migrate
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}

func TestPruneMessagesDropsTheirFiredRows(t *testing.T) {
	s := open(t)
	now := time.Now().UTC()
	s.AddMessage(msg("old", 1, "ada", "", now.Add(-72*time.Hour)))
	s.AddMessage(msg("new", 2, "ada", "", now))
	for _, id := range []string{"old", "new", "never-stored"} {
		if err := s.MarkMessageFired(id, "", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.PruneMessages(now.Add(-24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	fired, err := s.MessageFired([]string{"old", "new", "never-stored"})
	if err != nil || len(fired) != 1 {
		t.Fatalf("fired = %v %v", fired, err)
	}
	if _, ok := fired["new"]; !ok {
		t.Fatalf("fired = %v", fired)
	}
}
