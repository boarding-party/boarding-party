package api

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
)

func TestClaimGivesReplayItsPathsOnce(t *testing.T) {
	srv, wsrv, svc := setupBoth(t, 1<<20)
	j := withJourney(t, svc)
	r := startReplay(t, svc, j, 5)
	if r.Claim == "" || r.Claim == r.Token {
		t.Fatalf("replay run claim %q", r.Claim)
	}
	if got := do(t, "GET", wsrv.URL+"/claim/"+r.Claim, "").StatusCode; got != 405 {
		t.Errorf("GET claim = %d, want 405", got)
	}
	resp := do(t, "POST", wsrv.URL+"/claim/"+r.Claim, "")
	body, _ := io.ReadAll(resp.Body)
	var paths map[string]string
	if resp.StatusCode != 200 || json.Unmarshal(body, &paths) != nil || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("claim = %d %s", resp.StatusCode, body)
	}
	want := map[string]string{"journey": "/journey/" + r.Token, "report": "/replay/" + r.Token, "upload": "/artifacts/" + r.Token + "/",
		"profile": "/profile/" + r.Token, "live": "/live/" + r.Token, "console": "/console/" + r.Token}
	for k, v := range want {
		if paths[k] != v {
			t.Errorf("paths[%s] = %q, want %q", k, paths[k], v)
		}
	}
	if len(paths) != len(want) {
		t.Errorf("paths %v", paths)
	}
	if got := do(t, "POST", wsrv.URL+"/claim/"+r.Claim, "").StatusCode; got != 404 {
		t.Errorf("second claim = %d, want 404", got)
	}
	// The board never sees a claim.
	b, _ := io.ReadAll(do(t, "GET", srv.URL+"/api/runs/"+r.ID, "").Body)
	if strings.Contains(string(b), r.Claim) || strings.Contains(string(b), "claim") {
		t.Errorf("run page carries the claim: %s", b)
	}
}

func TestClaimRefusedOnceTheRunEnds(t *testing.T) {
	_, wsrv, svc := setupBoth(t, 1<<20)
	j := withJourney(t, svc)
	r := startReplay(t, svc, j, 5)
	if err := svc.FinishReplay(r.Token, runs.ReplayResult{Status: "passed", Seed: 5}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := do(t, "POST", wsrv.URL+"/claim/"+r.Claim, "").StatusCode; got != 404 {
		t.Errorf("claim after the run = %d, want 404", got)
	}
}

func TestAIRunHasNoClaim(t *testing.T) {
	srv, _, svc := setupBoth(t, 1<<20)
	if r := startRun(t, srv, svc); r.Claim != "" {
		t.Errorf("AI run claim %q", r.Claim)
	}
}
