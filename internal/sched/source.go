package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// Fire is one source's wish that a persona acts now.
type Fire struct {
	Source  string // source name, e.g. "schedule"
	Persona string
	Role    string
	Stories []string        // candidates, preferred first; the scheduler takes the first eligible
	Reason  string          // shown on the board, e.g. "Scheduled: every 1 h"
	Context json.RawMessage // handed to the run (a message in 2d); nil for schedule
}

// Source is anything that can ask for a run: a schedule, a mailbox, a calendar.
type Source interface {
	Name() string
	// Due returns the fires that are due at now. It makes no model calls.
	Due(ctx context.Context, p project.Project, now time.Time) ([]Fire, error)
	// Settle tells the source what became of a fire: story is the one queued,
	// or "" when it was skipped (gated, persona busy, no eligible story).
	// A source advances its own cursor here; a skipped fire is never replayed.
	// Settle is called exactly once per fire, when the scheduler has decided
	// its fate. If the scheduler itself fails on a fire (a store error), it
	// does not settle it, so the source offers the fire again on a later tick.
	Settle(f Fire, story string, now time.Time) error
}

func (s *Scheduler) sources() []Source {
	if s.Sources != nil {
		return s.Sources
	}
	return []Source{&Schedule{Store: s.Store, Rand: s.Rand}}
}

// fire asks every source what is due and queues the runs the shared policy
// allows: not while gated, not for a busy persona, and only for a story that
// is neither stuck nor waiting. A source error does not stop the others.
func (s *Scheduler) fire(ctx context.Context, now time.Time) error {
	p, err := s.Store.Project()
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	// Only pause and budget count as gated; any other gate error is the
	// scheduler failing, so every fire stays unsettled for a later tick.
	gerr := s.Runs.Gate(now)
	gated := errors.Is(gerr, runs.ErrPaused) || errors.Is(gerr, runs.ErrOverBudget)
	if gerr != nil && !gated {
		return fmt.Errorf("gate: %w", gerr)
	}
	var busy, waiting map[string]bool
	if !gated {
		if busy, err = s.busyPersonas(); err != nil {
			return err
		}
		if waiting, err = s.waitingStories(); err != nil {
			return err
		}
	}
	var errs []error
	for _, src := range s.sources() {
		fires, err := src.Due(ctx, p, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("source %s: %w", src.Name(), err))
		}
		for _, f := range fires {
			story := ""
			if !gated && !busy[f.Persona] {
				if story, err = s.pick(f, waiting); err != nil {
					errs = append(errs, fmt.Errorf("source %s: %w", src.Name(), err))
					continue // unsettled: offered again next tick
				}
			}
			if story != "" {
				q := store.Queued{ID: s.newID(), Persona: f.Persona, Role: f.Role, Story: story, Reason: f.Reason,
					Context: f.Context, Created: now}
				if err := s.Store.Enqueue(q); err != nil {
					errs = append(errs, fmt.Errorf("source %s: queue %s/%s: %w", src.Name(), f.Persona, story, err))
					continue // unsettled: offered again next tick
				}
				busy[f.Persona] = true
			}
			if err := src.Settle(f, story, now); err != nil {
				errs = append(errs, fmt.Errorf("source %s: settle: %w", src.Name(), err))
			}
		}
	}
	return errors.Join(errs...)
}

// busyPersonas returns the personas with an active run or any queued item.
func (s *Scheduler) busyPersonas() (map[string]bool, error) {
	out := map[string]bool{}
	active, err := s.Store.ActiveRuns()
	if err != nil {
		return nil, err
	}
	for _, r := range active {
		out[r.Persona] = true
	}
	queue, err := s.Store.Queue()
	if err != nil {
		return nil, err
	}
	for _, q := range queue {
		out[q.Persona] = true
	}
	return out, nil
}

// waitingStories returns "persona/story" for every open wait.
func (s *Scheduler) waitingStories() (map[string]bool, error) {
	ws, err := s.Store.OpenWaits()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, w := range ws {
		r, err := s.Store.RunByID(w.RunID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[r.Persona+"/"+r.Story] = true
	}
	return out, nil
}

// pick returns the first story of f the persona can start, "" if none.
func (s *Scheduler) pick(f Fire, waiting map[string]bool) (string, error) {
	for _, st := range f.Stories {
		if waiting[f.Persona+"/"+st] {
			continue
		}
		last, err := s.Store.LastRun(f.Persona, st)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", err
		}
		if err == nil && last.State == store.RunStuck {
			continue
		}
		return st, nil
	}
	return "", nil
}
