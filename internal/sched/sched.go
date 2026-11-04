package sched

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

const (
	// MaxChainRuns caps the runs (started and queued) in one journey, so
	// stories that hand off to each other cannot loop forever.
	MaxChainRuns = 10
	DefaultEvery = 30 * time.Second
)

var chainFull = fmt.Sprintf("the chain reached %d runs", MaxChainRuns)

// Starter starts the run a queue item asks for (runs.Service does).
type Starter interface {
	StartQueued(ctx context.Context, q store.Queued) (store.Run, error)
}

// Scheduler hands stories off, resolves waits and starts queued runs, one at
// a time. It makes no model calls.
type Scheduler struct {
	Store *store.Store
	Runs  Starter
	Every time.Duration    // 0 = DefaultEvery
	Now   func() time.Time // nil = time.Now
	NewID func() string    // nil = 16 random bytes, hex

	mu       sync.Mutex // one Tick at a time
	pokeOnce sync.Once
	poke     chan struct{}
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Scheduler) newID() string {
	if s.NewID != nil {
		return s.NewID()
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails on supported platforms
	return hex.EncodeToString(b)
}

func (s *Scheduler) pokes() chan struct{} {
	s.pokeOnce.Do(func() { s.poke = make(chan struct{}, 1) })
	return s.poke
}

// Poke asks for a tick soon. It never blocks; pokes before the tick coalesce.
func (s *Scheduler) Poke() {
	select {
	case s.pokes() <- struct{}{}:
	default:
	}
}

// Run ticks at once, then every Every or on Poke, until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	every := s.Every
	if every <= 0 {
		every = DefaultEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Printf("scheduler: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.pokes():
		}
	}
}

// Tick hands off finished runs' stories, resolves waits, then starts at most
// one queued run. A failure in one phase does not skip the others.
func (s *Scheduler) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	return errors.Join(s.followAll(now), s.resolveAll(now), s.drain(ctx))
}

