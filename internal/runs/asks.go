package runs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
	"github.com/boarding-party/boardingparty/pkg/digest"
)

// The limits a lead can ask its owner to raise, named as in the project file.
const (
	AskChildren = "maxChildren"
	AskRuns     = "dailyRuns"
	AskBudget   = "dailyBudget"
)

// askAction is the lead log action of an ask; its subject is "<limit> <to>".
const askAction = "ask"

// Ask is a lead asking its owner to raise one of its limits to To.
type Ask struct {
	Limit string  `json:"limit"`
	To    float64 `json:"to"`
}

// OpenAsk is a lead's newest ask while the limit is still below it.
type OpenAsk struct {
	Ask
	Now     float64   `json:"now"`
	Reason  string    `json:"reason"`
	RunID   string    `json:"runId"`
	Created time.Time `json:"created"`
}

// current returns the lead's value of limit.
func current(l project.Lead, limit string) (float64, bool) {
	switch limit {
	case AskChildren:
		return float64(l.Children()), true
	case AskRuns:
		return float64(l.Runs()), true
	case AskBudget:
		return l.Budget(), true
	}
	return 0, false
}

// CheckAsk says whether a is something the owner could grant l: a known
// limit, raised above its value now and within the project's maximum.
func CheckAsk(l project.Lead, a Ask) error {
	now, ok := current(l, a.Limit)
	if !ok {
		return fmt.Errorf("%w: limit must be %s, %s or %s", ErrInvalid, AskChildren, AskRuns, AskBudget)
	}
	var max float64
	switch a.Limit {
	case AskChildren:
		max = project.MaxLeadChildren
	case AskRuns:
		max = project.MaxLeadRuns
	case AskBudget:
		max = project.MaxDailyBudget
	}
	if a.Limit != AskBudget && a.To != math.Trunc(a.To) {
		return fmt.Errorf("%w: %s must be a whole number", ErrInvalid, a.Limit)
	}
	if !(a.To > now) || a.To > max {
		return fmt.Errorf("%w: %s is %s now; ask for more, at most %s", ErrInvalid, a.Limit, num(now), num(max))
	}
	return nil
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func parseAsk(subject string) (Ask, bool) {
	limit, to, ok := strings.Cut(subject, " ")
	if !ok {
		return Ask{}, false
	}
	f, err := strconv.ParseFloat(to, 64)
	return Ask{Limit: limit, To: f}, err == nil
}

// LogAsk records a lead's ask in its decision log.
func (s *Service) LogAsk(lead, runID string, a Ask, reason string, now time.Time) error {
	return s.Store.LogLead(store.LeadAction{Lead: lead, RunID: runID, Action: askAction,
		Subject: a.Limit + " " + num(a.To), Reason: reason, Created: now})
}

// leadLimits returns the lead settings of the hand-made lead named name.
func leadLimits(p project.Project, name string) (project.Lead, bool) {
	pe, _, ok := p.Persona(name)
	if !ok || !pe.IsLead() {
		return project.Lead{}, false
	}
	return *pe.Lead, true
}

// OpenAsk returns the lead's newest ask if its limit is still below it, or nil.
func (s *Service) OpenAsk(lead string) (*OpenAsk, error) {
	p, err := s.Store.Project()
	if err != nil {
		return nil, err
	}
	a, err := s.Store.NewestLeadAction(lead, askAction)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return openAsk(p, a), nil
}

func openAsk(p project.Project, a store.LeadAction) *OpenAsk {
	l, ok := leadLimits(p, a.Lead)
	if !ok {
		return nil
	}
	ask, ok := parseAsk(a.Subject)
	if !ok {
		return nil
	}
	now, ok := current(l, ask.Limit)
	if !ok || now >= ask.To {
		return nil
	}
	return &OpenAsk{Ask: ask, Now: now, Reason: a.Reason, RunID: a.RunID, Created: a.Created}
}

// RaiseLeadLimit sets a hand-made lead's limit to a.To, which must be more
// than it is now.
func (s *Service) RaiseLeadLimit(ctx context.Context, lead string, a Ask) error {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	p, err := s.Store.RawProject()
	if err != nil {
		return err
	}
	l, ok := leadLimits(p, lead)
	if !ok {
		return fmt.Errorf("%w: %q is not a lead", ErrInvalid, lead)
	}
	if err := CheckAsk(l, a); err != nil {
		return err
	}
	setPersona(&p, lead, func(pe *project.Persona) {
		nl := *pe.Lead
		switch a.Limit {
		case AskChildren:
			nl.MaxChildren = int(a.To)
		case AskRuns:
			nl.DailyRuns = int(a.To)
		case AskBudget:
			to := a.To
			nl.DailyBudget = &to
		}
		pe.Lead = &nl
	})
	return s.Store.PutProject(p)
}

// asksSince are the leads' asks logged since since that are still open,
// the newest one per lead.
func (s *Service) asksSince(p project.Project, since time.Time) (digest.List[digest.Ask], error) {
	as, err := s.Store.LeadActionsSince(askAction, since)
	if err != nil {
		return digest.List[digest.Ask]{}, err
	}
	newest := map[string]store.LeadAction{}
	var order []string
	for _, a := range as { // oldest first
		if _, ok := newest[a.Lead]; !ok {
			order = append(order, a.Lead)
		}
		newest[a.Lead] = a
	}
	var out []digest.Ask
	for _, lead := range order {
		if o := openAsk(p, newest[lead]); o != nil {
			out = append(out, digest.Ask{Lead: lead, Limit: o.Limit, Now: o.Now, To: o.To})
		}
	}
	return digest.Cap(out), nil
}
