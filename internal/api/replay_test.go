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
	"github.com/boarding-party/boardingparty/internal/sched"
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
	var fetched struct {
		Code   string          `json:"code"`
		Resume json.RawMessage `json:"resume"`
	}
	if resp.StatusCode != 200 || json.Unmarshal(body, &fetched) != nil || fetched.Code != journeyJS || fetched.Resume != nil ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
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

const mailYAML = `name: p
sites: ["https://x.test"]
roles:
  - name: buyer
    personas: [{name: ann, identity: {email: ann@shop.test}}]
    stories: [{name: checkout, goal: buy a thing}]
`

// mailJourney saves a project where ann has an email address and gives her
// checkout journey a live revision.
func mailJourney(t *testing.T, svc *runs.Service) store.Journey {
	t.Helper()
	p, err := project.Load([]byte(mailYAML))
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

func TestReplayWaitParksTheReplay(t *testing.T) {
	_, wsrv, svc := setupBoth(t, 1<<20)
	j := mailJourney(t, svc)
	r := startReplay(t, svc, j, 5)
	body := `{"status":"waiting","step":1,"seed":5,"wait":{"n":1,"until":{"time":{"after":"30m"}}}}`
	if got := do(t, "PUT", wsrv.URL+"/replay/"+r.Token, body).StatusCode; got != 204 {
		t.Fatalf("PUT waiting = %d", got)
	}
	run, _ := svc.Store.RunByID(r.ID)
	w, err := svc.Store.WaitFor(r.ID)
	if run.State != store.RunWaiting || err != nil || w.Kind != "time" || w.Status != store.WaitOpen || !strings.HasPrefix(run.Outcome, "Waiting until") {
		t.Fatalf("run %+v wait %+v %v", run, w, err)
	}
}

func TestReplayHandoffWaitRefused(t *testing.T) {
	_, wsrv, svc := setupBoth(t, 1<<20)
	j := mailJourney(t, svc)
	r := startReplay(t, svc, j, 5)
	body := `{"status":"waiting","step":2,"seed":5,"wait":{"n":1,"until":{"handoff":{"role":"x","story":"y"}}}}`
	if got := do(t, "PUT", wsrv.URL+"/replay/"+r.Token, body).StatusCode; got != 204 {
		t.Fatalf("PUT = %d", got)
	}
	run, _ := svc.Store.RunByID(r.ID)
	if run.State != store.RunFailed || !strings.Contains(run.Outcome, "a replay cannot wait for a hand-off") {
		t.Fatalf("run %+v", run)
	}
}

// An email that arrives while the replay still runs, before it reports the
// wait, still ends the wait.
func TestReplayEmailWaitCountsMailFromTheRun(t *testing.T) {
	_, wsrv, svc := setupBoth(t, 1<<20)
	j := mailJourney(t, svc)
	r := startReplay(t, svc, j, 5)
	if _, err := svc.Store.AddMessage(store.Message{ID: "m1", UIDValidity: 1, UID: 1, Persona: "ann", From: "hello@x.test",
		Subject: "Confirm your account", Text: "Open https://x.test/confirm?t=1", Received: time.Now(), Stored: time.Now()}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	body := `{"status":"waiting","step":1,"seed":5,"wait":{"n":1,"until":{"email":{"subject":"confirm"},"timeout":"2h"}}}`
	if got := do(t, "PUT", wsrv.URL+"/replay/"+r.Token, body).StatusCode; got != 204 {
		t.Fatalf("PUT = %d", got)
	}
	w, err := svc.Store.WaitFor(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := svc.Store.RunByID(r.ID)
	a, met, err := sched.Met(svc.Store, w, run, time.Now())
	if err != nil || !met || a.MessageID != "m1" {
		t.Fatalf("met %v %+v %v (wait %+v)", met, a, err, w)
	}
}

func TestReplayEmailWaitNeedsAnAddress(t *testing.T) {
	_, wsrv, svc := setupBoth(t, 1<<20)
	j := withJourney(t, svc) // ann has no email address here
	r := startReplay(t, svc, j, 5)
	body := `{"status":"waiting","step":1,"wait":{"n":1,"until":{"email":{"subject":"confirm"}}}}`
	if got := do(t, "PUT", wsrv.URL+"/replay/"+r.Token, body).StatusCode; got != 204 {
		t.Fatalf("PUT = %d", got)
	}
	if run, _ := svc.Store.RunByID(r.ID); run.State != store.RunFailed || !strings.Contains(run.Outcome, "no email address") {
		t.Fatalf("run %+v", run)
	}
}

func TestJourneyFetchCarriesResume(t *testing.T) {
	_, wsrv, svc := setupBoth(t, 1<<20)
	j := mailJourney(t, svc)
	if _, err := svc.Store.AddMessage(store.Message{ID: "m1", UIDValidity: 1, UID: 1, Persona: "ann", From: "hello@x.test",
		Subject: "Confirm", Text: "Open https://x.test/confirm?t=1 or https://x.test/help.", Links: []string{"https://x.test/confirm?t=1", "https://x.test/html-only"},
		Received: time.Now(), Stored: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ctx, _ := json.Marshal(store.RunContext{Kind: "continue", Resume: &store.ContextResume{After: 1,
		Arrivals: []store.ContextArrival{{Detail: "email \"Confirm\" from hello@x.test", MessageID: "m1"}}}})
	q, err := svc.EnqueueReplay(j.ID, 0, 5, "owner", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	q.Context, q.Via = ctx, "continue"
	r, err := svc.StartQueued(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	resp := do(t, "GET", wsrv.URL+"/journey/"+r.Token, "")
	var got struct {
		Code   string `json:"code"`
		Resume struct {
			After    int `json:"after"`
			Arrivals []struct {
				Detail  string `json:"detail"`
				Message *struct {
					From, Subject, Text string
					Links               []string
				} `json:"message"`
			} `json:"arrivals"`
		} `json:"resume"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %v", resp.StatusCode, err)
	}
	a := got.Resume.Arrivals
	if got.Code != journeyJS || got.Resume.After != 1 || len(a) != 1 || a[0].Message == nil || a[0].Message.Subject != "Confirm" ||
		strings.Join(a[0].Message.Links, " ") != "https://x.test/confirm?t=1 https://x.test/html-only https://x.test/help" {
		t.Fatalf("fetched %+v", got)
	}
}

func TestReplayCodeCarriesTheChainsDay(t *testing.T) {
	_, wsrv, svc := setupBoth(t, 1<<20)
	j := withJourney(t, svc)
	r := startReplay(t, svc, j, 5)
	// An earlier run of the same chain: a continuation draws dates from the day the chain began.
	began := time.Date(2026, 3, 29, 23, 30, 0, 0, time.UTC) // already 30 March in London (BST)
	if err := svc.Store.CreateRun(store.Run{ID: "first", Token: "tok-first", Persona: "ann", Story: "checkout", State: store.RunWaiting,
		Started: began, Chain: r.Chain, Kind: store.RunReplay, JourneyID: j.ID, Revision: 1, Seed: 5}); err != nil {
		t.Fatal(err)
	}
	var fetched struct {
		Today string `json:"today"`
	}
	if err := json.NewDecoder(do(t, "GET", wsrv.URL+"/journey/"+r.Token, "").Body).Decode(&fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.Today != "2026-03-30" {
		t.Fatalf("today = %q", fetched.Today)
	}
}
