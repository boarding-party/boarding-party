package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPutConsole(t *testing.T) {
	srv, wsrv, svc := setupBoth(t, 1<<20)
	run := startRun(t, srv, svc)

	ok := `[{"at":"2026-11-10T09:00:01Z","level":"error","kind":"exception","text":"TypeError: x","url":"https://a.test/app.js"}]`
	for body, want := range map[string]int{
		ok: 204,
		`[{"at":"2026-11-10T09:00:01Z","level":"loud","kind":"console","text":"x"}]`:                                                                                                           400, // unknown level
		`[{"at":"2026-11-10T09:00:01Z","level":"error","kind":"magic","text":"x"}]`:                                                                                                            400, // unknown kind
		`[{"at":"2026-11-10T09:00:01Z","level":"error","kind":"console","text":"x","extra":1}]`:                                                                                                400,
		`[` + strings.Repeat(`{"at":"2026-11-10T09:00:01Z","level":"error","kind":"console","text":"x"},`, 200) + `{"at":"2026-11-10T09:00:01Z","level":"error","kind":"console","text":"x"}]`: 400, // over 200 in one batch
		`not json`: 400,
	} {
		if r := do(t, "PUT", wsrv.URL+"/console/"+run.Token, body); r.StatusCode != want {
			t.Errorf("PUT %.60s = %d, want %d", body, r.StatusCode, want)
		}
	}
	if r := do(t, "PUT", wsrv.URL+"/console/wrong", ok); r.StatusCode != 404 {
		t.Errorf("unknown token = %d, want 404", r.StatusCode)
	}
	var got struct {
		Console []struct{ Level, Kind, Text, URL string } `json:"console"`
	}
	if err := json.NewDecoder(do(t, "GET", srv.URL+"/api/runs/"+run.ID, "").Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Console) != 1 || got.Console[0].Kind != "exception" || got.Console[0].URL != "https://a.test/app.js" {
		t.Fatalf("console = %+v", got.Console)
	}
}
