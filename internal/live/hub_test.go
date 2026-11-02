package live

import (
	"testing"
	"time"
)

func TestSubscribeGetsLatestImmediately(t *testing.T) {
	h := NewHub()
	h.Publish("r", []byte("a"))
	ch, cancel := h.Subscribe("r")
	defer cancel()
	select {
	case f := <-ch:
		if string(f) != "a" {
			t.Fatalf("got %q", f)
		}
	default:
		t.Fatal("no immediate frame")
	}
}

func TestSlowSubscriberGetsNewestOnly(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("r")
	defer cancel()
	for _, s := range []string{"1", "2", "3"} {
		h.Publish("r", []byte(s))
	}
	if f := <-ch; string(f) != "3" {
		t.Fatalf("got %q", f)
	}
	select {
	case f := <-ch:
		t.Fatalf("extra frame %q", f)
	default:
	}
}

func TestPublishCountsWatchers(t *testing.T) {
	h := NewHub()
	if n := h.Publish("r", []byte("a")); n != 0 {
		t.Fatalf("want 0, got %d", n)
	}
	_, cancel := h.Subscribe("r")
	if n := h.Publish("r", []byte("a")); n != 1 {
		t.Fatalf("want 1, got %d", n)
	}
	cancel()
	cancel()
	if n := h.Publish("r", []byte("a")); n != 0 {
		t.Fatalf("want 0, got %d", n)
	}
}

func TestSweepDropsIdleRuns(t *testing.T) {
	h := NewHub()
	h.Publish("r", []byte("a"))
	h.Sweep(time.Now().Add(time.Minute))
	if ch, c := h.Subscribe("r"); len(ch) != 1 {
		t.Fatal("swept too early")
	} else {
		c()
	}
	h.Sweep(time.Now().Add(3 * time.Minute))
	ch, c := h.Subscribe("r")
	defer c()
	if len(ch) != 0 {
		t.Fatal("frame survived sweep")
	}
}

func TestSweepKeepsSubscribedRuns(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("r")
	defer cancel()
	h.Sweep(time.Now().Add(3 * time.Minute))
	if n := h.Publish("r", []byte("late")); n != 1 {
		t.Fatalf("watchers = %d, want 1", n)
	}
	select {
	case f := <-ch:
		if string(f) != "late" {
			t.Fatalf("got %q", f)
		}
	default:
		t.Fatal("subscriber got nothing")
	}
}

func TestHasFrame(t *testing.T) {
	h := NewHub()
	if h.HasFrame("r") {
		t.Fatal("frame before publish")
	}
	_, cancel := h.Subscribe("r")
	defer cancel()
	if h.HasFrame("r") {
		t.Fatal("subscribe alone is not a frame")
	}
	h.Publish("r", []byte("f"))
	if !h.HasFrame("r") {
		t.Fatal("no frame after publish")
	}
}