// followAll acts once on every done or waiting run not yet acted on.
func (s *Scheduler) followAll(now time.Time) error {
	un, err := s.Store.Unfollowed()
	if err != nil || len(un) == 0 {
		return err
	}
	p, err := s.Store.Project()
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("load project: %w", err)
	}
	var errs []error
	for _, r := range un {
		if err := s.follow(p, r, now); err != nil {
			errs = append(errs, fmt.Errorf("follow up run %s: %w", r.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Scheduler) follow(p project.Project, r store.Run, now time.Time) error {
	waiting := r.State == store.RunWaiting
	var targets []project.Handoff
	if waiting {
		w, err := s.Store.WaitFor(r.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err == nil && w.Kind == "handoff" && w.Status == store.WaitOpen {
			targets = []project.Handoff{{Role: w.Role, Story: w.Story}}
		}
	} else if st, ok := p.FindStory(r.Role, r.Story); ok {
		targets = st.Handoffs
	}
	items, stuck, err := s.handoffs(p, r, targets, waiting, now)
	if err != nil {
		return err
	}
	if stuck != "" {
		// The run leaves waiting, so it is no longer unfollowed even if the
		// FollowUp below never happens.
		if _, err := s.Store.ResolveWait(r.ID, s.stuck(r, stuck, now)); err != nil {
			return err
		}
	}
	return s.Store.FollowUp(r.ID, items)
}

// handoffs returns the queue items run r hands off to targets. For a waiting
// run, a handoff that cannot happen returns the reason its wait is stuck.
func (s *Scheduler) handoffs(p project.Project, r store.Run, targets []project.Handoff, waiting bool, now time.Time) ([]store.Queued, string, error) {
	if len(targets) == 0 {
		return nil, "", nil
	}
	chainRuns, chainQueued, err := s.chain(r.Journey)
	if err != nil {
		return nil, "", err
	}
	ctxJSON, err := json.Marshal(store.RunContext{Kind: "handoff", PreviousRun: r.ID, Previous: r.Outcome,
		From: &store.ContextFrom{Persona: r.Persona, Role: r.Role, Story: r.Story, Summary: r.Outcome}})
	if err != nil {
		return nil, "", err
	}
	var items []store.Queued
	for _, h := range targets {
		if handedOff(h, chainRuns, chainQueued, items) {
			continue
		}
		role, _ := p.Role(h.Role)
		if len(role.Personas) == 0 {
			msg := fmt.Sprintf("nobody in role %q to hand off to", h.Role)
			if waiting {
				return nil, msg, nil
			}
			log.Printf("run %s: %s", r.ID, msg)
			continue
		}
		if len(chainRuns)+len(chainQueued)+len(items) >= MaxChainRuns {
			if waiting {
				return nil, chainFull, nil
			}
			log.Printf("run %s: %s; not handing off to %s/%s", r.ID, chainFull, h.Role, h.Story)
			break
		}
		items = append(items, store.Queued{ID: s.newID(), Persona: role.Personas[0].Name, Role: h.Role, Story: h.Story,
			Journey: r.Journey, Follows: r.ID, Via: "handoff", Context: ctxJSON,
			Reason: fmt.Sprintf("Handed off by %s (%s)", r.Persona, r.Story), Created: now})
	}
	return items, "", nil
}

// handedOff reports whether h was already handed off in this chain.
func handedOff(h project.Handoff, rs []store.Run, qs, pending []store.Queued) bool {
	for _, x := range rs {
		if x.Via == "handoff" && x.Role == h.Role && x.Story == h.Story {
			return true
		}
	}
	for _, list := range [][]store.Queued{qs, pending} {
		for _, x := range list {
			if x.Via == "handoff" && x.Role == h.Role && x.Story == h.Story {
				return true
			}
		}
	}
	return false
}

// chain returns the runs and queued items of a journey.
func (s *Scheduler) chain(journey string) ([]store.Run, []store.Queued, error) {
	rs, err := s.Store.RunsInJourney(journey)
	if err != nil {
		return nil, nil, err
	}
	all, err := s.Store.Queue()
	if err != nil {
		return nil, nil, err
	}
	var qs []store.Queued
	for _, q := range all {
		if q.Journey == journey {
			qs = append(qs, q)
		}
	}
	return rs, qs, nil
}

func (s *Scheduler) stuck(r store.Run, detail string, now time.Time) store.Resolution {
	return store.Resolution{Status: store.WaitStuck, Detail: detail, Ended: now,
		Finding: &store.Bug{ID: s.newID(), RunID: r.ID, Summary: detail, Step: "waiting", Severity: "medium",
			Note: r.Outcome, Created: now}}
}

// resolveAll ends every open wait whose condition happened or whose deadline passed.
func (s *Scheduler) resolveAll(now time.Time) error {
	ws, err := s.Store.OpenWaits()
	if err != nil {
		return err
	}
	var errs []error
	for _, w := range ws {
		if err := s.resolve(w, now); err != nil {
			errs = append(errs, fmt.Errorf("wait of run %s: %w", w.RunID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Scheduler) resolve(w store.Wait, now time.Time) error {
	r, err := s.Store.RunByID(w.RunID)
	if err != nil {
		return err
	}
	a, met, err := Met(s.Store, w, r, now)
	if err != nil {
		return err
	}
	if !met {
		if now.Before(w.Deadline) {
			return nil
		}
		_, err := s.Store.ResolveWait(r.ID, s.stuck(r, StuckText(w), now))
		return err
	}
	rs, qs, err := s.chain(r.Journey)
	if err != nil {
		return err
	}
	if len(rs)+len(qs) >= MaxChainRuns {
		_, err := s.Store.ResolveWait(r.ID, s.stuck(r, chainFull, now))
		return err
	}
	ctxJSON, err := json.Marshal(store.RunContext{Kind: "continue", WaitedFor: Describe(w), Arrived: a.Detail,
		MessageID: a.MessageID, PreviousRun: r.ID, Previous: r.Outcome})
	if err != nil {
		return err
	}
	next := store.Queued{ID: s.newID(), Persona: r.Persona, Role: r.Role, Story: r.Story, Journey: r.Journey,
		Follows: r.ID, Via: "continue", Context: ctxJSON, Reason: "Continues after " + a.Detail, Created: now}
	_, err = s.Store.ResolveWait(r.ID, store.Resolution{Status: store.WaitMet, Detail: a.Detail, Ended: now, Next: &next})
	return err
}

// drain starts the oldest queued run that can start, at most one per tick.
func (s *Scheduler) drain(ctx context.Context) error {
	queue, err := s.Store.Queue()
	if err != nil {
		return err
	}
	for _, q := range queue {
		_, err := s.Runs.StartQueued(ctx, q)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, runs.ErrBusy), errors.Is(err, runs.ErrNoFence):
			return nil // the item stays for a later tick
		case errors.Is(err, runs.ErrInvalid):
			if derr := s.Store.DeleteQueued(q.ID); derr != nil && !errors.Is(derr, store.ErrNotFound) {
				return derr
			}
			log.Printf("dropped queued %s/%s: %v", q.Persona, q.Story, err)
		case errors.Is(err, store.ErrNotFound):
			// Already taken; try the next.
		default:
			// Either the run was created and failed to launch (it is recorded
			// as failed and the item is taken) or nothing was written; both
			// end this tick.
			return fmt.Errorf("start queued %s/%s: %w", q.Persona, q.Story, err)
		}
	}
	return nil
}
