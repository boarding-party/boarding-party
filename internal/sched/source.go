package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// Fire is one source's wish that a persona acts now.
type Fire struct {
	Source  string // source name, e.g. "schedule"
	Persona string
	Role    string
	Stories []string // candidates, preferred first; the scheduler takes the first eligible
	Reason  string   // shown on the board, e.g. "Scheduled: every 1 h"
	Context string   // handed to the run (a message body in 2d); "" for schedule
}

// Source is anything that can ask for a run: a schedule, a mailbox, a calendar.
type Source interface {
	Name() string
	// Due returns the fires that are due at now. It makes no model calls.
	Due(ctx context.Context, p project.Project, now time.Time) ([]Fire, error)
	// Settle tells the source what became of a fire: story is the one queued,
	// or "" when it was skipped (gated, persona busy, no eligible story).
	// A source advances its own cursor here; a skipped fire is never replayed.
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
	gated := s.Runs.Gate(now) != nil
	var errs []error
	var busy map[string]bool
	var waiting map[string]bool
	for _, src := range s.sources() {
		fires, err := src.Due(ctx, p, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("source %s: %w", src.Name(), err))
		}
		for _, f := range fires {
			story := ""
			if !gated {
				if busy == nil {
					if busy, err = s.busyPersonas(); err != nil {
						return errors.Join(append(errs, err)...)
					}
					if waiting, err = s.waitingStories(); err != nil {
						return errors.Join(append(errs, err)...)
					}
				}
				if !busy[f.Persona] {
					if story, err = s.pick(f, waiting); err != nil {
						errs = append(errs, fmt.Errorf("source %s: %w", src.Name(), err))
						continue
					}
				}
			}
			if story != "" {
				q := store.Queued{ID: s.newID(), Persona: f.Persona, Role: f.Role, Story: story, Reason: f.Reason, Created: now}
				if f.Context != "" {
					q.Context = []byte(f.Context)
				}
				if err := s.Store.Enqueue(q); err != nil {
					errs = append(errs, fmt.Errorf("source %s: queue %s/%s: %w", src.Name(), f.Persona, story, err))
					continue
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
