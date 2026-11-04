package api

import (
	"encoding/json"
	"testing"
)

func TestPutUsage(t *testing.T) {
	srv, wsrv, svc := setupBoth(t, 1<<20)
	run := startRun(t, srv, svc)

	for body, want := range map[string]int{
		`{"input":5,"output":6,"cacheRead":7,"cacheWrite":8,"turns":2}`: 204,
		`{"input":-1}`: 400, // counts are never negative
		`{"tokens":1}`: 400, // unknown fields are refused
		`not json`:     400,
	} {
		if r := do(t, "PUT", wsrv.URL+"/usage/"+run.Token, body); r.StatusCode != want {
			t.Errorf("PUT %s = %d, want %d", body, r.StatusCode, want)
		}
	}
	if r := do(t, "PUT", wsrv.URL+"/usage/wrong", `{"turns":1}`); r.StatusCode != 404 {
		t.Errorf("unknown token = %d, want 404", r.StatusCode)
	}

	var got struct {
		Usage struct{ Input, Output, CacheRead, CacheWrite, Turns uint64 } `json:"usage"`
	}
	if err := json.NewDecoder(do(t, "GET", srv.URL+"/api/runs/"+run.ID, "").Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Usage.Input != 5 || got.Usage.CacheWrite != 8 || got.Usage.Turns != 2 {
		t.Fatalf("run usage = %+v", got.Usage)
	}
}
