package inbox

import (
	"context"
	"crypto/tls"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

// Retention of stored mail, and how often it is enforced.
const (
	Retention  = 30 * 24 * time.Hour
	pruneEvery = time.Hour
)

// Service polls the persona mailbox into the store and lets callers wait for mail.
type Service struct {
	Store   *store.Store
	Secrets Secrets
	TLS     *tls.Config   // nil = system roots; tests inject
	Every   time.Duration // between successful cycles; default 10s
	Backoff time.Duration // after a failed cycle; default 60s
	Now     func() time.Time
	// CheckFunc replaces the real dial and login in Check; the dev director and tests override it.
	CheckFunc func(context.Context, Config) error

	initOnce  sync.Once
	poke      chan struct{}
	forced    atomic.Bool // set by Kick: this poke overrides the error backoff
	cycleMu   sync.Mutex  // one cycle at a time
	mu        sync.Mutex
	status    Status
	wake      chan struct{} // closed and replaced by Notify
	lastPrune time.Time
}

func (s *Service) init() {
	s.initOnce.Do(func() {
		s.poke = make(chan struct{}, 1)
		s.mu.Lock()
		if s.wake == nil {
			s.wake = make(chan struct{})
		}
		if s.status.State == "" {
			s.status.State = "off"
		}
		s.mu.Unlock()
	})
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Run polls until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.init()
	for {
		wait := s.cycle(ctx)
		t := time.NewTimer(wait)
	sleep:
		for {
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-s.poke:
				// A plain poke must not defeat the backoff after a failed cycle: repeated
				// failed logins can get the mailbox locked. Kick overrides it.
				if s.Status().State == "error" && !s.forced.Swap(false) {
					continue
				}
				s.forced.Store(false)
				t.Stop()
				break sleep
			case <-t.C:
				break sleep
			}
		}
	}
}

// cycle runs one poll and returns how long to wait before the next.
func (s *Service) cycle(ctx context.Context) time.Duration {
	s.init()
	s.cycleMu.Lock()
	defer s.cycleMu.Unlock()
	every, backoff := orDefault(s.Every, 10*time.Second), orDefault(s.Backoff, 60*time.Second)

	c, ok, err := s.Secrets.Load(ctx)
	if err != nil {
		s.fail(scrub(err, c.Password))
		return backoff
	}
	if !ok {
		s.setStatus(Status{State: "off", Checked: s.now()})
		return every
	}
	if err := s.poll(ctx, c); err != nil {
		if ctx.Err() != nil {
			return every
		}
		s.fail(scrub(err, c.Password))
		return backoff
	}
	s.setStatus(Status{State: "ok", Checked: s.now()})
	s.prune()
	return every
}

func (s *Service) fail(err error) {
	log.Printf("inbox: %v", err) // already scrubbed; never log the Config
	s.setStatus(Status{State: "error", Error: err.Error(), Checked: s.now()})
}

func (s *Service) setStatus(st Status) {
	s.mu.Lock()
	s.status = st
	s.mu.Unlock()
}

func (s *Service) prune() {
	now := s.now()
	if !s.lastPrune.IsZero() && now.Sub(s.lastPrune) < pruneEvery {
		return
	}
	s.lastPrune = now
	if _, err := s.Store.PruneMessages(now.Add(-Retention)); err != nil {
		log.Printf("inbox: prune: %v", err)
	}
}

// Check dials and logs in with c, then logs out. Errors never contain the password.
func (s *Service) Check(ctx context.Context, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if s.CheckFunc != nil {
		return scrub(s.CheckFunc(ctx, c), c.Password)
	}
	cl, err := s.connect(ctx, c)
	if err != nil {
		return scrub(err, c.Password)
	}
	cl.Logout()
	return nil
}

// Status is the outcome of the last cycle.
func (s *Service) Status() Status {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Kick starts the next cycle now, even during the error backoff. Use it after the mailbox
// settings changed and the new login was already checked.
func (s *Service) Kick() {
	s.init()
	s.forced.Store(true)
	s.Poke()
}

// Poke starts the next cycle now, unless the last cycle failed and the backoff has not
// elapsed. It never blocks; pokes during a cycle coalesce into one.
func (s *Service) Poke() {
	s.init()
	select {
	case s.poke <- struct{}{}:
	default:
	}
}

// Notify wakes every Wait so it re-checks the store.
func (s *Service) Notify() {
	s.init()
	s.mu.Lock()
	close(s.wake)
	s.wake = make(chan struct{})
	s.mu.Unlock()
}

func (s *Service) wakeCh() <-chan struct{} {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wake
}

// Wait returns the newest delivered (unfiltered) message for persona stored at or after
// since that match accepts (nil accepts all). When ctx ends first it returns (zero, false, nil).
func (s *Service) Wait(ctx context.Context, persona string, since time.Time, match func(store.Message) bool) (store.Message, bool, error) {
	for {
		wake := s.wakeCh() // taken before the read so a message stored meanwhile still wakes us
		msgs, err := s.Store.Messages(store.MessageQuery{Persona: persona, Since: since, Limit: 200})
		if err != nil {
			return store.Message{}, false, err
		}
		for _, m := range msgs { // newest first
			if match == nil || match(m) {
				return m, true, nil
			}
		}
		select {
		case <-ctx.Done():
			return store.Message{}, false, nil
		case <-wake:
		}
	}
}
