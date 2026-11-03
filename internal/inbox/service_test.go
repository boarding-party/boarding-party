package inbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

func put(t *testing.T, s *Service, id string, uid uint32, persona, subject, filtered string) {
	t.Helper()
	if _, err := s.Store.AddMessage(store.Message{ID: id, UIDValidity: 1, UID: uid, Persona: persona,
		From: "no-reply@smesolutions.uk", Subject: subject, Stored: time.Now(), Filtered: filtered}); err != nil {
		t.Fatal(err)
	}
	s.Notify()
}

func TestWaitReturnsMatchingMessage(t *testing.T) {
	s := newService(t, nil, "")
	since := time.Now()
	put(t, s, "old", 1, "John", "verify old", "") // before since: ignored
	time.Sleep(time.Millisecond)
	since = time.Now()

	type result struct {
		m   store.Message
		ok  bool
		err error
	}
	res := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		m, ok, err := s.Wait(ctx, "John", since, func(m store.Message) bool { return strings.Contains(m.Subject, "verify") })
		res <- result{m, ok, err}
	}()

	time.Sleep(20 * time.Millisecond)
	put(t, s, "spam", 2, "John", "verify spam", "sender not allowed")
	put(t, s, "ada", 3, "Ada", "verify ada", "")
	put(t, s, "other", 4, "John", "newsletter", "")
	select {
	case r := <-res:
		t.Fatalf("Wait returned early: %+v", r)
	case <-time.After(20 * time.Millisecond):
	}
	put(t, s, "want", 5, "John", "please verify", "")
	select {
	case r := <-res:
		if r.err != nil || !r.ok || r.m.ID != "want" {
			t.Fatalf("Wait = %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not wake")
	}

	// Already stored: returns at once.
	m, ok, err := s.Wait(ctx, "John", since, nil)
	if err != nil || !ok || m.ID != "want" {
		t.Fatalf("Wait on stored = %+v %v %v", m, ok, err)
	}
}

func TestWaitTimesOut(t *testing.T) {
	s := newService(t, nil, "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	m, ok, err := s.Wait(ctx, "John", time.Now(), nil)
	if err != nil || ok || m.ID != "" {
		t.Fatalf("Wait = %+v %v %v, want zero, false, nil", m, ok, err)
	}
}
