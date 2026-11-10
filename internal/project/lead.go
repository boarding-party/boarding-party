package project

import (
	"fmt"
	"strconv"
	"strings"
)

// LeadStory is the reserved story name of a lead run: a run of the lead
// itself, with no browser.
const LeadStory = "(lead)"

// Lead defaults and limits.
const (
	DefaultLeadBudget   = 5.0
	DefaultLeadRuns     = 10
	DefaultLeadChildren = 6
	DefaultLeadWake     = "09:00"
	MaxLeadRuns         = 100
	MaxLeadChildren     = 20
	MaxLeadBrief        = 2000
)

// Lead makes a persona a lead: with any power ticked, it writes and runs its
// own child personas within its budget instead of taking browser runs.
type Lead struct {
	// Brief says what the lead should find out.
	Brief          string `json:"brief"`
	SeeRuns        bool   `json:"seeRuns,omitempty"`
	CreatePersonas bool   `json:"createPersonas,omitempty"`
	StartRuns      bool   `json:"startRuns,omitempty"`
	TidyBugs       bool   `json:"tidyBugs,omitempty"`
	// DailyBudget caps a day's spend of the lead and all its children, in US
	// dollars. nil means DefaultLeadBudget.
	DailyBudget *float64 `json:"dailyBudget,omitempty"`
	// DailyRuns caps the runs its children start in a day (the lead's own runs
	// count only against DailyBudget); 0 means DefaultLeadRuns.
	DailyRuns int `json:"dailyRuns,omitempty"`
	// MaxChildren caps the children that are not stopped; 0 means DefaultLeadChildren.
	MaxChildren int `json:"maxChildren,omitempty"`
	// WakeAt is the daily wake time, HH:MM in the project's time zone; empty means DefaultLeadWake.
	WakeAt string `json:"wakeAt,omitempty"`
}

// IsLead reports whether the persona has any lead power ticked.
func (pe Persona) IsLead() bool {
	l := pe.Lead
	return l != nil && (l.SeeRuns || l.CreatePersonas || l.StartRuns || l.TidyBugs)
}

func (l Lead) Budget() float64 {
	if l.DailyBudget == nil {
		return DefaultLeadBudget
	}
	return *l.DailyBudget
}

func (l Lead) Runs() int {
	if l.DailyRuns == 0 {
		return DefaultLeadRuns
	}
	return l.DailyRuns
}

func (l Lead) Children() int {
	if l.MaxChildren == 0 {
		return DefaultLeadChildren
	}
	return l.MaxChildren
}

// Wake returns the daily wake time; validation guarantees WakeAt parses.
func (l Lead) Wake() (hour, min int) {
	h, m, _ := parseClock(l.WakeAt)
	return h, m
}

func parseClock(s string) (int, int, bool) {
	if s == "" {
		s = DefaultLeadWake
	}
	hs, ms, ok := strings.Cut(s, ":")
	if !ok || len(hs) != 2 || len(ms) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(hs)
	m, err2 := strconv.Atoi(ms)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// normalise checks the lead's limits; "Create personas" ticks "Start runs",
// since children run only when their lead queues them.
func (l *Lead) normalise() error {
	if len(l.Brief) > MaxLeadBrief {
		return fmt.Errorf("lead brief is longer than %d characters", MaxLeadBrief)
	}
	if b := l.DailyBudget; b != nil && (!(*b > 0) || *b > MaxDailyBudget) {
		return fmt.Errorf("lead dailyBudget must be more than 0 and at most 1000 (US dollars)")
	}
	if l.DailyRuns < 0 || l.DailyRuns > MaxLeadRuns {
		return fmt.Errorf("lead dailyRuns must be between 1 and %d", MaxLeadRuns)
	}
	if l.MaxChildren < 0 || l.MaxChildren > MaxLeadChildren {
		return fmt.Errorf("lead maxChildren must be between 1 and %d", MaxLeadChildren)
	}
	if _, _, ok := parseClock(l.WakeAt); !ok {
		return fmt.Errorf("lead wakeAt %q must be a time such as 09:00", l.WakeAt)
	}
	if l.CreatePersonas {
		l.StartRuns = true
	}
	return nil
}

// Persona returns the named persona and its role.
func (p Project) Persona(name string) (Persona, Role, bool) {
	for _, r := range p.Roles {
		for _, pe := range r.Personas {
			if pe.Name == name {
				return pe, r, true
			}
		}
	}
	return Persona{}, Role{}, false
}

// LeadOf returns the lead that made a child persona.
func (p Project) LeadOf(persona string) (string, bool) {
	_, r, ok := p.Persona(persona)
	if !ok || r.MadeBy == "" {
		return "", false
	}
	return r.MadeBy, true
}

// ChildrenOf returns the names of a lead's children in the project.
func (p Project) ChildrenOf(lead string) []string {
	var out []string
	for _, r := range p.Roles {
		if lead == "" || r.MadeBy != lead {
			continue
		}
		for _, pe := range r.Personas {
			out = append(out, pe.Name)
		}
	}
	return out
}

// Leads returns the personas with a lead power ticked, in file order.
func (p Project) Leads() []Persona {
	var out []Persona
	for _, r := range p.Roles {
		for _, pe := range r.Personas {
			if pe.IsLead() {
				out = append(out, pe)
			}
		}
	}
	return out
}
