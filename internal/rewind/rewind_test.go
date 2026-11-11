package rewind

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func jpeg(b byte) []byte { return []byte{0xFF, 0xD8, b, b, b} }

var t0 = time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)

func TestKeepsAtMostOneFramePerInterval(t *testing.T) {
	s := New(t.TempDir(), 1<<20)
	for i := 0; i < 8; i++ { // four frames a second for two seconds
		if err := s.Append("r1", t0.Add(time.Duration(i)*250*time.Millisecond), jpeg(byte(i))); err != nil {
			t.Fatal(err)
		}
	}
	at, err := s.Index("r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(at) != 2 || at[0] != t0.UnixMilli() || at[1] != t0.Add(time.Second).UnixMilli() {
		t.Fatalf("index %v", at)
	}
	f, err := s.Frame("r1", 1)
	if err != nil || !bytes.Equal(f, jpeg(4)) {
		t.Fatalf("frame 1 = %v %v", f, err)
	}
}

func TestSkipsUnchangedFrames(t *testing.T) {
	s := New(t.TempDir(), 1<<20)
	s.Append("r1", t0, jpeg(1))
	s.Append("r1", t0.Add(2*time.Second), jpeg(1)) // the worker's keepalive resend
	s.Append("r1", t0.Add(4*time.Second), jpeg(2))
	if at, _ := s.Index("r1"); len(at) != 2 {
		t.Fatalf("index %v", at)
	}
}

func TestStopsAtTheCap(t *testing.T) {
	s := New(t.TempDir(), 12) // room for two 5-byte frames
	for i := 0; i < 4; i++ {
		s.Append("r1", t0.Add(time.Duration(i)*time.Second), jpeg(byte(i)))
	}
	if at, _ := s.Index("r1"); len(at) != 2 {
		t.Fatalf("index %v", at)
	}
}

func TestSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	New(dir, 1<<20).Append("r1", t0, jpeg(1))
	s := New(dir, 1<<20)
	s.Append("r1", t0.Add(300*time.Millisecond), jpeg(2)) // still inside the interval
	s.Append("r1", t0.Add(2*time.Second), jpeg(3))
	at, _ := s.Index("r1")
	if len(at) != 2 {
		t.Fatalf("index %v", at)
	}
	if f, _ := s.Frame("r1", 1); !bytes.Equal(f, jpeg(3)) {
		t.Fatalf("frame 1 = %v", f)
	}
}

func TestUnknownRunAndFrame(t *testing.T) {
	s := New(t.TempDir(), 1<<20)
	if at, err := s.Index("nope"); err != nil || len(at) != 0 {
		t.Fatalf("%v %v", at, err)
	}
	s.Append("r1", t0, jpeg(1))
	if _, err := s.Frame("r1", 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v", err)
	}
	if _, err := s.Frame("../etc", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad id: %v", err)
	}
}

func TestRemove(t *testing.T) {
	s := New(t.TempDir(), 1<<20)
	s.Append("r1", t0, jpeg(1))
	if err := s.Remove("r1"); err != nil {
		t.Fatal(err)
	}
	if at, _ := s.Index("r1"); len(at) != 0 {
		t.Fatalf("index %v", at)
	}
}
