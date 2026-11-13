package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// wakeLeads queues a lead run for each lead that should look at its work
// now: on its first wake, after its children finish a batch, and once a day.
// A lead never wakes while it or any child is queued or running, while runs
// are gated, or once its own budget is spent.
func (s *Scheduler) wakeLeads(now time.Time) error {
	p, err := s.Store.Project()
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	leads := p.Leads()
	if len(leads) == 0 {
		return nil
	}
	if err := s.Runs.Gate(now); err != nil {
		if errors.Is(err, runs.ErrPaused) || errors.Is(err, runs.ErrOverBudget) {
			return nil
		}
		return fmt.Errorf("gate: %w", err)
	}
	busy, err := s.busyPersonas(false)
	if err != nil {
		return err
	}
	var errs []error
	for _, l := range leads {
		children := p.ChildrenOf(l.Name)
		if busy[l.Name] || anyOf(busy, children) {
			continue
		}
		if err := s.Runs.LeadGate(p, l.Name, now); err != nil {
			if !errors.Is(err, runs.ErrLeadOverBudget) {
				errs = append(errs, fmt.Errorf("lead %s: %w", l.Name, err))
			}
			continue
		}
		reason, err := s.wakeReason(p, l, children, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("lead %s: %w", l.Name, err))
			continue
		}
		if reason == "" {
			continue
		}
		_, role, _ := p.Persona(l.Name)
		ctxJSON, err := json.Marshal(store.RunContext{Kind: "lead", Reason: reason})
		if err != nil {
			return err
		}
		if err := s.Store.Enqueue(store.Queued{ID: s.newID(), Persona: l.Name, Role: role.Name, Story: project.LeadStory,
			Context: ctxJSON, Reason: "Lead: " + reason, Created: now}); err != nil {
			errs = append(errs, fmt.Errorf("lead %s: queue: %w", l.Name, err))
		}
	}
	return errors.Join(errs...)
}

// wakeReason says why the lead should wake now, or "" if it should not.
func (s *Scheduler) wakeReason(p project.Project, l project.Persona, children []string, now time.Time) (string, error) {
	last, err := s.Store.LastRun(l.Name, project.LeadStory)
	if errors.Is(err, store.ErrNotFound) {
		return "First wake", nil
	}
	if err != nil {
		return "", err
	}
	if len(children) > 0 {
		rs, err := s.Store.RunsSince(last.Started)
		if err != nil {
			return "", err
		}
		kids := map[string]bool{}
		for _, c := range children {
			kids[c] = true
		}
		for _, r := range rs {
			// A replay is not news a lead plans from, and waking one costs a model run.
			if kids[r.Persona] && r.Kind != store.RunReplay && r.State.Final() && r.Finished.After(last.Started) {
				return "Children finished", nil
			}
		}
	}
	h, m := l.Lead.Wake()
	loc := p.Location()
	y, mo, d := now.In(loc).Date()
	slot := time.Date(y, mo, d, h, m, 0, 0, loc)
	if !now.Before(slot) && last.Started.Before(slot) {
		return "Daily wake", nil
	}
	return "", nil
}

func anyOf(set map[string]bool, names []string) bool {
	for _, n := range names {
		if set[n] {
			return true
		}
	}
	return false
}
