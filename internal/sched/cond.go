// Package sched holds the rules for runs that wait: what a run can wait for,
// when the wait is over, and how to say so in plain words.
package sched

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

const (
	MaxWait        = 7 * 24 * time.Hour
	DefaultTimeout = 24 * time.Hour
	MinWait        = time.Minute
)

// Until says what a run waits for. Set exactly one of email, handoff or time.
type Until struct {
	Email   *EmailCond   `json:"email,omitempty" jsonschema:"wait for an email to arrive in this persona's inbox"`
	Handoff *HandoffCond `json:"handoff,omitempty" jsonschema:"wait for a story that this story hands off to, to finish"`
	Time    *TimeCond    `json:"time,omitempty" jsonschema:"wait until a point in time"`
	Timeout string       `json:"timeout,omitempty" jsonschema:"how long to wait before giving up, such as 8h; from 1m to 168h, default 24h; ignored for time waits"`
}

type EmailCond struct {
	From    string `json:"from,omitempty" jsonschema:"part of the sender address, case-insensitive"`
	Subject string `json:"subject,omitempty" jsonschema:"part of the subject, case-insensitive"`
	AfterID string `json:"after_id,omitempty" jsonschema:"the id of the newest message you already saw in messages_list; any later message counts, even one that arrives while you wrap up"`
}

type HandoffCond struct {
	Role  string `json:"role" jsonschema:"the role of the story to wait for, as declared in the story's handoffs"`
	Story string `json:"story" jsonschema:"the name of the story to wait for"`
}

type TimeCond struct {
	At    string `json:"at,omitempty" jsonschema:"an RFC3339 time from 1 minute to 7 days away, such as 2026-11-04T15:00:00Z"`
	After string `json:"after,omitempty" jsonschema:"a duration from now, from 1m to 168h, such as 30m; give this or at"`
}

func durationIn(s string) (time.Duration, bool) {
	d, err := time.ParseDuration(s)
	return d, err == nil && d >= MinWait && d <= MaxWait
}

// ParseWait checks u against the project and the waiting run and returns the
// wait to record.
func ParseWait(u Until, p project.Project, r store.Run, now time.Time) (store.Wait, error) {
	n := 0
	for _, set := range []bool{u.Email != nil, u.Handoff != nil, u.Time != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		return store.Wait{}, errors.New("until needs exactly one of email, handoff or time")
	}
	w := store.Wait{RunID: r.ID, Began: now, Status: store.WaitOpen}
	switch {
	case u.Email != nil:
		if u.Email.From == "" && u.Email.Subject == "" {
			return w, errors.New("email needs from or subject")
		}
		w.Kind, w.From, w.Subject, w.AfterID = "email", u.Email.From, u.Email.Subject, u.Email.AfterID
	case u.Handoff != nil:
		h := u.Handoff
		st, _ := p.FindStory(r.Role, r.Story)
		var names []string
		for _, d := range st.Handoffs {
			if d.Role == h.Role && d.Story == h.Story {
				names = nil
				break
			}
			names = append(names, d.Role+"/"+d.Story)
		}
		if names != nil || len(st.Handoffs) == 0 {
			to := "nobody"
			if len(names) > 0 {
				to = ": " + strings.Join(names, ", ")
			}
			sep := " "
			if len(names) > 0 {
				sep = ""
			}
			return w, fmt.Errorf("story %q does not hand off to %s/%s; it hands off to%s%s", r.Story, h.Role, h.Story, sep, to)
		}
		w.Kind, w.Role, w.Story = "handoff", h.Role, h.Story
	default:
		t := u.Time
		if (t.At == "") == (t.After == "") {
			return w, errors.New("time needs at or after")
		}
		w.Kind = "time"
		if t.After != "" {
			d, ok := durationIn(t.After)
			if !ok {
				return w, errors.New("time.after must be a duration from 1m to 168h, such as 30m")
			}
			w.At = now.Add(d)
		} else {
			at, err := time.Parse(time.RFC3339, t.At)
			if err != nil {
				return w, errors.New("time.at must be RFC3339, such as 2026-11-04T15:00:00Z")
			}
			if at.Before(now.Add(MinWait)) || at.After(now.Add(MaxWait)) {
				return w, errors.New("time.at must be between 1 minute and 7 days from now")
			}
			w.At = at
		}
		w.Deadline = w.At
		return w, nil
	}
	timeout := DefaultTimeout
	if u.Timeout != "" {
		d, ok := durationIn(u.Timeout)
		if !ok {
			return w, errors.New("timeout must be a duration from 1m to 168h, such as 8h")
		}
		timeout = d
	}
	w.Deadline = now.Add(timeout)
	return w, nil
}

