package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// ErrLeadOverBudget means the lead's own budget (money or runs) is spent for
// today. Unlike ErrOverBudget it stops only that lead and its children.
var ErrLeadOverBudget = errors.New("the lead's budget is spent")

// LeadUsage is what a lead and its children used today.
type LeadUsage struct {
	Spent float64 `json:"spent"`
	Runs  int     `json:"runs"`
}

// LeadUsage sums the runs started since the project's local midnight by the
// lead or any of its children in p.
func (s *Service) LeadUsage(p project.Project, lead string, now time.Time) (LeadUsage, error) {
	who := map[string]bool{lead: true}
	for _, c := range p.ChildrenOf(lead) {
		who[c] = true
	}
	rs, err := s.Store.RunsSince(DayStart(now, p.Location()))
	if err != nil {
		return LeadUsage{}, err
	}
	var u LeadUsage
	for _, r := range rs {
		if !who[r.Persona] {
			continue
		}
		u.Runs++
		us, err := s.Usage(r.ID)
		if err != nil {
			return LeadUsage{}, err
		}
		if us != nil {
			u.Spent += Cost(*us)
		}
	}
	return u, nil
}

// leadOf returns the lead whose budget a run of persona spends: the persona
// itself when it is a lead, or the lead that made it.
func leadOf(p project.Project, persona string) (project.Persona, bool) {
	name := persona
	if l, ok := p.LeadOf(persona); ok {
		name = l
	}
	pe, _, ok := p.Persona(name)
	if !ok || !pe.IsLead() {
		return project.Persona{}, false
	}
	return pe, true
}

// LeadGate refuses a run of persona when its lead's budget is spent.
func (s *Service) LeadGate(p project.Project, persona string, now time.Time) error {
	lead, ok := leadOf(p, persona)
	if !ok {
		return nil
	}
	u, err := s.LeadUsage(p, lead.Name, now)
	if err != nil {
		return err
	}
	l := *lead.Lead
	switch {
	case u.Spent >= l.Budget():
		return fmt.Errorf("%w: %s spent $%.2f of $%.2f today", ErrLeadOverBudget, lead.Name, u.Spent, l.Budget())
	case u.Runs >= l.Runs():
		return fmt.Errorf("%w: %s used %d of %d runs today", ErrLeadOverBudget, lead.Name, u.Runs, l.Runs())
	}
	return nil
}

// WakeLead queues a lead run. reason is shown to the lead as why it woke.
func (s *Service) WakeLead(lead, reason string, now time.Time) (store.Queued, error) {
	p, err := s.Store.Project()
	if err != nil {
		return store.Queued{}, fmt.Errorf("load project: %w", err)
	}
	pe, role, ok := p.Persona(lead)
	if !ok || !pe.IsLead() {
		return store.Queued{}, fmt.Errorf("%w: %q is not a lead", ErrInvalid, lead)
	}
	busy, err := s.personaBusy(lead)
	if err != nil {
		return store.Queued{}, err
	}
	if busy {
		return store.Queued{}, fmt.Errorf("%w: %s is already queued or running", ErrBusy, lead)
	}
	ctxJSON, err := json.Marshal(store.RunContext{Kind: "lead", Reason: reason})
	if err != nil {
		return store.Queued{}, err
	}
	id, err := randHex(16)
	if err != nil {
		return store.Queued{}, err
	}
	q := store.Queued{ID: id, Persona: lead, Role: role.Name, Story: project.LeadStory, Context: ctxJSON,
		Reason: "Lead: " + reason, Created: now}
	return q, s.Store.Enqueue(q)
}

// personaBusy reports whether persona has a queued or active run.
func (s *Service) personaBusy(persona string) (bool, error) {
	active, err := s.Store.ActiveRuns()
	if err != nil {
		return false, err
	}
	for _, r := range active {
		if r.Persona == persona {
			return true, nil
		}
	}
	queue, err := s.Store.Queue()
	if err != nil {
		return false, err
	}
	for _, q := range queue {
		if q.Persona == persona {
			return true, nil
		}
	}
	return false, nil
}

// childNameTaken returns a stored child whose name a persona or role of the
// user's project p uses, or "".
func (s *Service) childNameTaken(p project.Project) (string, error) {
	cs, err := s.Store.AllChildren()
	if err != nil {
		return "", err
	}
	names := map[string]bool{}
	for _, r := range p.Roles {
		names[r.Name] = true
		for _, pe := range r.Personas {
			names[pe.Name] = true
		}
	}
	for _, c := range cs {
		if names[c.Name] {
			return c.Name, nil
		}
	}
	return "", nil
}

// Adopt makes a child hand-made: its role (persona pe and stories, as the
// user edited them) moves into the user's project file and the child leaves
// its lead. A child never has powers, so pe.Lead is dropped.
func (s *Service) Adopt(ctx context.Context, name string, pe project.Persona, stories []project.Story) error {
	c, err := s.Store.Child(name)
	if err != nil {
		return err
	}
	raw, err := s.Store.RawProject()
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	pe.Name, pe.Lead = name, nil
	raw.Roles = append(raw.Roles, project.Role{Name: name, Description: "Adopted from " + c.Lead,
		Personas: []project.Persona{pe}, Stories: stories})
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	p, err := project.LoadJSON(b)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := s.Store.AdoptChild(name, p); err != nil {
		return err
	}
	return s.SyncHosts(ctx, p)
}
