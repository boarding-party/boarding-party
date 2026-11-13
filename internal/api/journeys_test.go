package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/boarding-party/boardingparty/internal/store"
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
