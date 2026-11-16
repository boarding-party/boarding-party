package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPutNarration(t *testing.T) {
	srv, wsrv, svc := setupBoth(t, 1<<20)
	run := startRun(t, srv, svc)

	ok := `[{"at":"2026-11-16T10:00:00Z","kind":"thought","text":"Looking for the basket"},{"at":"2026-11-16T10:00:01Z","kind":"action","text":"browser_click: Basket"}]`
	for body, want := range map[string]int{
		ok: 204,
		`[{"at":"2026-11-16T10:00:00Z","kind":"thought","text":"x","mood":"calm"}]`: 400, // unknown fields are refused
		`[{"at":"2026-11-16T10:00:00Z","kind":"shout","text":"x"}]`:                 400,
		`not json`: 400,
		`[` + strings.Repeat(`{"at":"2026-11-16T10:00:00Z","kind":"thought","text":"`+strings.Repeat("x", 500)+`"},`, 4500) + `{}]`: 400, // over the body limit
	} {
		if r := do(t, "PUT", wsrv.URL+"/narration/"+run.Token, body); r.StatusCode != want {
			t.Errorf("PUT %.60s = %d, want %d", body, r.StatusCode, want)
		}
	}
	if r := do(t, "PUT", wsrv.URL+"/narration/wrong", ok); r.StatusCode != 404 {
		t.Errorf("unknown token = %d, want 404", r.StatusCode)
	}

	var got struct {
		Narration []struct{ At, Kind, Text string } `json:"narration"`
		Artifacts []string                         `json:"artifacts"`
	}
	if err := json.NewDecoder(do(t, "GET", srv.URL+"/api/runs/"+run.ID, "").Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Narration) != 2 || got.Narration[0].Kind != "thought" || got.Narration[1].Text != "browser_click: Basket" {
		t.Fatalf("run narration = %+v", got.Narration)
	}
}
