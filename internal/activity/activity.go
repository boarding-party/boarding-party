// Package activity parses a role's plain-text activity schedule into
// jittered due times.
package activity

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type kind int

const (
	none kind = iota
	daily
	every
	perDay
)

// Pattern is a parsed schedule. The zero value means no schedule.
type Pattern struct {
	kind       kind
	times      []int // daily: minutes after midnight
	interval   int   // every: minutes
	lo, hi     int   // perDay: count range
	start, end int   // window, minutes after midnight (end may be 1440)
}

var (
	timeRe   = `(\d{1,2}):(\d{2})`
	winRe    = timeRe + `\s*-\s*` + timeRe
	dailyRe  = regexp.MustCompile(`^daily at (` + timeRe + `(?:\s*,\s*` + timeRe + `)*)$`)
	everyRe  = regexp.MustCompile(`^every (\d+)\s*(min|mins|minutes|h|hour|hours)(?:\s*,\s*` + winRe + `)?$`)
	perDayRe = regexp.MustCompile(`^(\d+)(?:\s*-\s*(\d+))?\s*times a day(?:\s*,\s*` + winRe + `)?$`)
	clockRe  = regexp.MustCompile(timeRe)
)

func parseErr(s string) error {
	return fmt.Errorf(`activity %q: use "daily at 09:00", "every 30 min, 09:00–17:30" or "2–4 times a day, 07:00–23:00"`, s)
}

func clock(h, m string) (int, bool) {
	hh, _ := strconv.Atoi(h)
	mm, _ := strconv.Atoi(m)
	if mm > 59 || hh > 24 || (hh == 24 && mm != 0) {
		return 0, false
	}
	return hh*60 + mm, true
}

func window(g []string, def [2]int) (int, int, bool) {
	if g[0] == "" {
		return def[0], def[1], true
	}
	s, ok1 := clock(g[0], g[1])
	e, ok2 := clock(g[2], g[3])
	return s, e, ok1 && ok2 && s < 1440 && e > s
}

// Parse reads a schedule such as "every 30 min, 09:00-17:30". An empty
// string yields the zero Pattern.
func Parse(s string) (Pattern, error) {
	n := strings.ToLower(strings.TrimSpace(s))
	if n == "" {
		return Pattern{}, nil
	}
	n = strings.NewReplacer("–", "-", "—", "-").Replace(n)
	bad := parseErr(s)

	if m := dailyRe.FindStringSubmatch(n); m != nil {
		var ts []int
		for _, c := range clockRe.FindAllStringSubmatch(m[1], -1) {
			v, ok := clock(c[1], c[2])
			if !ok || v >= 1440 {
				return Pattern{}, bad
			}
			ts = append(ts, v)
		}
		if len(ts) > 8 {
			return Pattern{}, bad
		}
		sort.Ints(ts)
		return Pattern{kind: daily, times: ts}, nil
	}
	if m := everyRe.FindStringSubmatch(n); m != nil {
		v, err := strconv.Atoi(m[1])
		if err != nil {
			return Pattern{}, bad
		}
		if strings.HasPrefix(m[2], "h") {
			v *= 60
		}
		s, e, ok := window(m[3:], [2]int{0, 1440})
		if !ok || v < 15 || v > 1440 {
			return Pattern{}, bad
		}
		return Pattern{kind: every, interval: v, start: s, end: e}, nil
	}
	if m := perDayRe.FindStringSubmatch(n); m != nil {
		lo, err := strconv.Atoi(m[1])
		if err != nil {
			return Pattern{}, bad
		}
		hi := lo
		if m[2] != "" {
			if hi, err = strconv.Atoi(m[2]); err != nil {
				return Pattern{}, bad
			}
		}
		s, e, ok := window(m[3:], [2]int{8 * 60, 20 * 60})
		if !ok || lo < 1 || lo > hi || hi > 48 {
			return Pattern{}, bad
		}
		return Pattern{kind: perDay, lo: lo, hi: hi, start: s, end: e}, nil
	}
	return Pattern{}, bad
}

// IsZero reports whether p is the empty schedule.
func (p Pattern) IsZero() bool { return p.kind == none }

func hhmm(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

func (p Pattern) win() string { return hhmm(p.start) + "–" + hhmm(p.end) }

// String returns the canonical text form, which Parse accepts back.
func (p Pattern) String() string {
	switch p.kind {
	case daily:
		parts := make([]string, len(p.times))
		for i, t := range p.times {
			parts[i] = hhmm(t)
		}
		return "daily at " + strings.Join(parts, ", ")
	case every:
		out := "every " + strconv.Itoa(p.interval) + " min"
		if p.interval%60 == 0 {
			out = "every " + strconv.Itoa(p.interval/60) + " h"
		}
		if p.start != 0 || p.end != 1440 {
			out += ", " + p.win()
		}
		return out
	case perDay:
		n := strconv.Itoa(p.lo)
		if p.hi != p.lo {
			n += "–" + strconv.Itoa(p.hi)
		}
		return n + " times a day, " + p.win()
	}
	return ""
}

// minute returns the wall-clock instant m minutes after midnight on the
// day of d in loc, so DST changes are honoured.
func minute(d time.Time, m int, loc *time.Location) time.Time {
	y, mo, day := d.Date()
	return time.Date(y, mo, day, m/60, m%60, 0, 0, loc)
}

func (p Pattern) bounds(d time.Time, loc *time.Location) (time.Time, time.Time) {
	return minute(d, p.start, loc), minute(d, p.end, loc)
}

// Slack is how early Next can place a daily time (its jitter), 0 for other
// patterns. Asking Next(now+Slack) after a time fired skips that slot's repeat.
func (p Pattern) Slack() time.Duration {
	if p.kind == daily {
		return 10 * time.Minute
	}
	return 0
}

// Next returns the next due time after the given instant. u yields values in
// [0,1); u() == 0.5 applies no jitter.
func (p Pattern) Next(after time.Time, loc *time.Location, u func() float64) time.Time {
	a := after.In(loc)
	switch p.kind {
	case daily:
		var t time.Time
	find:
		for day := 0; day < 2; day++ {
			d := a.AddDate(0, 0, day)
			for _, m := range p.times {
				if c := minute(d, m, loc); c.After(a) {
					t = c
					break find
				}
			}
		}
		j := 2*u() - 1
		if r := t.Add(time.Duration(j * float64(10*time.Minute))); r.After(a) {
			return r
		}
		return t
	case every:
		n := time.Duration(p.interval) * time.Minute
		spread := float64(n) * 0.2
		c := a.Add(n + time.Duration((2*u()-1)*spread))
		ws, we := p.bounds(c.In(loc), loc)
		if !c.Before(ws) && c.Before(we) {
			return c
		}
		if !c.Before(we) {
			ws, _ = p.bounds(c.In(loc).AddDate(0, 0, 1), loc)
		}
		return ws.Add(time.Duration(u() * spread))
	case perDay:
		ws, we := p.bounds(a, loc)
		gap := func(ws, we time.Time) float64 {
			return float64(we.Sub(ws)) / (float64(p.lo+p.hi) / 2)
		}
		if !a.Before(ws) && a.Before(we) {
			if c := a.Add(time.Duration(gap(ws, we) * (0.5 + u()))); c.Before(we) {
				return c
			}
			ws, we = p.bounds(a.AddDate(0, 0, 1), loc)
			return ws.Add(time.Duration(gap(ws, we) * u()))
		}
		if !a.Before(we) {
			ws, we = p.bounds(a.AddDate(0, 0, 1), loc)
		}
		return ws.Add(time.Duration(gap(ws, we) * u()))
	}
	return time.Time{}
}
