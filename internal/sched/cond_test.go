package sched

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

const yaml = `name: p
sites: ["https://x.test"]
roles:
  - name: patient
    stories:
      - name: book
        goal: book a slot
        handoffs: [{role: clinician, story: approve}]
      - {name: lone, goal: g}
  - name: clinician
    stories:
      - {name: approve, goal: approve it}
`

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func proj(t *testing.T) project.Project {
	t.Helper()
	p, err := project.Load([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var run = store.Run{ID: "r1", Persona: "pat", Role: "patient", Story: "book", Journey: "j1"}

func wantErr(t *testing.T, u Until, want string) {
	t.Helper()
	_, err := ParseWait(u, proj(t), run, now)
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestParseWaitExactlyOne(t *testing.T) {
	msg := "until needs exactly one of email, handoff or time"
	wantErr(t, Until{}, msg)
	wantErr(t, Until{Email: &EmailCond{From: "a"}, Time: &TimeCond{After: "30m"}}, msg)
}

func TestParseWaitEmailNeedsField(t *testing.T) {
	wantErr(t, Until{Email: &EmailCond{}}, "email needs from or subject")
	w, err := ParseWait(Until{Email: &EmailCond{From: "a@x"}}, proj(t), run, now)
	if err != nil || w.Kind != "email" || w.From != "a@x" || w.RunID != "r1" || !w.Began.Equal(now) {
		t.Fatalf("%+v %v", w, err)
	}
}

func TestParseWaitHandoffMustBeDeclared(t *testing.T) {
	wantErr(t, Until{Handoff: &HandoffCond{Role: "nurse", Story: "triage"}},
		`story "book" does not hand off to nurse/triage; it hands off to: clinician/approve`)
	lone := run
	lone.Story = "lone"
	_, err := ParseWait(Until{Handoff: &HandoffCond{Role: "nurse", Story: "triage"}}, proj(t), lone, now)
	if err == nil || err.Error() != `story "lone" does not hand off to nurse/triage; it hands off to nobody` {
		t.Fatal(err)
	}
	w, err := ParseWait(Until{Handoff: &HandoffCond{Role: "clinician", Story: "approve"}}, proj(t), run, now)
	if err != nil || w.Kind != "handoff" || w.Role != "clinician" || w.Story != "approve" {
		t.Fatalf("%+v %v", w, err)
	}
}

func TestParseWaitTimeBounds(t *testing.T) {
	after := "time.after must be a duration from 1m to 168h, such as 30m"
	wantErr(t, Until{Time: &TimeCond{After: "30s"}}, after)
	wantErr(t, Until{Time: &TimeCond{After: "169h"}}, after)
	wantErr(t, Until{Time: &TimeCond{After: "soon"}}, after)
	wantErr(t, Until{Time: &TimeCond{}}, "time needs at or after")
	wantErr(t, Until{Time: &TimeCond{At: "x", After: "30m"}}, "time needs at or after")
	wantErr(t, Until{Time: &TimeCond{At: "tomorrow"}}, "time.at must be RFC3339, such as 2026-11-04T15:00:00Z")
	between := "time.at must be between 1 minute and 7 days from now"
	wantErr(t, Until{Time: &TimeCond{At: now.Add(-time.Hour).Format(time.RFC3339)}}, between)
	wantErr(t, Until{Time: &TimeCond{At: now.Add(8 * 24 * time.Hour).Format(time.RFC3339)}}, between)
	w, err := ParseWait(Until{Time: &TimeCond{After: "30m"}}, proj(t), run, now)
	if err != nil || !w.Deadline.Equal(now.Add(30*time.Minute)) || !w.At.Equal(w.Deadline) || w.Kind != "time" {
		t.Fatalf("%+v %v", w, err)
	}
	at := now.Add(48 * time.Hour)
	w, err = ParseWait(Until{Time: &TimeCond{At: at.Format(time.RFC3339)}}, proj(t), run, now)
	if err != nil || !w.At.Equal(at) {
		t.Fatalf("%+v %v", w, err)
	}
}

func TestParseWaitTimeoutDefaultAndCap(t *testing.T) {
	e := func(to string) Until { return Until{Email: &EmailCond{From: "a"}, Timeout: to} }
	w, err := ParseWait(e(""), proj(t), run, now)
	if err != nil || !w.Deadline.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("%+v %v", w, err)
	}
	w, err = ParseWait(e("8h"), proj(t), run, now)
	if err != nil || !w.Deadline.Equal(now.Add(8*time.Hour)) {
		t.Fatalf("%+v %v", w, err)
	}
	wantErr(t, e("200h"), "timeout must be a duration from 1m to 168h, such as 8h")
	wantErr(t, e("1s"), "timeout must be a duration from 1m to 168h, such as 8h")
	u := Until{Time: &TimeCond{After: "30m"}, Timeout: "1h"}
	w, err = ParseWait(u, proj(t), run, now)
	if err != nil || !w.Deadline.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("%+v %v", w, err)
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		w    store.Wait
		want string
	}{
		{store.Wait{Kind: "email", From: "x", Subject: "y"}, `an email from "x" with "y" in the subject`},
		{store.Wait{Kind: "email", From: "x"}, `an email from "x"`},
		{store.Wait{Kind: "email", Subject: "y"}, `an email with "y" in the subject`},
		{store.Wait{Kind: "handoff", Role: "clinician", Story: "approve"}, `clinician to finish "approve"`},
		{store.Wait{Kind: "time", At: time.Date(2026, 11, 5, 14, 0, 0, 0, time.UTC)}, `14:00 on 5 Nov 2026 (UTC)`},
	}
	for _, c := range cases {
		if got := Describe(c.w); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

func TestMetEmail(t *testing.T) {
	s := openStore(t)
	began := now
	uid := uint32(0)
	add := func(id, persona, from, subj, filtered string, stored time.Time) {
		t.Helper()
		uid++
		_, err := s.AddMessage(store.Message{ID: id, UID: uid, UIDValidity: 1, Persona: persona,
			From: from, Subject: subj, Stored: stored, Received: stored, Filtered: filtered, Mailbox: "m"})
		if err != nil {
			t.Fatal(err)
		}
	}
	add("old", "pat", "clinic@x.test", "Your booking is APPROVED", "", began.Add(-time.Hour))
	w := store.Wait{RunID: "r1", Kind: "email", Subject: "approved", Began: began}
	if _, ok, err := Met(s, w, run, now); err != nil || ok {
		t.Fatalf("old matched: %v %v", ok, err)
	}
	add("other", "someone", "clinic@x.test", "Your booking is APPROVED", "", began.Add(time.Minute))
	add("filt", "pat", "clinic@x.test", "Your booking is APPROVED", "spam", began.Add(2*time.Minute))
	if _, ok, _ := Met(s, w, run, now); ok {
		t.Fatal("other persona or filtered matched")
	}
	add("m1", "pat", "clinic@x.test", "Your booking is APPROVED", "", began.Add(3*time.Minute))
	add("m2", "pat", "clinic@x.test", "Booking approved again", "", began.Add(4*time.Minute))
	a, ok, err := Met(s, w, run, now)
	if err != nil || !ok || a.MessageID != "m1" || a.Detail != `email "Your booking is APPROVED" from clinic@x.test` {
		t.Fatalf("%+v %v %v", a, ok, err)
	}
	both := w
	both.From = "other@x.test"
	if _, ok, _ := Met(s, both, run, now); ok {
		t.Fatal("from must also match")
	}
	both.From = "CLINIC@"
	if _, ok, _ := Met(s, both, run, now); !ok {
		t.Fatal("from+subject should match")
	}
}

func TestMetHandoff(t *testing.T) {
	s := openStore(t)
	began := now
	mk := func(id, journey string, state store.RunState, fin time.Time) {
		t.Helper()
		err := s.CreateRun(store.Run{ID: id, Token: "t" + id, Persona: "doc", Role: "clinician", Story: "approve",
			State: state, Outcome: "approved it\nsecond line", Started: began.Add(-time.Hour), Finished: fin, Journey: journey})
		if err != nil {
			t.Fatal(err)
		}
	}
	w := store.Wait{RunID: "r1", Kind: "handoff", Role: "clinician", Story: "approve", Began: began}
	mk("a", "j2", store.RunDone, began.Add(time.Minute))
	mk("b", "j1", store.RunDone, began.Add(-time.Minute))
	mk("c", "j1", store.RunStuck, began.Add(time.Minute))
	if _, ok, err := Met(s, w, run, now); err != nil || ok {
		t.Fatalf("matched too early: %v %v", ok, err)
	}
	mk("d", "j1", store.RunDone, began.Add(time.Minute))
	a, ok, err := Met(s, w, run, now)
	if err != nil || !ok || a.Detail != `doc finished "approve": approved it` {
		t.Fatalf("%+v %v %v", a, ok, err)
	}
}

func TestMetTime(t *testing.T) {
	at := now.Add(time.Hour)
	w := store.Wait{Kind: "time", At: at}
	if _, ok, _ := Met(nil, w, run, at.Add(-time.Second)); ok {
		t.Fatal("early")
	}
	a, ok, _ := Met(nil, w, run, at)
	if !ok || a.Detail != "time reached" {
		t.Fatalf("%+v %v", a, ok)
	}
}

func TestStuckText(t *testing.T) {
	w := store.Wait{Kind: "handoff", Role: "clinician", Story: "approve", Began: now, Deadline: now.Add(24 * time.Hour)}
	if got := StuckText(w); got != `waited 24h for clinician to finish "approve"; it never happened` {
		t.Fatal(got)
	}
	for d, want := range map[time.Duration]string{30 * time.Minute: "30m", 90 * time.Minute: "1h30m", 168 * time.Hour: "7d"} {
		w.Deadline = now.Add(d)
		if got := StuckText(w); !strings.Contains(got, "waited "+want+" for") {
			t.Errorf("%v: %s", d, got)
		}
	}
}
