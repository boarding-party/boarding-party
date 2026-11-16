package runs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/store"
)

func TestSaveNarration(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	r, _ := svc.Start(ctx, "ann", "checkout")
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	if n, err := svc.Narration(r.ID); err != nil || n != nil {
		t.Fatalf("narration before any report = %v, %v", n, err)
	}
	if err := svc.SaveNarration(r.Token, []NarrationEntry{{At: at, Kind: "thought", Text: "a"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// A later list replaces the earlier one.
	want := []NarrationEntry{{At: at, Kind: "thought", Text: "Looking for the basket"}, {At: at.Add(time.Second), Kind: "action", Text: "browser_click: Basket"}}
	if err := svc.SaveNarration(r.Token, want, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Narration(r.ID)
	if err != nil || len(got) != 2 || got[1].Text != want[1].Text || !got[0].At.Equal(at) {
		t.Fatalf("narration = %v, %v", got, err)
	}

	if err := svc.Finish(ctx, r.Token, store.RunDone, "ok"); err != nil {
		t.Fatal(err)
	}
	done, _ := svc.Store.RunByID(r.ID)
	if err := svc.SaveNarration(r.Token, want, done.Finished.Add(time.Minute)); err != nil {
		t.Fatalf("report within grace: %v", err)
	}
	if err := svc.SaveNarration(r.Token, want, done.Finished.Add(3*time.Minute)); !errors.Is(err, ErrNotActive) {
		t.Fatalf("report after grace = %v, want ErrNotActive", err)
	}
	if err := svc.SaveNarration("nope", want, time.Now()); !errors.Is(err, ErrNotActive) {
		t.Fatalf("unknown token = %v", err)
	}
}

func TestNarrationBounds(t *testing.T) {
	svc, _ := newSvc(t)
	svc.Artifacts = artifacts.NewFS(t.TempDir(), 4<<20) // a full narration is far over the test store's 1 KiB
	r, _ := svc.Start(context.Background(), "ann", "checkout")
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	many := func(n int) []NarrationEntry {
		out := make([]NarrationEntry, n)
		for i := range out {
			out[i] = NarrationEntry{At: at, Kind: "action", Text: "x"}
		}
		return out
	}
	if err := svc.SaveNarration(r.Token, many(2000), time.Now()); err != nil {
		t.Fatalf("2000 entries: %v", err)
	}
	for name, n := range map[string][]NarrationEntry{
		"2001 entries": many(2001),
		"long text":    {{At: at, Kind: "thought", Text: strings.Repeat("é", 501)}},
		"empty text":   {{At: at, Kind: "thought", Text: ""}},
		"bad kind":     {{At: at, Kind: "shout", Text: "x"}},
		"no time":      {{Kind: "thought", Text: "x"}},
	} {
		if err := svc.SaveNarration(r.Token, n, time.Now()); !errors.Is(err, ErrBadNarration) {
			t.Errorf("%s = %v, want ErrBadNarration", name, err)
		}
	}
	// 500 characters fit, whatever their size in bytes.
	if err := svc.SaveNarration(r.Token, []NarrationEntry{{At: at, Kind: "thought", Text: strings.Repeat("é", 500)}}, time.Now()); err != nil {
		t.Fatalf("500 characters: %v", err)
	}
}
