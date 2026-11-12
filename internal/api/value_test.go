package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

func TestValueAndRunCost(t *testing.T) {
	srv, svc := setup(t, 1024)
	now := time.Now().UTC()
	if err := svc.Store.CreateRun(store.Run{ID: "r1", Token: "tok-r1", Persona: "ann", Role: "buyer", Story: "checkout",
		State: store.RunRunning, Started: now.Add(-time.Hour), Journey: "r1"}); err != nil {
		t.Fatal(err)
	}
	spend(t, svc, "tok-r1")
	if err := svc.Store.AddBug(store.Bug{ID: "b1", RunID: "r1", Summary: "total wrong", Created: now.Add(-30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	r := do(t, "GET", srv.URL+"/api/value?days=7", "")
	if r.StatusCode != 200 {
		t.Fatalf("value: %d", r.StatusCode)
	}
	var v runs.Value
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v.Runs != 1 || v.Spent != 2 || v.Bugs != 1 || v.PerBug != 2 || len(v.Personas) != 1 {
		t.Fatalf("value %+v", v)
	}
	if got := now.Sub(v.Since); got < 7*24*time.Hour-time.Minute || got > 7*24*time.Hour+time.Minute {
		t.Fatalf("since %s, want 7 days back", v.Since)
	}
	for _, bad := range []string{"0", "91", "x"} {
		if r := do(t, "GET", srv.URL+"/api/value?days="+bad, ""); r.StatusCode != 400 {
			t.Fatalf("days=%s: %d, want 400", bad, r.StatusCode)
		}
	}

	var list []struct {
		ID   string   `json:"id"`
		Cost *float64 `json:"cost"`
	}
	if err := json.NewDecoder(do(t, "GET", srv.URL+"/api/runs", "").Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Cost == nil || *list[0].Cost != 2 {
		t.Fatalf("runs %+v, want r1 costing $2", list)
	}
}
