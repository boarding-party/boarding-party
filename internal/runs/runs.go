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
	"log"
	"strings"
	"sync"
	"time"

	"github.com/boarding-party/boardingparty/internal/agentkey"
	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/files"
	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/rewind"
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

// trimRewind drops the run's kept frames once it has a recording, which
// shows the same screen; a run without one keeps them as its replay.
func (s *Service) trimRewind(runID string) {
	if s.Rewind == nil || s.Artifacts == nil {
		return
	}
	names, err := s.Artifacts.List(runID)
	if err != nil {
		return
	}
	for _, n := range names {
		if strings.HasSuffix(n, ".webm") || strings.HasSuffix(n, ".mp4") {
			// The frames go, but the recording is placed on the run's clock
			// by when the first of them arrived.
			if at, err := s.Rewind.Index(runID); err == nil && len(at) > 0 {
				if err := s.Store.SetFirstFrame(runID, time.UnixMilli(at[0]).UTC()); err != nil {
					log.Printf("runs: keep first frame of %s: %v", runID, err)
				}
			}
			if err := s.Rewind.Remove(runID); err != nil {
				log.Printf("runs: drop frames of %s: %v", runID, err)
			}
			return
		}
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

// ErrNoAgentKey means agents have no Anthropic key yet; queued runs wait.
var ErrNoAgentKey = errors.New("no agent key")

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
	clash, err := s.childClash(p)
	if err != nil {
		return err
	}
	if clash != "" {
		return fmt.Errorf("%w: %s", ErrInvalid, clash)
	}
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
	// Rewind, when set, keeps the run's screen frames so the board can scroll
	// back through a live run; a run that ends with a recording drops them.
	Rewind *rewind.Store
	// Fence, when set, must return nil before a run starts: it checks that
	// agents can reach only the hosts someone allowed.
	Fence func(ctx context.Context) error
	// Egress, when set, keeps the project's hosts on the fence's allow-list
	// and says which hosts the fence refused during a run.
	Egress Egress
	// AgentKey, when set, must report a key before a run starts, and is what
	// the board's Agent key card reads and sets.
	AgentKey agentkey.Store
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
	return s.start(ctx, persona, story, nil, nil)
}

// StartQueued starts the run a queue item asks for. The run continues the
// item's chain and the item is taken in the same transaction that creates
// the run, so a refused start (busy, no fence) leaves it queued. It returns
// store.ErrNotFound if the item was already taken.
func (s *Service) StartQueued(ctx context.Context, q store.Queued) (store.Run, error) {
	if q.Kind == store.RunReplay {
		return s.startReplay(ctx, q)
	}
	if q.JourneyID != "" {
		// A copilot: nothing runs for a paused journey, and a deleted one
		// has nothing left to decide about.
		j, err := s.Store.Journey(q.JourneyID)
		if errors.Is(err, store.ErrNotFound) {
			return store.Run{}, fmt.Errorf("%w: its journey was deleted", ErrInvalid)
		}
		if err != nil {
			return store.Run{}, err
		}
		if j.State == store.JourneyPaused {
			return store.Run{}, fmt.Errorf("%w: its journey is paused", ErrInvalid)
		}
	}
	return s.start(ctx, q.Persona, q.Story, &q, nil)
}

// Enqueue queues a fresh run of persona doing story for the scheduler to
// start when nothing else runs. The run starts its own chain.
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

// start creates and launches a run. replay, when set, makes it a replay of
// that journey revision: no model, so no agent key and no budget check.
func (s *Service) start(ctx context.Context, persona, story string, q *store.Queued, replay *store.Run) (store.Run, error) {
	p, err := s.Store.Project()
	if err != nil {
		return store.Run{}, fmt.Errorf("load project: %w", err)
	}
	role, st, err := find(p, persona, story)
	if err != nil {
		return store.Run{}, err
	}
	if replay != nil {
		if err := s.pauseGate(); err != nil {
			return store.Run{}, err
		}
	} else {
		if err := s.Gate(time.Now()); err != nil {
			return store.Run{}, err
		}
		if err := s.LeadGate(p, persona, time.Now()); err != nil {
			return store.Run{}, err
		}
	}
	id, err := randHex(16)
	if err != nil {
		return store.Run{}, err
	}
	token, err := randHex(32)
	if err != nil {
		return store.Run{}, err
	}
	if s.AgentKey != nil && replay == nil {
		st, err := s.AgentKey.Status(ctx)
		if err != nil {
			return store.Run{}, fmt.Errorf("check the agent key: %w", err)
		}
		if !st.Set {
			return store.Run{}, fmt.Errorf("%w: set the Anthropic key in Project → Agent key", ErrNoAgentKey)
		}
	}
	if s.Fence != nil {
		if err := s.Fence(ctx); err != nil {
			return store.Run{}, fmt.Errorf("%w: %v", ErrNoFence, err)
		}
	}
	r := store.Run{ID: id, Token: token, Persona: persona, Role: role, Story: st,
		State: store.RunPending, Started: time.Now().UTC(), Chain: id, Profile: profileOf(p, role, persona)}
	queueID := ""
	if q != nil {
		r.Follows, r.Via, r.Context = q.Follows, q.Via, q.Context
		if q.Chain != "" {
			r.Chain = q.Chain
		}
		queueID = q.ID
	}
	if replay != nil {
		r.Kind, r.JourneyID, r.Revision, r.Seed = store.RunReplay, replay.JourneyID, replay.Revision, replay.Seed
		// The pod's command carries this one-time claim, not the token:
		// everything in the pod, the journey too, can read its command.
		if r.Claim, err = randHex(32); err != nil {
			return store.Run{}, err
		}
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
// profileOf is the profile of persona in role.
func profileOf(p project.Project, role, persona string) project.Profile {
	for _, r := range p.Roles {
		for _, pe := range r.Personas {
			if r.Name == role && pe.Name == persona {
				return pe.Profile
			}
		}
	}
	return project.Profile{}
}

func find(p project.Project, persona, story string) (role, storyName string, err error) {
	for _, r := range p.Roles {
		for _, pe := range r.Personas {
			if pe.Name != persona {
				continue
			}
			if pe.IsLead() != (story == project.LeadStory) {
				if pe.IsLead() {
					return "", "", fmt.Errorf("%w: %s is a lead and takes only lead runs", ErrInvalid, persona)
				}
				return "", "", fmt.Errorf("%w: %s is not a lead", ErrInvalid, persona)
			}
			if pe.IsLead() {
				return r.Name, story, nil
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
// Claim spends a replay run's one-time claim and returns the run, whose
// token its pod then keeps in memory only. A claim spent, unknown or of a
// run no longer active is ErrNotActive.
func (s *Service) Claim(code string) (store.Run, error) {
	r, err := s.Store.RedeemClaim(code)
	if errors.Is(err, store.ErrNotFound) {
		return store.Run{}, ErrNotActive
	}
	return r, err
}

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
	return s.FinishChecked(ctx, token, state, outcome, nil)
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
	return s.finish(ctx, r, store.RunFailed, "Stopped from the board.", nil)
}

// finish moves an active run to a final state (with a fix check's verdict,
// if any) and runs the shared tail.
func (s *Service) finish(ctx context.Context, r store.Run, state store.RunState, outcome string, v *store.Verdict) error {
	finished := time.Now().UTC()
	won, err := s.Store.FinishRunChecked(r.ID, state, outcome, finished, v)
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
	s.trimRewind(r.ID)
	s.keepBlocked(ctx, r, finished)
	defer s.finished()
	if r.Session == "" {
		return nil
	}
	return s.Launcher.Stop(ctx, r.Session)
}
