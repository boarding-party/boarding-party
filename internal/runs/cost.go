package runs

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// price is USD per million tokens.
type price struct{ in, out, cacheRead, cacheWrite float64 }

// prices are matched by model-ID prefix, in order. Cache writes are the
// 5-minute TTL rate (1.25x input).
var prices = []struct {
	prefix string
	price
}{
	{"claude-haiku-5-5", price{0.10, 0.50, 0.01, 0.125}},
	{"claude-sonnet-5-5", price{2, 10, 0.20, 2.50}},
	{"claude-opus-5-5", price{4, 20, 0.40, 5.00}},
}

// dearest prices anything unknown, so a surprise model never under-counts.
var dearest = prices[len(prices)-1].price

func (p price) cost(c Counts) float64 {
	return (float64(c.Input)*p.in + float64(c.Output)*p.out +
		float64(c.CacheRead)*p.cacheRead + float64(c.CacheWrite)*p.cacheWrite) / 1e6
}

// Cost prices a run's usage in USD. A usage without a per-model split, or with
// an unknown model, is priced at the dearest rate.
func Cost(u Usage) float64 {
	if len(u.ByModel) == 0 {
		return dearest.cost(u.Counts)
	}
	var total float64
	for model, c := range u.ByModel {
		p := dearest
		for _, row := range prices {
			if strings.HasPrefix(model, row.prefix) {
				p = row.price
				break
			}
		}
		total += p.cost(c)
	}
	// Top-level counts beyond what the split accounts for are priced dearest,
	// so mismatched reports can never under-count.
	split := splitSum(u.ByModel)
	excess := Counts{Input: over(u.Input, split.Input), Output: over(u.Output, split.Output),
		CacheRead: over(u.CacheRead, split.CacheRead), CacheWrite: over(u.CacheWrite, split.CacheWrite)}
	return total + dearest.cost(excess)
}

func over(total, part uint64) uint64 {
	if total > part {
		return total - part
	}
	return 0
}

// DayStart is the most recent midnight in loc at or before now, as UTC.
func DayStart(now time.Time, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc).UTC()
}

// SpentSince sums the cost of every run started at or after t, counting a
// running run's latest reported usage.
func (s *Service) SpentSince(t time.Time) (float64, error) {
	rs, err := s.Store.RunsSince(t)
	if err != nil {
		return 0, err
	}
	var total float64
	for _, r := range rs {
		u, err := s.Usage(r.ID)
		if err != nil {
			return 0, err
		}
		if u != nil {
			total += Cost(*u)
		}
	}
	return total, nil
}

var (
	ErrPaused     = errors.New("the cast is paused; resume it to start runs")
	ErrOverBudget = errors.New("today's budget is spent")
)

// Gate is the single check every run start passes: nil, ErrPaused, or a
// wrapped ErrOverBudget. Paused is checked first: it is cheaper and clearer.
func (s *Service) Gate(now time.Time) error {
	paused, err := s.Store.Paused()
	if err != nil {
		return err
	}
	if paused {
		return ErrPaused
	}
	p, err := s.Store.Project()
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	loc, budget := p.Location(), p.Budget()
	spent, err := s.SpentSince(DayStart(now, loc))
	if err != nil {
		return err
	}
	if spent >= budget {
		return fmt.Errorf("%w: $%.2f of $%.2f; runs start again at midnight (%s) or when you raise the budget", ErrOverBudget, spent, budget, loc)
	}
	return nil
}

// Status is what the board shows about spend and scheduling.
type Status struct {
	Paused      bool      `json:"paused"`
	SpentToday  float64   `json:"spentToday"`
	DailyBudget float64   `json:"dailyBudget"`
	OverBudget  bool      `json:"overBudget"`
	Timezone    string    `json:"timezone"`
	ResetsAt    time.Time `json:"resetsAt"` // next local midnight
	Due         []DueView `json:"due"`      // scheduled personas and calendar events, soonest first
}

// DueView is one scheduled persona, or one calendar event, and when it is next due.
type DueView struct {
	Persona string    `json:"persona"`
	Role    string    `json:"role"`
	At      time.Time `json:"at"`
	Reason  string    `json:"reason,omitempty"` // set for a calendar event, e.g. "Calendar: Video appointment"
}

// calendarWindow is how far ahead Status lists calendar dues.
const calendarWindow = 24 * time.Hour

// Status reports pause state, today's spend against the budget, and the
// schedule. Only personas of roles with an activity pattern have a schedule
// due; calendar events have one when a story of the persona's role triggers
// on them.
func (s *Service) Status(now time.Time) (Status, error) {
	paused, err := s.Store.Paused()
	if err != nil {
		return Status{}, err
	}
	p, err := s.Store.Project()
	if err != nil {
		return Status{}, fmt.Errorf("load project: %w", err)
	}
	loc, budget := p.Location(), p.Budget()
	start := DayStart(now, loc)
	spent, err := s.SpentSince(start)
	if err != nil {
		return Status{}, err
	}
	dues, err := s.Store.Dues()
	if err != nil {
		return Status{}, err
	}
	roleOf := map[string]project.Role{}
	for _, r := range p.Roles {
		for _, pe := range r.Personas {
			roleOf[pe.Name] = r
		}
	}
	due := []DueView{}
	for name, d := range dues {
		if role, ok := roleOf[name]; ok && role.Activity != "" {
			due = append(due, DueView{Persona: name, Role: role.Name, At: d.At})
		}
	}
	cal, err := s.calendarDues(roleOf, now)
	if err != nil {
		return Status{}, err
	}
	due = append(due, cal...)
	sort.Slice(due, func(i, j int) bool {
		if !due[i].At.Equal(due[j].At) {
			return due[i].At.Before(due[j].At)
		}
		return due[i].Persona < due[j].Persona
	})
	y, m, d := start.In(loc).Date()
	return Status{Paused: paused, SpentToday: spent, DailyBudget: budget, OverBudget: spent >= budget,
		Timezone: loc.String(), ResetsAt: time.Date(y, m, d+1, 0, 0, 0, 0, loc).UTC(), Due: due}, nil
}

// calendarDues lists the unfired events whose story starts within
// calendarWindow of now. An event matching several stories is due when the
// first of them starts, at the longest lead.
func (s *Service) calendarDues(roleOf map[string]project.Role, now time.Time) ([]DueView, error) {
	events, err := s.Store.Events("", now, now.Add(calendarWindow+project.MaxLead))
	if err != nil {
		return nil, err
	}
	var out []DueView
	for _, e := range events {
		if e.FiredStart.Equal(e.Start) {
			continue
		}
		role, ok := roleOf[e.Persona]
		if !ok {
			continue
		}
		lead, matched := time.Duration(0), false
		for _, st := range role.Stories {
			if st.Trigger != nil && st.Trigger.Calendar != nil && st.Trigger.Calendar.Matches(e.Title) {
				lead, matched = max(lead, st.Trigger.Calendar.LeadTime()), true
			}
		}
		at := e.Start.Add(-lead)
		if !matched || at.Before(now) || at.After(now.Add(calendarWindow)) {
			continue
		}
		out = append(out, DueView{Persona: e.Persona, Role: role.Name, At: at, Reason: store.CalendarReason(e.Title)})
	}
	return out, nil
}
