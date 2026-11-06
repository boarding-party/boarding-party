package sched

import (
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

const calendarYAML = `name: p
sites: ["https://x.test"]
roles:
  - name: patient
    personas: [{name: Ada}]
    stories:
      - {name: lone, goal: g}
      - name: visit
        goal: join the video appointment
        trigger: {calendar: {title: appointment, lead: 5m}}
      - name: prepare
        goal: get ready for the clinic
        trigger: {calendar: {title: clinic, lead: 30m}}
`

func newCalFix(t *testing.T) *fix {
	t.Helper()
	f := newFix(t, calendarYAML)
	f.s.Sources = []Source{&Calendar{Store: f.st}}
	return f
}

// at is h:m on the fixture's day.
func at(h, m int) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.UTC)
}

func (f *fix) setClock(t time.Time) {
	f.clk.Add(t.Sub(f.clk.Now()))
}

func (f *fix) event(persona, uid, title string, start time.Time) {
	f.t.Helper()
	if err := f.st.UpsertEvent(store.Event{Persona: persona, UID: uid, Title: title, Start: start, End: start.Add(30 * time.Minute),
		Location: "Room 2", URL: "https://meet.x.test/r"}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fix) dueEvents() []store.Event {
	f.t.Helper()
	es, err := f.st.DueEvents(f.clk.Now(), 24*time.Hour)
	if err != nil {
		f.t.Fatal(err)
	}
	return es
}

func TestCalendarFiresAtLead(t *testing.T) {
	f := newCalFix(t)
	f.event("Ada", "e1", "Video appointment", at(10, 0))
	f.setClock(at(9, 54))
	f.fire()
	if len(f.queue()) != 0 {
		t.Fatalf("09:54 queue = %+v", f.queue())
	}
	f.setClock(at(9, 55))
	f.fire()
	q := f.queue()
	if len(q) != 1 || q[0].Persona != "Ada" || q[0].Role != "patient" || q[0].Story != "visit" || q[0].Reason != "Calendar: Video appointment" {
		t.Fatalf("queue = %+v", q)
	}
	c := ctxOf(t, q[0])
	if c.Kind != "calendar" || c.Event == nil || c.Event.Title != "Video appointment" || !c.Event.Start.Equal(at(10, 0)) ||
		c.Event.Location != "Room 2" || c.Event.URL != "https://meet.x.test/r" {
		t.Fatalf("context = %+v %+v", c, c.Event)
	}
	if err := f.st.DeleteQueued(q[0].ID); err != nil {
		t.Fatal(err)
	}
	f.setClock(at(9, 56))
	f.fire()
	if len(f.queue()) != 0 {
		t.Fatalf("09:56 queue = %+v", f.queue())
	}
}

func TestCalendarRescheduledFiresAgain(t *testing.T) {
	f := newCalFix(t)
	f.event("Ada", "e1", "Video appointment", at(10, 0))
	f.setClock(at(9, 55))
	f.fire()
	q := f.queue()
	if len(q) != 1 {
		t.Fatalf("queue = %+v", q)
	}
	if err := f.st.DeleteQueued(q[0].ID); err != nil {
		t.Fatal(err)
	}
	f.event("Ada", "e1", "Video appointment", at(11, 0))
	f.setClock(at(10, 58))
	f.fire()
	if q := f.queue(); len(q) != 1 || q[0].Story != "visit" || !ctxOf(t, q[0]).Event.Start.Equal(at(11, 0)) {
		t.Fatalf("queue = %+v", q)
	}
}

func TestCalendarUnmatchedTitleMarked(t *testing.T) {
	f := newCalFix(t)
	f.event("Ada", "e1", "Team lunch", f.clk.Now().Add(time.Minute))
	f.fire()
	if len(f.queue()) != 0 {
		t.Fatalf("queue = %+v", f.queue())
	}
	if es := f.dueEvents(); len(es) != 0 {
		t.Fatalf("still due: %+v", es)
	}
}

func TestCalendarUnknownPersonaMarked(t *testing.T) {
	f := newCalFix(t)
	f.event("Gone", "e1", "Video appointment", f.clk.Now().Add(time.Minute))
	f.fire()
	if len(f.queue()) != 0 || len(f.dueEvents()) != 0 {
		t.Fatalf("queue = %+v due = %+v", f.queue(), f.dueEvents())
	}
}

// An event inside the longest lead but not its own story's lead stays unfired.
func TestCalendarWaitsForItsOwnLead(t *testing.T) {
	f := newCalFix(t)
	f.event("Ada", "e1", "Video appointment", at(13, 0))
	f.setClock(at(12, 40))
	f.fire()
	if len(f.queue()) != 0 || len(f.dueEvents()) != 1 {
		t.Fatalf("12:40 queue = %+v due = %+v", f.queue(), f.dueEvents())
	}
	f.setClock(at(12, 55))
	f.fire()
	if q := f.queue(); len(q) != 1 || q[0].Story != "visit" {
		t.Fatalf("queue = %+v", q)
	}
}

func TestCalendarSkippedWhenBusyIsNotReplayed(t *testing.T) {
	f := newCalFix(t)
	f.running("r1", "Ada", "patient", "lone", "j1")
	f.event("Ada", "e1", "Clinic visit", f.clk.Now().Add(10*time.Minute))
	f.fire()
	if len(f.queue()) != 0 || len(f.dueEvents()) != 0 {
		t.Fatalf("queue = %+v due = %+v", f.queue(), f.dueEvents())
	}
}

func TestCalendarSettleWithoutEventErrors(t *testing.T) {
	f := newCalFix(t)
	if err := (&Calendar{Store: f.st}).Settle(Fire{Persona: "Ada", Context: []byte(`{"kind":"calendar"}`)}, "visit", now); err == nil {
		t.Fatal("want error")
	}
}

func TestCalendarReasonIsClipped(t *testing.T) {
	f := newCalFix(t)
	f.event("Ada", "e1", "Video appointment\x07 "+strings.Repeat("x", 100), f.clk.Now().Add(time.Minute))
	f.fire()
	q := f.queue()
	if len(q) != 1 || q[0].Reason != "Calendar: Video appointment  "+strings.Repeat("x", 61) {
		t.Fatalf("queue = %+v", q)
	}
}
