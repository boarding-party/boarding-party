package api

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

const journeyJS = "export default async function journey({ page, bp }) { await bp.step('Open', async () => {}); }"

// withJourney saves the project and gives ann's checkout journey one live revision.
func withJourney(t *testing.T, svc *runs.Service) store.Journey {
	t.Helper()
	p, err := project.Load([]byte(projYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	j, _, err := svc.Store.AddRevision("ann", "checkout", store.Revision{Kind: store.RevisionFull, Code: journeyJS, Reason: "r", RunID: "old", Created: time.Now()},
		store.Decision{Kind: "recorded", Reason: "r", Actor: store.ActorModel, Created: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestReplayWorkerRoutes(t *testing.T) {
	srv, wsrv, svc := setupBoth(t, 1<<20)
	j := withJourney(t, svc)
	r, err := svc.StartReplay(context.Background(), j.ID, 0, 5, "owner")
	if err != nil {
		t.Fatal(err)
	}
	resp := do(t, "GET", wsrv.URL+"/journey/"+r.Token, "")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != journeyJS || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("journey = %d %q %s", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	if r := do(t, "GET", wsrv.URL+"/journey/wrong", ""); r.StatusCode != 404 {
		t.Errorf("unknown token = %d", r.StatusCode)
	}
	for body, want := range map[string]int{
		`not json`:                         400,
		`{"status":"passed","extra":1}`:    400,
		`{"status":"maybe"}`:               400,
		`{"status":"passed","seed":5}`:     204,
		`{"status":"passed","seed":5,"x"}`: 400,
	} {
		if got := do(t, "PUT", wsrv.URL+"/replay/"+r.Token, body).StatusCode; got != want && !(want == 400 && got == 404) {
			t.Errorf("PUT %s = %d, want %d", body, got, want)
		}
	}
	got, _ := svc.Store.RunByID(r.ID)
	if got.State != store.RunDone || got.Outcome != "Passed" {
		t.Fatalf("run %+v", got)
	}
	if r := do(t, "GET", wsrv.URL+"/journey/"+r.Token, ""); r.StatusCode != 404 {
		t.Errorf("journey after the run = %d, want 404", r.StatusCode)
	}
	// An AI run's token gets no journey and cannot report a replay.
	ai := startRun(t, srv, svc)
	if r := do(t, "GET", wsrv.URL+"/journey/"+ai.Token, ""); r.StatusCode != 404 {
		t.Errorf("AI run journey = %d", r.StatusCode)
	}
	if r := do(t, "PUT", wsrv.URL+"/replay/"+ai.Token, `{"status":"passed"}`); r.StatusCode != 400 {
		t.Errorf("AI run replay report = %d", r.StatusCode)
	}
	big := `{"status":"failed","error":"` + strings.Repeat("x", 70<<10) + `"}`
	if r := do(t, "PUT", wsrv.URL+"/replay/"+ai.Token, big); r.StatusCode != 400 && r.StatusCode != 413 {
		t.Errorf("oversized report = %d", r.StatusCode)
	}
}
