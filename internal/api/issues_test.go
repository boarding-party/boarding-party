package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

func postStatus(t *testing.T, url, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestIssuesAPI(t *testing.T) {
	srv, wsrv, svc := setupBoth(t, 1024)
	now := time.Now().UTC()
	if err := svc.Store.CreateRun(store.Run{ID: "r1", Token: "tok", Persona: "ann", Role: "buyer", Story: "checkout",
		State: store.RunDone, Started: now, Chain: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AddBug(store.Bug{ID: "b1", RunID: "r1", Summary: "total wrong", Created: now}); err != nil {
		t.Fatal(err)
	}
	var list struct{ Issues []store.Issue }
	resp := do(t, "GET", srv.URL+"/api/issues", "")
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Issues) != 1 || list.Issues[0].Summary != "total wrong" {
		t.Fatalf("list %+v", list)
	}
	id := list.Issues[0].ID
	if code := postStatus(t, srv.URL+"/api/issues/"+id+"/status", `{"status":"not_a_bug","reason":"site default"}`); code != 204 {
		t.Fatalf("status code %d", code)
	}
	var detail struct {
		Issue   store.Issue
		Reports []store.Report
	}
	resp = do(t, "GET", srv.URL+"/api/issues/"+id, "")
	json.NewDecoder(resp.Body).Decode(&detail)
	resp.Body.Close()
	if detail.Issue.Status != store.IssueNotABug || detail.Issue.Reason != "site default" ||
		len(detail.Reports) != 1 || detail.Reports[0].Persona != "ann" || detail.Reports[0].Story != "checkout" {
		t.Fatalf("detail %+v", detail)
	}
	if code := postStatus(t, srv.URL+"/api/issues/"+id+"/status", `{"status":"bogus"}`); code != 400 {
		t.Fatalf("bogus status code %d", code)
	}
	if code := postStatus(t, srv.URL+"/api/issues/nope/status", `{"status":"confirmed"}`); code != 404 {
		t.Fatalf("unknown issue code %d", code)
	}
	if resp := do(t, "GET", srv.URL+"/api/issues/nope", ""); resp.StatusCode != 404 {
		t.Fatalf("unknown detail %d", resp.StatusCode)
	}
	if resp := do(t, "GET", wsrv.URL+"/api/issues", ""); resp.StatusCode != 404 {
		t.Fatalf("worker serves issues: %d", resp.StatusCode)
	}
}

func TestIssueSaysHowAFixIsChecked(t *testing.T) {
	srv, svc := setup(t, 1024)
	now := time.Now().UTC()
	if err := svc.Store.CreateRun(store.Run{ID: "r1", Token: "tok", Persona: "ann", Role: "buyer", Story: "checkout",
		State: store.RunDone, Started: now, Chain: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AddBug(store.Bug{ID: "b1", RunID: "r1", Summary: "total wrong", Created: now}); err != nil {
		t.Fatal(err)
	}
	bugs, _ := svc.Store.Bugs("r1")
	checkBy := func() string {
		t.Helper()
		var d struct {
			CheckBy string `json:"checkBy"`
		}
		resp := do(t, "GET", srv.URL+"/api/issues/"+bugs[0].IssueID, "")
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(&d); err != nil || resp.StatusCode != 200 {
			t.Fatalf("%d %v", resp.StatusCode, err)
		}
		return d.CheckBy
	}
	if got := checkBy(); got != "none" {
		t.Fatalf("no persona in the project: checkBy = %q", got)
	}
	withJourney(t, svc) // puts the project
	if got := checkBy(); got != "run" {
		t.Fatalf("checkBy = %q", got)
	}
}
