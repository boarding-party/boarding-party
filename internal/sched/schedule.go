package sched

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// Schedule is the trigger source for role activity patterns: each persona of
// a role with an activity acts when its stored due time passes, one story
// after another.
type Schedule struct {
	Store *store.Store
	Rand  func() float64 // nil = rand.Float64
}

func (*Schedule) Name() string { return "schedule" }

func (s *Schedule) rand() float64 {
	if s.Rand != nil {
		return s.Rand()
	}
	return rand.Float64()
}

func (s *Schedule) Due(_ context.Context, p project.Project, now time.Time) ([]Fire, error) {
	var names []string
	for _, r := range p.Roles {
		for _, ps := range r.Personas {
			names = append(names, ps.Name)
		}
	}
	if err := s.Store.DeleteDuesExcept(names); err != nil {
		return nil, err
	}
	dues, err := s.Store.Dues()
	if err != nil {
		return nil, err
	}
	loc := p.Location()
	var fires []Fire
	for _, r := range p.Roles {
		pat := r.Pattern()
		if pat.IsZero() || len(r.Stories) == 0 {
			continue
		}
		for _, ps := range r.Personas {
			d, ok := dues[ps.Name]
			if !ok || d.Activity != r.Activity {
				if err := s.Store.SetDue(store.Due{Persona: ps.Name, Activity: r.Activity, At: pat.Next(now, loc, s.rand)}); err != nil {
					return nil, err
				}
				continue
			}
			if d.At.After(now) {
				continue
			}
			f := Fire{Source: s.Name(), Persona: ps.Name, Role: r.Name, Reason: "Scheduled: " + r.Activity}
			for i := range r.Stories {
				f.Stories = append(f.Stories, r.Stories[(d.NextStory+i)%len(r.Stories)].Name)
			}
			fires = append(fires, f)
		}
	}
	return fires, nil
}

// Settle moves the persona's due on whatever became of the fire: a due missed
// while paused, over budget or busy is skipped, never caught up.
func (s *Schedule) Settle(f Fire, story string, now time.Time) error {
	p, err := s.Store.Project()
	if err != nil {
		return err
	}
	role, ok := p.Role(f.Role)
	if !ok {
		return fmt.Errorf("role %q is gone", f.Role)
	}
	dues, err := s.Store.Dues()
	if err != nil {
		return err
	}
	d, ok := dues[f.Persona]
	if !ok {
		return errors.New("no due for " + f.Persona)
	}
	pat := role.Pattern()
	// Daily jitter can fire early; look past the slot that just fired.
	d.At = pat.Next(now.Add(pat.Slack()), p.Location(), s.rand)
	if story != "" {
		for i, st := range role.Stories {
			if st.Name == story {
				d.NextStory = (i + 1) % len(role.Stories)
			}
		}
	}
	return s.Store.SetDue(d)
}
