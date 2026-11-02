// Package live holds the newest screen frame of each active run in memory and
// fans it out to watchers. Nothing is written to disk.
package live

import (
	"sync"
	"time"
)

// IdleTTL is how long a run's frame is kept after its last Publish.
const IdleTTL = 2 * time.Minute

type entry struct {
	frame []byte
	last  time.Time
	subs  map[chan []byte]struct{}
}

// Hub keeps at most one frame per run.
type Hub struct {
	mu   sync.Mutex
	runs map[string]*entry
	now  func() time.Time
}

func NewHub() *Hub { return &Hub{runs: map[string]*entry{}, now: time.Now} }

func (h *Hub) entry(id string) *entry {
	e := h.runs[id]
	if e == nil {
		e = &entry{subs: map[chan []byte]struct{}{}}
		h.runs[id] = e
	}
	return e
}

// push puts f in a capacity-1 channel, replacing any unread frame.
// Callers hold h.mu, so there is a single writer.
func push(ch chan []byte, f []byte) {
	select {
	case ch <- f:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- f:
	default:
	}
}

// Publish stores frame as the run's latest and returns the number of open
// subscriptions. The hub keeps frame; callers must not modify it afterwards.
func (h *Hub) Publish(runID string, frame []byte) (watchers int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.entry(runID)
	e.frame, e.last = frame, h.now()
	for ch := range e.subs {
		push(ch, frame)
	}
	return len(e.subs)
}

// Subscribe returns a capacity-1 channel that holds the latest frame (if any)
// straight away and then only ever the newest one. cancel is idempotent.
func (h *Hub) Subscribe(runID string) (<-chan []byte, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.entry(runID)
	if e.last.IsZero() {
		e.last = h.now() // keep the entry sweepable even if nothing is published
	}
	ch := make(chan []byte, 1)
	if e.frame != nil {
		ch <- e.frame
	}
	e.subs[ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if e := h.runs[runID]; e != nil {
			delete(e.subs, ch)
		}
	}
}

// HasFrame reports whether the run has a frame held right now.
func (h *Hub) HasFrame(runID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.runs[runID]
	return e != nil && e.frame != nil
}

// Sweep drops the frame of runs idle for IdleTTL, and the run itself unless
// it still has subscribers.
func (h *Hub) Sweep(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, e := range h.runs {
		if now.Sub(e.last) < IdleTTL {
			continue
		}
		if len(e.subs) > 0 {
			e.frame = nil // keep the entry so open streams still get later frames
			continue
		}
		delete(h.runs, id)
	}
}
