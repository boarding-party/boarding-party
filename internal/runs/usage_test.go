package runs

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

func TestSaveUsage(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	r, _ := svc.Start(ctx, "ann", "checkout")

	if u, err := svc.Usage(r.ID); err != nil || u != nil {
		t.Fatalf("usage before any report = %v, %v", u, err)
	}
	if err := svc.SaveUsage(r.Token, Usage{Counts: Counts{Input: 1, Turns: 1}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// A later running total replaces the earlier one.
	want := Usage{Counts: Counts{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Turns: 5},
		ByModel: map[string]Counts{"claude-sonnet-5-5": {Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Turns: 5}}}
	if err := svc.SaveUsage(r.Token, want, time.Now()); err != nil {
		t.Fatal(err)
	}
	if u, err := svc.Usage(r.ID); err != nil || u == nil || !reflect.DeepEqual(*u, want) {
		t.Fatalf("usage = %v, %v", u, err)
	}

	if err := svc.Finish(ctx, r.Token, store.RunDone, "ok"); err != nil {
		t.Fatal(err)
	}
	done, _ := svc.Store.RunByID(r.ID)
	// The finishing turn's report may land just after the run is done.
	if err := svc.SaveUsage(r.Token, Usage{Counts: Counts{Turns: 6}}, done.Finished.Add(time.Minute)); err != nil {
		t.Fatalf("report within grace: %v", err)
	}
	if err := svc.SaveUsage(r.Token, Usage{Counts: Counts{Turns: 7}}, done.Finished.Add(3*time.Minute)); !errors.Is(err, ErrNotActive) {
		t.Fatalf("report after grace = %v, want ErrNotActive", err)
	}
	if u, _ := svc.Usage(r.ID); u == nil || u.Turns != 6 {
		t.Fatalf("usage after grace = %v", u)
	}
	for _, bad := range []map[string]Counts{
		{"Claude Sonnet": {}},
		{"a": {}, "b": {}, "c": {}, "d": {}, "e": {}, "f": {}, "g": {}, "h": {}, "i": {}},
	} {
		if err := svc.SaveUsage(r.Token, Usage{ByModel: bad}, done.Finished); !errors.Is(err, ErrBadUsage) {
			t.Errorf("models %v = %v, want ErrBadUsage", bad, err)
		}
	}
	if err := svc.SaveUsage("nope", Usage{}, time.Now()); !errors.Is(err, ErrNotActive) {
		t.Fatalf("unknown token = %v", err)
	}
}
