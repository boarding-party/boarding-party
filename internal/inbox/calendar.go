package inbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// Event is a calendar entry found in a message: an .ics invite or an add-to-calendar link.
// Cancel events carry only the UID to remove.
type Event struct {
	UID, Title    string
	Start, End    time.Time
	Location, URL string
	Cancel        bool
}

// Limits on the calendar data one message carries.
const (
	MaxEvents = 20

	maxField = 1 << 10 // bytes kept of a title, location or URL
)

// allDayHour is when an all-day event starts, in the persona's local time.
const allDayHour = 9

// ParseICS reads an iCalendar object. METHOD absent, PUBLISH or REQUEST yield events to add or update;
// CANCEL yields Cancel events; any other method yields none. Events without a UID or a readable
// DTSTART are skipped; a VEVENT with STATUS:CANCELLED is a Cancel event whatever the method.
// Floating and all-day times, and unknown TZIDs, use loc. At most MaxEvents are returned.
func ParseICS(b []byte, loc *time.Location) (out []Event, err error) {
	defer func() { // go-ical panics on some malformed input; one bad invite must not crash the poller
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("ical: %v", r)
		}
	}()
	cal, err := ical.NewDecoder(bytes.NewReader(b)).Decode()
	if err != nil {
		return nil, err
	}
	method := ""
	if p := cal.Props.Get(ical.PropMethod); p != nil {
		method = strings.ToUpper(strings.TrimSpace(p.Value))
	}
	cancel := method == "CANCEL"
	if !cancel && method != "" && method != "PUBLISH" && method != "REQUEST" {
		return nil, nil
	}
	for _, ve := range cal.Events() {
		if len(out) >= MaxEvents {
			break
		}
		uid := text(ve.Props, ical.PropUID)
		if uid == "" {
			continue
		}
		if cancel || strings.EqualFold(text(ve.Props, ical.PropStatus), "CANCELLED") {
			out = append(out, Event{UID: uid, Cancel: true})
			continue
		}
		start, ok := icsTime(ve.Props.Get(ical.PropDateTimeStart), loc)
		if !ok {
			continue
		}
		end, ok := icsTime(ve.Props.Get(ical.PropDateTimeEnd), loc)
		if !ok || end.Before(start) {
			end = start
		}
		out = append(out, Event{
			UID:      uid,
			Title:    text(ve.Props, ical.PropSummary),
			Start:    start,
			End:      end,
			Location: text(ve.Props, ical.PropLocation),
			URL:      icsURL(ve.Props.Get(ical.PropURL)),
		})
	}
	return out, nil
}

func text(props ical.Props, name string) string {
	s, err := props.Text(name)
	if err != nil {
		return ""
	}
	return truncate(strings.TrimSpace(s), maxField)
}

// icsTime reads a DATE or DATE-TIME property. A trailing Z is UTC; a TZID that time.LoadLocation
// knows is used; otherwise the time is in loc. Dates start at allDayHour.
func icsTime(p *ical.Prop, loc *time.Location) (time.Time, bool) {
	if p == nil {
		return time.Time{}, false
	}
	v := strings.TrimSpace(p.Value)
	if p.ValueType() == ical.ValueDate || len(v) == len("20060102") {
		return parseDate(v, loc)
	}
	// "Local" would mean the director's own zone, not the sender's: treat it as unknown.
	if tzid := strings.TrimSpace(strings.Trim(p.Params.Get(ical.PropTimezoneID), `"`)); tzid != "" && tzid != "Local" && !strings.HasSuffix(v, "Z") {
		if l, err := time.LoadLocation(tzid); err == nil {
			loc = l
		}
	}
	return parseDateTime(v, loc)
}

func parseDate(v string, loc *time.Location) (time.Time, bool) {
	d, err := time.ParseInLocation("20060102", v, loc)
	if err != nil {
		return time.Time{}, false
	}
	return d.Add(allDayHour * time.Hour), true
}

func parseDateTime(v string, loc *time.Location) (time.Time, bool) {
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		return t, err == nil
	}
	t, err := time.ParseInLocation("20060102T150405", v, loc)
	return t, err == nil
}

// GoogleLinkEvent reads a Google Calendar add-to-calendar link
// (calendar.google.com/calendar/render or /calendar/event with action=TEMPLATE).
// Its UID is "gcal:" and the first 16 hex characters of the link's SHA-256.
func GoogleLinkEvent(link string, loc *time.Location) (Event, bool) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "calendar.google.com") ||
		(u.Path != "/calendar/render" && u.Path != "/calendar/event") {
		return Event{}, false
	}
	q := u.Query()
	if !strings.EqualFold(q.Get("action"), "TEMPLATE") {
		return Event{}, false
	}
	if tz := q.Get("ctz"); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	s, e, _ := strings.Cut(q.Get("dates"), "/")
	start, ok := gcalTime(s, loc)
	if !ok {
		return Event{}, false
	}
	end, ok := gcalTime(e, loc)
	if !ok || end.Before(start) {
		end = start
	}
	sum := sha256.Sum256([]byte(link))
	return Event{
		UID:      "gcal:" + hex.EncodeToString(sum[:])[:16],
		Title:    truncate(strings.TrimSpace(q.Get("text")), maxField),
		Start:    start,
		End:      end,
		Location: truncate(strings.TrimSpace(q.Get("location")), maxField),
	}, true
}

func gcalTime(v string, loc *time.Location) (time.Time, bool) {
	if len(v) == len("20060102") {
		return parseDate(v, loc)
	}
	return parseDateTime(v, loc)
}

// icsURL reads a URL property, whose value type is URI rather than text.
func icsURL(p *ical.Prop) string {
	if p == nil {
		return ""
	}
	return webURL(truncate(strings.TrimSpace(p.Value), maxField))
}

// webURL keeps http(s) URLs only.
func webURL(s string) string {
	l := strings.ToLower(s)
	if strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
		return s
	}
	return ""
}

// CalendarEvents resolves the message's .ics parts, then its Google add-to-calendar links, into
// events. loc is the persona's time zone, known only after routing. Parts that do not parse are
// skipped; an event repeating an earlier UID is dropped; at most MaxEvents are returned.
func (m Message) CalendarEvents(loc *time.Location) []Event {
	var out []Event
	seen := map[string]bool{}
	add := func(e Event) bool {
		if len(out) >= MaxEvents {
			return false
		}
		if !seen[e.UID] {
			seen[e.UID] = true
			out = append(out, e)
		}
		return true
	}
	for _, part := range m.calendar {
		evs, err := ParseICS(part, loc)
		if err != nil {
			continue
		}
		for _, e := range evs {
			if !add(e) {
				return out
			}
		}
	}
	for _, l := range m.Links {
		if e, ok := GoogleLinkEvent(l, loc); ok && !add(e) {
			return out
		}
	}
	return out
}
