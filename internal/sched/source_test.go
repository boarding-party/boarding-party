package sched

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
)

// fakeSource returns fixed fires and records what Settle was told.
type fakeSource struct {
	name    string
	fires   []Fire
	err     error
	settled []string
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Due(context.Context, project.Project, time.Time) ([]Fire, error) {
	return f.fires, f.err
}
func (f *fakeSource) Settle(_ Fire, story string, _ time.Time) error {
	f.settled = append(f.settled, story)
	return nil
}

func mailFire() Fire {
	return Fire{Source: "mail", Persona: "Cleo", Role: "clinician", Stories: []string{"approve"},
		Reason: "Mail from x", Context: "mail body"}
}

func TestFireQueuesWithContext(t *testing.T) {
	f := newFix(t, schedYAML)
	src := &fakeSource{name: "mail", fires: []Fire{mailFire()}}
	f.s.Sources = []Source{src}
	if err := f.s.fire(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	q := f.queue()
	if len(q) != 1 || q[0].Story != "approve" || q[0].Persona != "Cleo" || q[0].Role != "clinician" ||
		q[0].Reason != "Mail from x" || string(q[0].Context) != "mail body" || !q[0].Created.Equal(now) {
		t.Fatalf("queue = %+v", q)
	}
	if fmt.Sprint(src.settled) != "[approve]" {
		t.Fatalf("settled = %q", src.settled)
	}
}

func TestFireSkipsAndSettles(t *testing.T) {
	f := newFix(t, schedYAML)
	src := &fakeSource{name: "mail", fires: []Fire{mailFire()}}
	f.s.Sources = []Source{src}
	f.running("r1", "Cleo", "clinician", "approve", "j1")
	f.tick()
	if len(f.queue()) != 0 || fmt.Sprintf("%q", src.settled) != `[""]` {
		t.Fatalf("busy: queue %+v settled %q", f.queue(), src.settled)
	}

	g := newFix(t, schedYAML)
	src = &fakeSource{name: "mail", fires: []Fire{mailFire()}}
	g.s.Sources = []Source{src}
	g.starter.SetGate(runs.ErrPaused)
	g.tick()
	if len(g.queue()) != 0 || fmt.Sprintf("%q", src.settled) != `[""]` {
		t.Fatalf("gated: queue %+v settled %q", g.queue(), src.settled)
	}
}

func TestFireSourceErrorDoesNotStopOthers(t *testing.T) {
	f := newFix(t, schedYAML)
	bad := &fakeSource{name: "bad", err: errors.New("boom")}
	good := &fakeSource{name: "mail", fires: []Fire{mailFire()}}
	f.s.Sources = []Source{bad, good}
	err := f.s.Tick(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if q := f.queue(); len(q) != 0 {
		// Tick also drains; the fake starter consumed the item.
		t.Fatalf("queue = %+v", q)
	}
	if calls := f.starter.Calls(); len(calls) != 1 || calls[0].Story != "approve" {
		t.Fatalf("calls = %+v", calls)
	}
}
