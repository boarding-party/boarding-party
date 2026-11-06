package api

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

const budgetYAML = `name: p
sites: ["https://x.test"]
dailyBudget: 1
roles:
  - name: buyer
    activity: "every 30 mins, 09:00-17:30"
    personas: [{name: ann}]
    stories: [{name: checkout, goal: buy a thing}]
`

// spend reports $2 of sonnet usage (1M input tokens) for the run.
func spend(t *testing.T, svc *runs.Service, token string) {
	t.Helper()
	u := runs.Usage{Counts: runs.Counts{Input: 1_000_000}, ByModel: map[string]runs.Counts{"claude-sonnet-5-5": {Input: 1_000_000}}}
	if err := svc.SaveUsage(token, u, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestStatusReportsSpendAndPause(t *testing.T) {
	srv, svc := setup(t, 1024)
	if r := do(t, "POST", srv.URL+"/api/project", budgetYAML); r.StatusCode != 204 {
		t.Fatalf("project: %d", r.StatusCode)
	}
	due := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := svc.Store.SetDue(store.Due{Persona: "ann", At: due}); err != nil {
		t.Fatal(err)
	}
	get := func() runs.Status {
		var st runs.Status
		if err := json.NewDecoder(do(t, "GET", srv.URL+"/api/status", "").Body).Decode(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	st := get()
	if st.Paused || st.OverBudget || st.SpentToday != 0 || st.DailyBudget != 1 || st.Timezone != "Europe/London" {
		t.Fatalf("fresh status = %+v", st)
	}
	if !st.ResetsAt.After(time.Now()) || st.ResetsAt.After(time.Now().Add(25*time.Hour)) {
		t.Fatalf("resetsAt = %v", st.ResetsAt)
	}
	if len(st.Due) != 1 || st.Due[0].Persona != "ann" || st.Due[0].Role != "buyer" || !st.Due[0].At.Equal(due) {
		t.Fatalf("due = %+v", st.Due)
	}

	run := startRun(t, srv, svc)
	do(t, "POST", srv.URL+"/api/project", budgetYAML) // startRun installed projYAML
	spend(t, svc, run.Token)
	if r := doJSON(t, "PUT", srv.URL+"/api/pause", `{"paused":true}`); r.StatusCode != 204 {
		t.Fatalf("pause: %d", r.StatusCode)
	}
	st = get()
	if !st.Paused || !st.OverBudget || st.SpentToday < 1.99 || st.SpentToday > 2.01 {
		t.Fatalf("status after spend+pause = %+v", st)
	}
}

func TestPauseBlocksStart(t *testing.T) {
	srv, svc := setup(t, 1024)
	if r := do(t, "POST", srv.URL+"/api/project", projYAML); r.StatusCode != 204 {
		t.Fatalf("project: %d", r.StatusCode)
	}
	if r := doJSON(t, "PUT", srv.URL+"/api/pause", `{"paused":true}`); r.StatusCode != 204 {
		t.Fatalf("pause: %d", r.StatusCode)
	}
	resp := do(t, "POST", srv.URL+"/api/runs", `{"persona":"ann","story":"checkout"}`)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 409 || !strings.Contains(string(b), "paused") {
		t.Fatalf("paused start = %d %s", resp.StatusCode, b)
	}
	// Resume, start a run, then pause again: a start while busy must still
	// return the gate error, never a queued item.
	pokes := 0
	svc.OnFinish = func() { pokes++ }
	doJSON(t, "PUT", srv.URL+"/api/pause", `{"paused":false}`)
	if pokes != 1 {
		t.Fatalf("resume pokes = %d, want 1", pokes)
	}
	if r := do(t, "POST", srv.URL+"/api/runs", `{"persona":"ann","story":"checkout"}`); r.StatusCode != 201 {
		t.Fatalf("resumed start = %d", r.StatusCode)
	}
	doJSON(t, "PUT", srv.URL+"/api/pause", `{"paused":true}`)
	resp = do(t, "POST", srv.URL+"/api/runs", `{"persona":"ann","story":"checkout"}`)
	if resp.StatusCode != 409 {
		t.Fatalf("paused+busy start = %d, want 409", resp.StatusCode)
	}
	if q, _ := svc.Store.Queue(); len(q) != 0 {
		t.Fatalf("gated start was queued: %+v", q)
	}
	if r := doJSON(t, "PUT", srv.URL+"/api/pause", `nope`); r.StatusCode != 400 {
		t.Fatalf("bad pause body = %d", r.StatusCode)
	}
}

func TestOverBudgetBlocksStart(t *testing.T) {
	srv, svc := setup(t, 1024)
	run := startRun(t, srv, svc)
	do(t, "POST", srv.URL+"/api/project", budgetYAML) // startRun installed projYAML
	spend(t, svc, run.Token)
	resp := do(t, "POST", srv.URL+"/api/runs", `{"persona":"ann","story":"checkout"}`)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 409 || !strings.Contains(string(b), "budget") {
		t.Fatalf("over-budget start = %d %s", resp.StatusCode, b)
	}
}

func TestStopRun(t *testing.T) {
	srv, svc := setup(t, 1024)
	run := startRun(t, srv, svc)
	pokes := 0
	svc.OnFinish = func() { pokes++ }
	if r := do(t, "POST", srv.URL+"/api/runs/"+run.ID+"/stop", ""); r.StatusCode != 204 {
		t.Fatalf("stop = %d", r.StatusCode)
	}
	got, err := svc.Store.RunByID(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.RunFailed || got.Outcome != "Stopped from the board." || pokes != 1 {
		t.Fatalf("run = %+v pokes %d", got, pokes)
	}
	if r := do(t, "POST", srv.URL+"/api/runs/"+run.ID+"/stop", ""); r.StatusCode != 404 {
		t.Fatalf("second stop = %d, want 404", r.StatusCode)
	}
	if r := do(t, "POST", srv.URL+"/api/runs/nope/stop", ""); r.StatusCode != 404 {
		t.Fatalf("unknown stop = %d, want 404", r.StatusCode)
	}
}

func TestGetRunCost(t *testing.T) {
	srv, svc := setup(t, 1024)
	run := startRun(t, srv, svc)
	var out map[string]any
	json.NewDecoder(do(t, "GET", srv.URL+"/api/runs/"+run.ID, "").Body).Decode(&out)
	if _, ok := out["cost"]; ok {
		t.Fatalf("cost without usage: %v", out["cost"])
	}
	spend(t, svc, run.Token)
	out = nil
	json.NewDecoder(do(t, "GET", srv.URL+"/api/runs/"+run.ID, "").Body).Decode(&out)
	c, _ := out["cost"].(float64)
	if c < 1.99 || c > 2.01 {
		t.Fatalf("cost = %v", out["cost"])
	}
}