// Describe says what the wait is for, in plain words.
func Describe(w store.Wait) string {
	switch w.Kind {
	case "email":
		switch {
		case w.From != "" && w.Subject != "":
			return fmt.Sprintf("an email from %q with %q in the subject", w.From, w.Subject)
		case w.From != "":
			return fmt.Sprintf("an email from %q", w.From)
		default:
			return fmt.Sprintf("an email with %q in the subject", w.Subject)
		}
	case "handoff":
		return fmt.Sprintf("%s to finish %q", w.Role, w.Story)
	case "time":
		return w.At.UTC().Format("15:04 on 2 Jan 2006") + " (UTC)"
	}
	return w.Kind
}

// Arrival is what ended a wait.
type Arrival struct {
	Detail    string
	MessageID string
}

// Met reports whether the wait's condition has happened. r is the waiting run.
func Met(st *store.Store, w store.Wait, r store.Run, now time.Time) (Arrival, bool, error) {
	switch w.Kind {
	case "time":
		if !now.Before(w.At) {
			return Arrival{Detail: "time reached"}, true, nil
		}
	case "email":
		q := store.MessageQuery{Persona: r.Persona, Since: w.Began, Limit: 200}
		if w.AfterID != "" {
			// Mail after the newest one the agent saw counts, even if it came
			// in during wrap-up, before the wait began. If that message was
			// pruned, fall back to mail since the wait began.
			if _, err := st.Message(w.AfterID); err == nil {
				q.Since, q.AfterID = time.Time{}, w.AfterID
			} else if !errors.Is(err, store.ErrNotFound) {
				return Arrival{}, false, err
			}
		}
		msgs, err := st.Messages(q)
		if err != nil {
			return Arrival{}, false, err
		}
		from, subj := strings.ToLower(w.From), strings.ToLower(w.Subject)
		for i := len(msgs) - 1; i >= 0; i-- { // newest first, so oldest match wins from the end
			m := msgs[i]
			if strings.Contains(strings.ToLower(m.From), from) && strings.Contains(strings.ToLower(m.Subject), subj) {
				return Arrival{Detail: fmt.Sprintf("email %q from %s", plain(m.Subject, 200), plain(m.From, 200)), MessageID: m.ID}, true, nil
			}
		}
	case "handoff":
		runs, err := st.RunsInJourney(r.Journey)
		if err != nil {
			return Arrival{}, false, err
		}
		for _, x := range runs {
			if x.Role == w.Role && x.Story == w.Story && x.State == store.RunDone && x.Finished.After(w.Began) {
				return Arrival{Detail: fmt.Sprintf("%s finished %q: %s", plain(x.Persona, 200), x.Story, plain(firstLine(x.Outcome, 200), 200))}, true, nil
			}
		}
	}
	return Arrival{}, false, nil
}

// plain makes outside text safe to show an agent: control characters (newlines
// included) become spaces and it is cut to max runes.
func plain(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

func firstLine(s string, max int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// StuckText is the outcome recorded when a wait runs out.
func StuckText(w store.Wait) string {
	return fmt.Sprintf("waited %s for %s; it never happened", fmtDur(w.Deadline.Sub(w.Began)), Describe(w))
}

func fmtDur(d time.Duration) string {
	if d == MaxWait {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	h, m := d/time.Hour, d%time.Hour/time.Minute
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dm", m)
}
