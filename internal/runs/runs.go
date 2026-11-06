// Package runs creates and finishes persona runs.
package runs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/files"
	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// ErrNotActive means the token is unknown or its run has already finished.
// Defaults for the run watchdog.
const (
	DefaultRunTimeout   = 20 * time.Minute
	DefaultPollInterval = 15 * time.Second
)

var ErrNotActive = errors.New("run not active")

// LastFrameName is the artifact holding the screen a run ended on.
const LastFrameName = "last-frame.jpg"

// keepLastFrame saves the run's newest live frame, if any. Best effort: a run
// without a frame simply has no picture.
func (s *Service) keepLastFrame(runID string) {
	if s.LastFrame == nil || s.Artifacts == nil {
		return
	}
	if f := s.LastFrame(runID); len(f) > 0 {
		_, _ = s.Artifacts.Put(runID, LastFrameName, bytes.NewReader(f))
	}
}

// ErrInvalid means the request names a persona or story that does not exist.
var ErrInvalid = errors.New("invalid run request")

// ErrBusy means another run is pending or running. Plan 1 runs one at a time
// because a session can read every other session's spec (and so its token).
var ErrBusy = errors.New("another run is active; Plan 1 runs one at a time")

// ErrNoFence means runs can't start because nothing limits which hosts an
// agent can reach.
var ErrNoFence = errors.New("runs are not fenced")

// ErrBadState means Finish was given a state that is not final.
var ErrBadState = errors.New("finish state must be done, stuck or failed")

type Launcher interface {
	Start(ctx context.Context, r store.Run, p project.Project) (session string, err error)
	Stop(ctx context.Context, session string) error
	// SessionName is the deterministic session name Start gives a run, so an
	// orphan can be stopped even if its name was never recorded.
	SessionName(runID string) string
	// Phase reports the session's phase (Pending, Running, Done, Failed), whether
	// it still exists, and its status message.
	Phase(ctx context.Context, session string) (phase string, exists bool, message string, err error)
	// Sessions lists the run sessions that exist, as session name -> run ID.
	Sessions(ctx context.Context) (map[string]string, error)
	// LivePods counts the pods of run sessions that still exist. A deleted
	// session's pod outlives it briefly.
	LivePods(ctx context.Context) (int, error)
	// Stage says how far a session's pod has got: "starting" (no pod yet, or
	// image pull and init), "waiting-for-node" (unscheduled) or "running".
	// "failing" carries the reason a container cannot start.
	Stage(ctx context.Context, session string) (stage, reason string, err error)
}

// Egress is the fence's allow-list and refusal log (see package egress).
type Egress interface {
	Sync(ctx context.Context, hosts []string) error
	Denied(ctx context.Context, since, until time.Time) ([]string, error)
}

// BlockedName is the artifact listing the hosts a finished run was refused.
const BlockedName = "blocked-hosts.json"

// ErrAllowList means the project was saved but the fence's allow-list was not updated.
var ErrAllowList = errors.New("project saved, but the egress allow-list was not updated")

// SaveProject stores p and puts its hosts on the egress allow-list.
func (s *Service) SaveProject(ctx context.Context, p project.Project) error {
	if err := s.Store.PutProject(p); err != nil {
		return err
	}
	return s.SyncHosts(ctx, p)
}

// SyncHosts puts p's hosts on the egress allow-list.
func (s *Service) SyncHosts(ctx context.Context, p project.Project) error {
	if s.Egress == nil {
		return nil
	}
	if err := s.Egress.Sync(ctx, p.Hosts()); err != nil {
		return fmt.Errorf("%w: %v", ErrAllowList, err)
	}
	return nil
}

// Blocked returns the hosts the fence refused during r: saved with a finished
// run, read from the proxy while it runs.
func (s *Service) Blocked(ctx context.Context, r store.Run) ([]string, error) {
	if !r.Finished.IsZero() {
		if s.Artifacts == nil {
			return nil, nil
		}
		rc, err := s.Artifacts.Open(r.ID, BlockedName)
		if errors.Is(err, artifacts.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		var hosts []string
		err = json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&hosts)
		return hosts, err
	}
	if s.Egress == nil {
		return nil, nil
	}
	return s.Egress.Denied(ctx, r.Started, time.Time{})
}

