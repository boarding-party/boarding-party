package runs

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// ErrNoLoginWait says a login was given for a run that is not waiting for one.
var ErrNoLoginWait = errors.New("this run is not waiting for a login")

// GiveLogin answers a run that waits for a login: it keeps the login under
// the wait's kind of user (replacing an older one of that kind), signs the
// waiting persona in with it, and marks the wait answered so the scheduler
// continues the run.
func (s *Service) GiveLogin(ctx context.Context, runID, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" || password == "" || len(username) > project.MaxLoginField || len(password) > project.MaxLoginField {
		return fmt.Errorf("%w: username and password must be 1 to %d characters", ErrInvalid, project.MaxLoginField)
	}
	w, err := s.Store.WaitFor(runID)
	if errors.Is(err, store.ErrNotFound) || err == nil && (w.Kind != "login" || w.Status != store.WaitOpen) {
		return ErrNoLoginWait
	}
	if err != nil {
		return err
	}
	r, err := s.Store.RunByID(runID)
	if err != nil {
		return err
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	p, err := s.Store.RawProject()
	if err != nil {
		return err
	}
	kind := w.Login
	logins := make([]project.Login, 0, len(p.Logins)+1)
	for _, l := range p.Logins {
		if strings.EqualFold(l.Kind, w.Login) {
			kind = l.Kind
			continue
		}
		logins = append(logins, l)
	}
	p.Logins = append(logins, project.Login{Kind: kind, Username: username, Password: password})
	child, err := s.Store.Child(r.Persona)
	switch {
	case err == nil:
		child.Persona.Login = kind
		if err := s.Store.UpdateChild(child); err != nil {
			return err
		}
	case errors.Is(err, store.ErrNotFound):
		setPersona(&p, r.Persona, func(pe *project.Persona) { pe.Login = kind })
	default:
		return err
	}
	if err := s.Store.PutProject(p); err != nil {
		return err
	}
	_, err = s.Store.GiveLogin(runID, time.Now().UTC())
	return err
}

// FillEmails gives personas email once a mailbox is saved: address, when it
// is an email address, goes to every lead without one, and every child of a
// lead with an address that has none gets its plus-address.
func (s *Service) FillEmails(ctx context.Context, address string) error {
	if a, err := mail.ParseAddress(address); err != nil || a.Address != address {
		return nil
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	p, err := s.Store.RawProject()
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	changed := false
	for i := range p.Roles {
		for j := range p.Roles[i].Personas {
			pe := &p.Roles[i].Personas[j]
			if pe.IsLead() && pe.Identity["email"] == "" {
				id := map[string]string{}
				for k, v := range pe.Identity {
					id[k] = v
				}
				id["email"] = address
				pe.Identity, changed = id, true
			}
		}
	}
	if changed {
		if err := s.Store.PutProject(p); err != nil {
			return err
		}
	}
	cs, err := s.Store.AllChildren()
	if err != nil {
		return err
	}
	for _, c := range cs {
		lead, _, ok := p.Persona(c.Lead)
		if !ok || c.Persona.Identity["email"] != "" || lead.Identity["email"] == "" {
			continue
		}
		id := map[string]string{}
		for k, v := range c.Persona.Identity {
			id[k] = v
		}
		id["email"] = project.ChildEmail(lead.Identity["email"], c.Name)
		c.Persona.Identity = id
		if err := s.Store.UpdateChild(c); err != nil {
			return err
		}
	}
	return nil
}

// setPersona applies f to the named persona of p's file.
func setPersona(p *project.Project, name string, f func(*project.Persona)) {
	for i := range p.Roles {
		for j := range p.Roles[i].Personas {
			if p.Roles[i].Personas[j].Name == name {
				f(&p.Roles[i].Personas[j])
			}
		}
	}
}
