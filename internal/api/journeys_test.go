package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/live"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"

	_ "modernc.org/sqlite"
)

// act sends a board request as actor (none: no X-BP-Actor header).
func act(t *testing.T, method, url, actor, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if actor != "" {
		req.Header.Set("X-BP-Actor", actor)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestJourneyRoutePauseResume(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	j := withJourney(t, svc)
	base := srv.URL + "/api/journeys/" + j.ID
	if r := act(t, "POST", base+"/pause", "kate@shop.test", ""); r.StatusCode != 204 {
		t.Fatalf("pause = %d", r.StatusCode)
	}
	if r := act(t, "POST", base+"/resume", "", ""); r.StatusCode != 204 {
		t.Fatalf("resume = %d", r.StatusCode)
	}
	var got struct {
		Journey   store.Journey
		Revisions []store.Revision
		Decisions []store.Decision
	}
	resp := act(t, "GET", base, "", "")
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %v", resp.StatusCode, err)
	}
	if got.Journey.ID != j.ID || got.Journey.State != store.JourneyLive || len(got.Revisions) != 1 || got.Revisions[0].Code != journeyJS {
		t.Fatalf("journey %+v revisions %+v", got.Journey, got.Revisions)
	}
	if len(got.Decisions) != 3 || got.Decisions[0].Kind != "resumed" || got.Decisions[0].Actor != "local" ||
		got.Decisions[1].Kind != "paused" || got.Decisions[1].Actor != "kate@shop.test" {
		t.Fatalf("decisions %+v", got.Decisions)
	}
	for _, path := range []string{"/pause", "/resume"} {
		if r := act(t, "POST", srv.URL+"/api/journeys/nope"+path, "", ""); r.StatusCode != 404 {
			t.Errorf("%s of a missing journey = %d", path, r.StatusCode)
		}
	}
	if r := act(t, "GET", srv.URL+"/api/journeys/nope", "", ""); r.StatusCode != 404 {
		t.Errorf("missing journey = %d", r.StatusCode)
	}
}

func TestJourneyRouteRollback(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	j := withJourney(t, svc)
	base := srv.URL + "/api/journeys/" + j.ID + "/rollback"
	resp := act(t, "POST", base, "kate", `{"revision":1}`)
	var rev store.Revision
	if err := json.NewDecoder(resp.Body).Decode(&rev); err != nil || resp.StatusCode != 200 || rev.N != 2 {
		t.Fatalf("%d %+v %v", resp.StatusCode, rev, err)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if ds[0].Kind != "rolled back" || ds[0].Actor != "kate" {
		t.Fatalf("decision %+v", ds[0])
	}
	for body, want := range map[string]int{`{"revision":0}`: 400, `{"revision":"x"}`: 400, `{}`: 400, `{"revision":9}`: 404} {
		if r := act(t, "POST", base, "kate", body); r.StatusCode != want {
			t.Errorf("rollback %s = %d, want %d", body, r.StatusCode, want)
		}
	}
}

func TestJourneyRouteDelete(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	j := withJourney(t, svc)
	if r := act(t, "DELETE", srv.URL+"/api/journeys/"+j.ID, "kate", ""); r.StatusCode != 204 {
		t.Fatalf("delete = %d", r.StatusCode)
	}
	if r := act(t, "DELETE", srv.URL+"/api/journeys/"+j.ID, "kate", ""); r.StatusCode != 404 {
		t.Fatalf("second delete = %d", r.StatusCode)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if ds[0].Kind != "deleted" || ds[0].Actor != "kate" {
		t.Fatalf("decision %+v", ds[0])
	}
}

// replayRun stores an ended replay run of journey j.
func replayRun(t *testing.T, svc *runs.Service, id, journeyID, via, follows string, state store.RunState, at time.Time) {
	t.Helper()
	if err := svc.Store.CreateRun(store.Run{ID: id, Token: "tok-" + id, Persona: "ann", Role: "buyer", Story: "checkout", State: state,
		Started: at, Chain: "c", Kind: store.RunReplay, JourneyID: journeyID, Revision: 1, Seed: 7, Via: via, Follows: follows}); err != nil {
		t.Fatal(err)
	}
}

func TestRunsFilteredByJourney(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	j := withJourney(t, svc)
	now := time.Now().UTC()
	replayRun(t, svc, "mine1", j.ID, "", "", store.RunDone, now)
	replayRun(t, svc, "other", "j-other", "", "", store.RunDone, now.Add(time.Second))
	replayRun(t, svc, "mine2", j.ID, "", "", store.RunFailed, now.Add(2*time.Second))
	var got []struct {
		ID   string   `json:"id"`
		Cost *float64 `json:"cost"`
	}
	resp := act(t, "GET", srv.URL+"/api/runs?journey="+j.ID, "", "")
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "mine2" || got[1].ID != "mine1" {
		t.Fatalf("runs %+v", got)
	}
}

func TestReplayCostIsZero(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	j := withJourney(t, svc)
	replayRun(t, svc, "rp", j.ID, "", "", store.RunDone, time.Now())
	var list []struct {
		ID   string   `json:"id"`
		Cost *float64 `json:"cost"`
	}
	if err := json.NewDecoder(act(t, "GET", srv.URL+"/api/runs", "", "").Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Cost == nil || *list[0].Cost != 0 {
		t.Fatalf("list %+v", list)
	}
	var one map[string]any
	if err := json.NewDecoder(act(t, "GET", srv.URL+"/api/runs/rp", "", "").Body).Decode(&one); err != nil {
		t.Fatal(err)
	}
	if c, ok := one["cost"].(float64); !ok || c != 0 {
		t.Fatalf("run cost %v", one["cost"])
	}
}

func TestFailedReplayShowsItsCopilot(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	j := withJourney(t, svc)
	now := time.Now().UTC()
	replayRun(t, svc, "rp", j.ID, "", "", store.RunFailed, now)
	replayRun(t, svc, "fc", j.ID, "flake check", "rp", store.RunFailed, now.Add(time.Second))
	if err := svc.Store.CreateRun(store.Run{ID: "cop", Token: "tok-cop", Persona: "ann", Role: "buyer", Story: "checkout", State: store.RunDone,
		Started: now.Add(2 * time.Second), Chain: "c", Via: "copilot", Follows: "fc"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AddDecision(store.Decision{JourneyID: j.ID, RunID: "cop", Kind: "site changed", Reason: "button renamed",
		Actor: store.ActorModel, RevBefore: 1, RevAfter: 2, Created: now}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Copilot *struct {
			RunID    string `json:"runId"`
			State    string `json:"state"`
			Decision *struct {
				Kind     string `json:"kind"`
				Reason   string `json:"reason"`
				RevAfter int    `json:"revAfter"`
			} `json:"decision"`
		} `json:"copilot"`
	}
	if err := json.NewDecoder(act(t, "GET", srv.URL+"/api/runs/rp", "", "").Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Copilot == nil || got.Copilot.RunID != "cop" || got.Copilot.State != "done" || got.Copilot.Decision == nil ||
		got.Copilot.Decision.Kind != "site changed" || got.Copilot.Decision.Reason != "button renamed" || got.Copilot.Decision.RevAfter != 2 {
		t.Fatalf("copilot %+v", got.Copilot)
	}
	var none map[string]any
	if err := json.NewDecoder(act(t, "GET", srv.URL+"/api/runs/cop", "", "").Body).Decode(&none); err != nil {
		t.Fatal(err)
	}
	if _, ok := none["copilot"]; ok {
		t.Fatal("an AI run got a copilot")
	}
}

func TestRunWithoutItsCopilotWhenTheLookupFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := &runs.Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), 1<<20), Launcher: noopLauncher{}}
	srv := httptest.NewServer(Admin(svc, 1<<20, testToken, live.NewHub(), nil))
	t.Cleanup(srv.Close)
	j := withJourney(t, svc)
	now := time.Now().UTC()
	replayRun(t, svc, "rp", j.ID, "", "", store.RunFailed, now)
	if err := svc.Store.CreateRun(store.Run{ID: "cop", Token: "tok-cop", Persona: "ann", Role: "buyer", Story: "checkout", State: store.RunDone,
		Started: now.Add(time.Second), Chain: "c", Via: "copilot", Follows: "rp"}); err != nil {
		t.Fatal(err)
	}
	// Break the copilot's decision lookup.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`ALTER TABLE journey_decisions RENAME TO journey_decisions_gone`); err != nil {
		t.Fatal(err)
	}
	resp := act(t, "GET", srv.URL+"/api/runs/rp", "", "")
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != 200 {
		t.Fatalf("%d %v", resp.StatusCode, err)
	}
	if run, _ := got["run"].(map[string]any); run == nil || run["id"] != "rp" {
		t.Fatalf("run %+v", got)
	}
	if _, ok := got["copilot"]; ok {
		t.Fatalf("copilot shown after a failed lookup: %+v", got["copilot"])
	}
}
