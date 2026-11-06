package inbox

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func london(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func ics(method string, event ...string) []byte {
	lines := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//t//t//EN"}
	if method != "" {
		lines = append(lines, "METHOD:"+method)
	}
	lines = append(lines, "BEGIN:VEVENT")
	lines = append(lines, event...)
	lines = append(lines, "END:VEVENT", "END:VCALENDAR", "")
	return []byte(strings.Join(lines, "\r\n"))
}

func TestParseICSRequest(t *testing.T) {
	evs, err := ParseICS(ics("REQUEST",
		"UID:appt-1@x.test",
		"DTSTART;TZID=Europe/London:20261110T143000",
		"DTEND;TZID=Europe/London:20261110T150000",
		"SUMMARY:Video appointment\\, Dr",
		"  Lee",
		"LOCATION:Online",
		"URL:https://x.test/a/1",
	), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	e := evs[0]
	if e.UID != "appt-1@x.test" || e.Title != "Video appointment, Dr Lee" || e.Location != "Online" || e.URL != "https://x.test/a/1" || e.Cancel {
		t.Errorf("event = %+v", e)
	}
	if want := time.Date(2026, 11, 10, 14, 30, 0, 0, time.UTC); !e.Start.Equal(want) {
		t.Errorf("start = %v, want %v", e.Start, want)
	}
	if want := time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC); !e.End.Equal(want) {
		t.Errorf("end = %v, want %v", e.End, want)
	}
}

func TestParseICSMethodAbsentAndPublish(t *testing.T) {
	for _, m := range []string{"", "PUBLISH"} {
		evs, err := ParseICS(ics(m, "UID:u", "DTSTART:20261110T143000Z", "SUMMARY:s"), time.UTC)
		if err != nil || len(evs) != 1 || evs[0].Cancel {
			t.Errorf("method %q: %+v, %v", m, evs, err)
		}
	}
	evs, err := ParseICS(ics("REPLY", "UID:u", "DTSTART:20261110T143000Z"), time.UTC)
	if err != nil || len(evs) != 0 {
		t.Errorf("REPLY: %+v, %v", evs, err)
	}
}

func TestParseICSCancel(t *testing.T) {
	evs, err := ParseICS(ics("CANCEL", "UID:appt-1@x.test", "DTSTART:20261110T143000Z", "STATUS:CANCELLED"), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || !evs[0].Cancel || evs[0].UID != "appt-1@x.test" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestParseICSStatusCancelled(t *testing.T) {
	for _, m := range []string{"REQUEST", "PUBLISH"} {
		evs, err := ParseICS(ics(m, "UID:u", "DTSTART:20261110T143000Z", "STATUS:CANCELLED"), time.UTC)
		if err != nil || len(evs) != 1 || !evs[0].Cancel || evs[0].UID != "u" {
			t.Errorf("%s: %+v, %v", m, evs, err)
		}
	}
}

// hostile is input that makes go-ical panic or fail mid-way.
var hostile = map[string][]byte{
	"quoted param": ics("REQUEST", "UID:u", `DTSTART;TZID="Europe/London"x:20261110T143000`),
	"truncated":    []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:u\r\nDTSTART:20261110T143000Z\r\n"),
}

func TestParseICSHostileInput(t *testing.T) {
	for name, b := range hostile {
		evs, err := ParseICS(b, time.UTC)
		if err == nil || len(evs) != 0 {
			t.Errorf("%s: %+v, %v; want an error and no events", name, evs, err)
		}
	}
}

func TestParseICSCap(t *testing.T) {
	lines := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "METHOD:PUBLISH"}
	for i := range 30 {
		lines = append(lines, "BEGIN:VEVENT", "UID:u"+strconv.Itoa(i), "DTSTART:20261110T143000Z", "END:VEVENT")
	}
	lines = append(lines, "END:VCALENDAR", "")
	evs, err := ParseICS([]byte(strings.Join(lines, "\r\n")), time.UTC)
	if err != nil || len(evs) != MaxEvents {
		t.Fatalf("events = %d, %v; want %d", len(evs), err, MaxEvents)
	}
}

func TestCalendarEventsSkipsBadPart(t *testing.T) {
	good := ics("REQUEST", "UID:good", "DTSTART:20261110T143000Z")
	m := Message{calendar: [][]byte{hostile["quoted param"], hostile["truncated"], good}}
	evs := m.CalendarEvents(time.UTC)
	if len(evs) != 1 || evs[0].UID != "good" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestParseICSUnknownTZIDAndFloating(t *testing.T) {
	loc := london(t)
	want := time.Date(2026, 11, 10, 9, 0, 0, 0, loc)
	for _, start := range []string{"DTSTART;TZID=Made/Up:20261110T090000", "DTSTART:20261110T090000",
		"DTSTART;TZID=Local:20261110T090000", `DTSTART;TZID=" ":20261110T090000`} {
		evs, err := ParseICS(ics("REQUEST", "UID:u", start), loc)
		if err != nil || len(evs) != 1 {
			t.Fatalf("%s: %+v, %v", start, evs, err)
		}
		if !evs[0].Start.Equal(want) {
			t.Errorf("%s: start = %v, want %v", start, evs[0].Start, want)
		}
	}
}

func TestParseICSAllDay(t *testing.T) {
	loc := london(t)
	evs, err := ParseICS(ics("", "UID:u", "DTSTART;VALUE=DATE:20261110", "DTEND;VALUE=DATE:20261111"), loc)
	if err != nil || len(evs) != 1 {
		t.Fatalf("%+v, %v", evs, err)
	}
	if want := time.Date(2026, 11, 10, 9, 0, 0, 0, loc); !evs[0].Start.Equal(want) {
		t.Errorf("start = %v, want %v", evs[0].Start, want)
	}
}

func TestParseICSSkipsBadEvents(t *testing.T) {
	evs, err := ParseICS(ics("REQUEST", "SUMMARY:no uid", "DTSTART:20261110T143000Z"), time.UTC)
	if err != nil || len(evs) != 0 {
		t.Errorf("no uid: %+v, %v", evs, err)
	}
	evs, err = ParseICS(ics("REQUEST", "UID:u", "DTSTART:garbage"), time.UTC)
	if err != nil || len(evs) != 0 {
		t.Errorf("bad start: %+v, %v", evs, err)
	}
	evs, err = ParseICS(ics("REQUEST", "UID:u", "DTSTART:20261110T143000Z", "URL:javascript:alert(1)"), time.UTC)
	if err != nil || len(evs) != 1 || evs[0].URL != "" {
		t.Errorf("non-http url: %+v, %v", evs, err)
	}
	if _, err := ParseICS([]byte("not a calendar"), time.UTC); err == nil {
		t.Error("garbage: no error")
	}
}

func TestGoogleLinkEvent(t *testing.T) {
	link := "https://calendar.google.com/calendar/render?action=TEMPLATE&text=Video+appointment&dates=20261110T143000Z/20261110T150000Z&location=Online"
	e, ok := GoogleLinkEvent(link, time.UTC)
	if !ok {
		t.Fatal("not recognised")
	}
	if e.Title != "Video appointment" || e.Location != "Online" || !strings.HasPrefix(e.UID, "gcal:") || len(e.UID) != len("gcal:")+16 {
		t.Errorf("event = %+v", e)
	}
	if !e.Start.Equal(time.Date(2026, 11, 10, 14, 30, 0, 0, time.UTC)) || !e.End.Equal(time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("times = %v - %v", e.Start, e.End)
	}
	if again, _ := GoogleLinkEvent(link, time.UTC); again.UID != e.UID {
		t.Errorf("uid not stable: %q vs %q", again.UID, e.UID)
	}

	loc := london(t)
	e, ok = GoogleLinkEvent("https://calendar.google.com/calendar/event?action=TEMPLATE&text=Day&dates=20261110/20261111", loc)
	if !ok || !e.Start.Equal(time.Date(2026, 11, 10, 9, 0, 0, 0, loc)) {
		t.Errorf("all-day event link: %+v, %v", e, ok)
	}
	e, ok = GoogleLinkEvent("https://calendar.google.com/calendar/render?action=TEMPLATE&text=T&dates=20261110T090000/20261110T100000&ctz=America/New_York", loc)
	ny, _ := time.LoadLocation("America/New_York")
	if !ok || !e.Start.Equal(time.Date(2026, 11, 10, 9, 0, 0, 0, ny)) {
		t.Errorf("ctz link: %+v, %v", e, ok)
	}

	for _, bad := range []string{
		"https://smesolutions.uk/verify?t=1",
		"https://calendar.google.com/calendar/render?action=VIEW&eid=abc",
		"https://calendar.google.com/calendar/render?action=TEMPLATE&text=no+dates",
		"https://evil.test/calendar/render?action=TEMPLATE&text=x&dates=20261110T143000Z/20261110T150000Z",
	} {
		if _, ok := GoogleLinkEvent(bad, time.UTC); ok {
			t.Errorf("%s: recognised", bad)
		}
	}
}

func TestMessageCalendarEvents(t *testing.T) {
	m, err := Parse(fixture(t, "invite-request.eml"))
	if err != nil {
		t.Fatal(err)
	}
	evs := m.CalendarEvents(london(t))
	if len(evs) != 1 || evs[0].UID != "appt-42@smesolutions.uk" || evs[0].Title != "Video appointment, Dr Lee" {
		t.Fatalf("invite (inline + attachment, one UID): %+v", evs)
	}
	if !strings.Contains(m.Text, "video appointment") {
		t.Errorf("text = %q", m.Text)
	}

	m, err = Parse(fixture(t, "gcal-link.eml"))
	if err != nil {
		t.Fatal(err)
	}
	evs = m.CalendarEvents(london(t))
	if len(evs) != 1 || evs[0].Title != "Video appointment" || !strings.HasPrefix(evs[0].UID, "gcal:") {
		t.Fatalf("gcal link: %+v", evs)
	}

	m, err = Parse(fixture(t, "plain.eml"))
	if err != nil {
		t.Fatal(err)
	}
	if evs := m.CalendarEvents(time.UTC); len(evs) != 0 {
		t.Fatalf("plain: %+v", evs)
	}
}

func TestMessageCalendarEventsCap(t *testing.T) {
	var links []string
	for i := range 30 {
		links = append(links, "https://calendar.google.com/calendar/render?action=TEMPLATE&text=e"+string(rune('a'+i%26))+strings.Repeat("x", i)+"&dates=20261110T143000Z/20261110T150000Z")
	}
	m := Message{Links: links}
	if evs := m.CalendarEvents(time.UTC); len(evs) != MaxEvents {
		t.Fatalf("events = %d, want %d", len(evs), MaxEvents)
	}
}
