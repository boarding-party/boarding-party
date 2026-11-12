package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// ErrLeadOverBudget means the lead's own budget (money or runs) is spent for
// today. Unlike ErrOverBudget it stops only that lead and its children.
var ErrLeadOverBudget = errors.New("the lead's budget is spent")

// LeadUsage is what a lead and its children used today. Spent covers the
// lead's own runs too; Runs counts only its children's, so planning never
// eats the runs meant for testing.
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
	// Stopped children are left out of p, but their runs today still count:
	// stopping a child must not free its budget.
	cs, err := s.Store.Children(lead)
	if err != nil {
		return LeadUsage{}, err
	}
	for _, c := range cs {
		who[c.Name] = true
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
		if r.Persona != lead && r.Kind != store.RunReplay { // replays cost nothing and use none of a lead's runs
			u.Runs++
		}
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

// childClash says why the user's project p can't be saved beside the stored
// children, or "": a persona or role named like a child, or a persona with a
// child's email address (both compared without case, so mail never reaches
// two personas), or a lead with children that is no longer in p (renaming
// or removing it would cut its children loose from their lead and budget).
func (s *Service) childClash(p project.Project) (string, error) {
	cs, err := s.Store.AllChildren()
	if err != nil {
		return "", err
	}
	names, emails := map[string]bool{}, map[string]bool{}
	for _, r := range p.Roles {
		names[strings.ToLower(r.Name)] = true
		for _, pe := range r.Personas {
			names[strings.ToLower(pe.Name)] = true
			if a, err := mail.ParseAddress(pe.Identity["email"]); err == nil {
				emails[strings.ToLower(a.Address)] = true
			}
		}
	}
	for _, c := range cs {
		if names[strings.ToLower(c.Name)] {
			return fmt.Sprintf("%q is the name of a lead's persona; adopt it or pick another name", c.Name), nil
		}
		if a, err := mail.ParseAddress(c.Persona.Identity["email"]); err == nil && emails[strings.ToLower(a.Address)] {
			return fmt.Sprintf("the address %s belongs to %s, a lead's persona", a.Address, c.Name), nil
		}
		if _, _, ok := p.Persona(c.Lead); !ok {
			return fmt.Sprintf("%s has personas it made; delete or adopt them on its lead page before renaming or removing %s", c.Lead, c.Lead), nil
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