// keepBlocked saves the hosts the fence refused during the run. Best effort,
// like the last frame: a run whose refusals can't be read has no list.
func (s *Service) keepBlocked(ctx context.Context, r store.Run, finished time.Time) {
	if s.Egress == nil || s.Artifacts == nil {
		return
	}
	hosts, err := s.Egress.Denied(ctx, r.Started, finished)
	if err != nil || len(hosts) == 0 {
		return
	}
	b, err := json.Marshal(hosts)
	if err == nil {
		_, _ = s.Artifacts.Put(r.ID, BlockedName, bytes.NewReader(b))
	}
}

type Service struct {
	Store     *store.Store
	Artifacts artifacts.Store
	Files     *files.Store // test fixtures personas may upload in the site; may be nil
	Launcher  Launcher
	// LastFrame returns the run's newest live screen frame, or nil. When set,
	// a finished run keeps it as the artifact LastFrameName.
	LastFrame func(runID string) []byte
	// Fence, when set, must return nil before a run starts: it checks that
	// agents can reach only the hosts someone allowed.
	Fence func(ctx context.Context) error
	// Egress, when set, keeps the project's hosts on the fence's allow-list
	// and says which hosts the fence refused during a run.
	Egress Egress
	// OnFinish, when set, is called after any run leaves the active states.
	OnFinish func()

	startMu sync.Mutex // makes the one-active-run check and CreateRun atomic
	usageMu sync.Mutex // makes SaveUsage's read-merge-write atomic
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Start creates a run for persona doing story and launches its session.
func (s *Service) Start(ctx context.Context, persona, story string) (store.Run, error) {
	return s.start(ctx, persona, story, nil)
}

// StartQueued starts the run a queue item asks for. The run continues the
// item's journey and the item is taken in the same transaction that creates
// the run, so a refused start (busy, no fence) leaves it queued. It returns
// store.ErrNotFound if the item was already taken.
func (s *Service) StartQueued(ctx context.Context, q store.Queued) (store.Run, error) {
	return s.start(ctx, q.Persona, q.Story, &q)
}

// Enqueue queues a fresh run of persona doing story for the scheduler to
// start when nothing else runs. The run starts its own journey.
func (s *Service) Enqueue(persona, story string, now time.Time) (store.Queued, error) {
	p, err := s.Store.Project()
	if err != nil {
		return store.Queued{}, fmt.Errorf("load project: %w", err)
	}
	role, st, err := find(p, persona, story)
	if err != nil {
		return store.Queued{}, err
	}
	id, err := randHex(16)
	if err != nil {
		return store.Queued{}, err
	}
	q := store.Queued{ID: id, Persona: persona, Role: role, Story: st, Reason: "Started from the board", Created: now}
	return q, s.Store.Enqueue(q)
}

func (s *Service) finished() {
	if s.OnFinish != nil {
		s.OnFinish()
	}
}

func (s *Service) start(ctx context.Context, persona, story string, q *store.Queued) (store.Run, error) {
	p, err := s.Store.Project()
	if err != nil {
		return store.Run{}, fmt.Errorf("load project: %w", err)
	}
	role, st, err := find(p, persona, story)
	if err != nil {
		return store.Run{}, err
	}
	if err := s.Gate(time.Now()); err != nil {
		return store.Run{}, err
	}
	id, err := randHex(16)
	if err != nil {
		return store.Run{}, err
	}
	token, err := randHex(32)
	if err != nil {
		return store.Run{}, err
	}
	if s.Fence != nil {
		if err := s.Fence(ctx); err != nil {
			return store.Run{}, fmt.Errorf("%w: %v", ErrNoFence, err)
		}
	}
	r := store.Run{ID: id, Token: token, Persona: persona, Role: role, Story: st,
		State: store.RunPending, Started: time.Now().UTC(), Journey: id}
	queueID := ""
	if q != nil {
		r.Follows, r.Via, r.Context = q.Follows, q.Via, q.Context
		if q.Journey != "" {
			r.Journey = q.Journey
		}
		queueID = q.ID
	}
	s.startMu.Lock()
	active, err := s.Store.ActiveRuns()
	if err == nil && len(active) > 0 {
		err = ErrBusy
	}
	if err == nil {
		// A finished run's session may still be alive (failed Stop, deletion in
		// progress) and could read the next run's token.
		var sessions map[string]string
		if sessions, err = s.Launcher.Sessions(ctx); err == nil && len(sessions) > 0 {
			err = ErrBusy
		}
	}
	if err == nil {
		// Deleting a Session returns before its pod is gone.
		var pods int
		if pods, err = s.Launcher.LivePods(ctx); err == nil && pods > 0 {
			err = ErrBusy
		}
	}
	if err == nil {
		err = s.Store.CreateRunFrom(r, queueID)
	}
	s.startMu.Unlock()
	if err != nil {
		return store.Run{}, err
	}
	session, err := s.Launcher.Start(ctx, r, p)
	if err != nil {
		r.State, r.Outcome, r.Finished = store.RunFailed, err.Error(), time.Now().UTC()
		if _, ferr := s.Store.FinishRun(r.ID, store.RunFailed, r.Outcome, r.Finished); ferr != nil {
			return r, errors.Join(err, ferr)
		}
		s.finished()
		return r, err
	}
	won, err := s.Store.SetRunSession(r.ID, session)
	if err != nil {
		return r, err
	}
	if !won {
		// Finished (or failed by the watchdog) while launching: don't leak the session.
		return r, errors.Join(ErrNotActive, s.Launcher.Stop(ctx, session))
	}
	r.State, r.Session = store.RunRunning, session
	return r, nil
}

// find returns the role owning persona, and the goal of the named story in that role.
func find(p project.Project, persona, story string) (role, storyName string, err error) {
	for _, r := range p.Roles {
		for _, pe := range r.Personas {
			if pe.Name != persona {
				continue
			}
			for _, st := range r.Stories {
				if st.Name == story {
					return r.Name, st.Name, nil
				}
			}
			return "", "", fmt.Errorf("%w: persona %q (role %q) has no story %q", ErrInvalid, persona, r.Name, story)
		}
	}
	return "", "", fmt.Errorf("%w: unknown persona %q", ErrInvalid, persona)
}

// Active returns the run for token if it is pending or running.
func (s *Service) Active(token string) (store.Run, error) {
	r, err := s.Store.RunByToken(token)
	if errors.Is(err, store.ErrNotFound) {
		return store.Run{}, ErrNotActive
	}
	if err != nil {
		return store.Run{}, err
	}
	if r.State != store.RunPending && r.State != store.RunRunning {
		return store.Run{}, ErrNotActive
	}
	return r, nil
}

// Finish records the final state and stops the run's session. Only the
// caller that actually moves the run out of an active state stops the session.
// A waiting run needs a condition: see FinishWaiting.
func (s *Service) Finish(ctx context.Context, token string, state store.RunState, outcome string) error {
	switch state {
	case store.RunDone, store.RunStuck, store.RunFailed:
	default:
		return fmt.Errorf("%w: got %q", ErrBadState, state)
	}
	r, err := s.Active(token)
	if err != nil {
		return err
	}
	return s.finish(ctx, r, state, outcome)
}

// StopRun ends an active run from the board: failed, session stopped,
// artifacts kept, scheduler poked. A run that is not active is ErrNotActive.
func (s *Service) StopRun(ctx context.Context, id string) error {
	r, err := s.Store.RunByID(id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotActive
	}
	if err != nil {
		return err
	}
	if r.State != store.RunPending && r.State != store.RunRunning {
		return ErrNotActive
	}
	return s.finish(ctx, r, store.RunFailed, "Stopped from the board.")
}

// finish moves an active run to a final state and runs the shared tail.
func (s *Service) finish(ctx context.Context, r store.Run, state store.RunState, outcome string) error {
	finished := time.Now().UTC()
	won, err := s.Store.FinishRun(r.ID, state, outcome, finished)
	if err != nil {
		return err
	}
	if !won {
		return ErrNotActive
	}
	return s.ended(ctx, r, finished)
}

// FinishWaiting ends the run as waiting on w and stops its session.
func (s *Service) FinishWaiting(ctx context.Context, token string, w store.Wait, outcome string) error {
	r, err := s.Active(token)
	if err != nil {
		return err
	}
	finished := time.Now().UTC()
	w.RunID = r.ID
	won, err := s.Store.FinishRunWaiting(r.ID, outcome, finished, w)
	if err != nil {
		return err
	}
	if !won {
		return ErrNotActive
	}
	return s.ended(ctx, r, finished)
}

// ended is the tail shared by every finish: keep what the session saw, stop
// it, and tell the scheduler a slot is free.
func (s *Service) ended(ctx context.Context, r store.Run, finished time.Time) error {
	s.keepLastFrame(r.ID)
	s.keepBlocked(ctx, r, finished)
	defer s.finished()
	if r.Session == "" {
		return nil
	}
	return s.Launcher.Stop(ctx, r.Session)
}
