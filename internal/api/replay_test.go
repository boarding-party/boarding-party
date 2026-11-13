package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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
		store.Decision{Kind: "recorded", Reason: "r", Actor: store.ActorModel, Created: time.Now(), Explored: true})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// startReplay queues a replay of j and starts it, as the scheduler does.
func startReplay(t *testing.T, svc *runs.Service, j store.Journey, seed int64) store.Run {
	t.Helper()
	q, err := svc.EnqueueReplay(j.ID, 0, seed, "owner", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.StartQueued(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReplayWorkerRoutes(t *testing.T) {
	srv, wsrv, svc := setupBoth(t, 1<<20)
	j := withJourney(t, svc)
	r := startReplay(t, svc, j, 5)
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

func TestJourneysRoute(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	j := withJourney(t, svc)
	var got []struct {
		ID, Persona, Story, State string
		LiveRev                   int `json:"liveRev"`
		Revisions                 []struct {
			N            int
			Kind, Reason string
			RunID        string `json:"runId"`
			Code         string
		} `json:"revisions"`
	}
	resp := do(t, "GET", srv.URL+"/api/journeys?persona=ann&story=checkout", "")
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %v", resp.StatusCode, err)
	}
	if len(got) != 1 || got[0].ID != j.ID || got[0].LiveRev != 1 || len(got[0].Revisions) != 1 || got[0].Revisions[0].Kind != "full" {
		t.Fatalf("journeys %+v", got)
	}
	if got[0].Revisions[0].Code != "" {
		t.Fatal("the list carries code; it shows only revisions")
	}
	resp = do(t, "GET", srv.URL+"/api/journeys?persona=bob", "")
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || len(got) != 0 {
		t.Fatalf("filter: %+v %v", got, err)
	}
}

func TestReplayJourneyQueues(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	j := withJourney(t, svc)
	req, _ := http.NewRequest("POST", srv.URL+"/api/journeys/"+j.ID+"/replay", strings.NewReader(`{"revision":1,"seed":9}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-BP-Actor", "kate@shop.test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct{ Queued store.Queued }
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != 202 {
		t.Fatalf("%d %v", resp.StatusCode, err)
	}
	if q := got.Queued; q.ID == "" || q.Kind != store.RunReplay || q.Revision != 1 || q.Seed != 9 || q.JourneyID != j.ID {
		t.Fatalf("queued %+v", q)
	}
	if qs, _ := svc.Store.Queue(); len(qs) != 1 {
		t.Fatalf("queue %+v", qs)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if ds[0].Kind != "replayed" || ds[0].Actor != "kate@shop.test" {
		t.Fatalf("decision %+v", ds[0])
	}
}

func TestReplayRouteRefusesStale(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	j := withJourney(t, svc)
	req, _ := http.NewRequest("POST", srv.URL+"/api/journeys/"+j.ID+"/replay", strings.NewReader(`{"revision":7}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 409 || !strings.Contains(string(body), "no revision 7") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	req, _ = http.NewRequest("POST", srv.URL+"/api/journeys/nope/replay", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 404 {
		t.Fatalf("missing journey = %v %v", r, err)
	}
	if qs, _ := svc.Store.Queue(); len(qs) != 0 {
		t.Fatalf("queued: %+v", qs)
	}
}

// A replay's token reaches only what a replay needs: no usage (a replay
// cannot add spend) and no test files.
func TestReplayTokenGetsNoUsageOrFiles(t *testing.T) {
	_, wsrv, svc := setupBoth(t, 1<<20)
	j := withJourney(t, svc)
	r := startReplay(t, svc, j, 5)
	if got := do(t, "PUT", wsrv.URL+"/usage/"+r.Token, `{"byModel":{"claude-opus-5-5":{"output":1000000}}}`).StatusCode; got != 404 {
		t.Errorf("usage with a replay token = %d, want 404", got)
	}
	if got := do(t, "GET", wsrv.URL+"/files/"+r.Token+"/", "").StatusCode; got != 404 {
		t.Errorf("files with a replay token = %d, want 404", got)
	}
	if got := do(t, "GET", wsrv.URL+"/files/"+r.Token+"/a.pdf", "").StatusCode; got != 404 {
		t.Errorf("a file with a replay token = %d, want 404", got)
	}
}

func TestReplayRouteRefusesHugeSeed(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	j := withJourney(t, svc)
	req, _ := http.NewRequest("POST", srv.URL+"/api/journeys/"+j.ID+"/replay", strings.NewReader(`{"seed":9007199254740993}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("seed past 2^31 = %d, want 400", resp.StatusCode)
	}
}
