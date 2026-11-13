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
	// RetryWhenBusy keeps a fire that meets a busy persona unsettled, so the
	// source offers it again on a later tick. The source bounds how long.
	RetryWhenBusy bool
}

// Source is anything that can ask for a run: a schedule, a mailbox, a calendar.
type Source interface {
	Name() string
	// Due returns the fires that are due at now. It makes no model calls.
	Due(ctx context.Context, p project.Project, now time.Time) ([]Fire, error)
	// Settle tells the source what became of a fire: story is the one queued,
	// or "" when it was skipped (gated, persona busy, no eligible story).
	// A source advances its own cursor here; a skipped fire is never replayed.
	// A RetryWhenBusy fire that meets a busy persona is not settled.
	// Settle is called exactly once per fire, when the scheduler has decided
	// its fate. If the scheduler itself fails on a fire (a store error), it
	// does not settle it, so the source offers the fire again on a later tick.
	Settle(f Fire, story string, now time.Time) error
}

// Triggers returns the sources the director runs, in the order they claim a
// persona: an event has a fixed time, a message waits up to LookBack, and the
// schedule just moves on to its next slot. rnd jitters the schedule; nil =
// rand.Float64.
func Triggers(st *store.Store, rnd func() float64) []Source {
	return []Source{&Calendar{Store: st}, &Messages{Store: st}, &Schedule{Store: st, Rand: rnd}}
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
// Earlier sources win a persona that several want in the same tick.
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
	// Over budget, stories that would replay still fire: replays never spend.
	gerr := s.Runs.Gate(now)
	gated := errors.Is(gerr, runs.ErrPaused)
	onlyReplays := errors.Is(gerr, runs.ErrOverBudget)
	if gerr != nil && !gated && !onlyReplays {
		return fmt.Errorf("gate: %w", gerr)
	}
	var busy, waiting map[string]bool
	if !gated {
		if busy, err = s.busyPersonas(onlyReplays); err != nil {
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
			if !gated && busy[f.Persona] && f.RetryWhenBusy {
				continue // unsettled: offered again once the persona is free
			}
			var c choice
			if !gated && !busy[f.Persona] {
				if c, err = s.pick(p, f, waiting, now, onlyReplays); err != nil {
					errs = append(errs, fmt.Errorf("source %s: %w", src.Name(), err))
					continue // unsettled: offered again next tick
				}
			}
			story := c.story
			if story != "" {
				q := store.Queued{ID: s.newID(), Persona: f.Persona, Role: f.Role, Story: story, Reason: f.Reason,
					Context: f.Context, Created: now}
				if c.replay != nil {
					seed, err := runs.NewSeed()
					if err != nil {
						errs = append(errs, err)
						continue
					}
					q.Kind, q.JourneyID, q.Seed = store.RunReplay, c.replay.ID, seed
					q.Reason = "Scheduled replay: " + roleNamed(p, f.Role).Activity
				}
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
// onlyReplays (over budget) leaves out queued AI items: they wait for
// tomorrow and must not hold up the day's replays.
func (s *Scheduler) busyPersonas(onlyReplays bool) (map[string]bool, error) {
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
		if onlyReplays && q.Kind != store.RunReplay {
			continue
		}
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

// choice is the story a fire runs, and the journey it replays (nil: an AI
// run).
type choice struct {
	story  string
	replay *store.Journey
}

// pick returns the first story of f the persona can start, "" if none. A
// story whose journey is paused or repairing is not eligible: nothing runs
// for it, never an AI run instead. A scheduled fire (no context) replays a
// story's live journey until the role's explore interval has passed since
// its last fresh AI run; onlyReplays (over budget) takes only such stories.
func (s *Scheduler) pick(p project.Project, f Fire, waiting map[string]bool, now time.Time, onlyReplays bool) (choice, error) {
	role := roleNamed(p, f.Role)
	for _, st := range f.Stories {
		if waiting[f.Persona+"/"+st] {
			continue
		}
		last, err := s.Store.LastRun(f.Persona, st)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return choice{}, err
		}
		if err == nil && last.State == store.RunStuck && last.Kind != store.RunReplay {
			continue // a replay's missed wait goes to the copilot instead
		}
		j, err := s.Store.JourneyFor(f.Persona, st)
		if errors.Is(err, store.ErrNotFound) {
			if onlyReplays {
				continue
			}
			return choice{story: st}, nil
		}
		if err != nil {
			return choice{}, err
		}
		if j.State == store.JourneyPaused || j.State == store.JourneyRepairing {
			continue
		}
		if f.Context == nil && storyOf(role, st).Replays() && j.LiveRev > 0 && now.Sub(j.LastExplored) < role.ExploreEvery() {
			return choice{story: st, replay: &j}, nil
		}
		if onlyReplays {
			continue
		}
		return choice{story: st}, nil
	}
	return choice{}, nil
}

func roleNamed(p project.Project, name string) project.Role {
	for _, r := range p.Roles {
		if r.Name == name {
			return r
		}
	}
	return project.Role{}
}

func storyOf(r project.Role, name string) project.Story {
	for _, st := range r.Stories {
		if st.Name == name {
			return st
		}
	}
	return project.Story{}
}
