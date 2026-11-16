package api

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/boarding-party/boardingparty/internal/tracesum"
)

func TestGetTraceSummary(t *testing.T) {
	srv, _, svc := setupBoth(t, 1<<20)
	run := startRun(t, srv, svc)
	url := srv.URL + "/api/runs/" + run.ID + "/trace"

	if r := do(t, "GET", url, ""); r.StatusCode != 404 {
		t.Fatalf("no trace = %d, want 404", r.StatusCode)
	}
	if _, err := svc.Artifacts.Put(run.ID, "trace.zip", bytes.NewReader([]byte("not a zip"))); err != nil {
		t.Fatal(err)
	}
	if r := do(t, "GET", url, ""); r.StatusCode != 422 {
		t.Fatalf("bad trace = %d, want 422", r.StatusCode)
	}

	b, err := os.ReadFile("../tracesum/testdata/trace.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Artifacts.Put(run.ID, "trace.zip", bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		r := do(t, "GET", url, "")
		if r.StatusCode != 200 {
			t.Fatalf("call %d = %d", i, r.StatusCode)
		}
		var s tracesum.Summary
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil || len(s.Actions) < 5 || len(s.Network) < 3 {
			t.Fatalf("call %d summary = %+v, %v", i, s, err)
		}
	}
	names, _ := svc.Artifacts.List(run.ID)
	if !slices.Contains(names, tracesum.SummaryName) {
		t.Fatalf("summary not kept: %v", names)
	}
}

// A summary kept by an older reader is worked out again, even though it is
// newer than the trace.
func TestGetTraceRereadsOldSummary(t *testing.T) {
	srv, _, svc := setupBoth(t, 1<<20)
	run := startRun(t, srv, svc)
	b, err := os.ReadFile("../tracesum/testdata/trace.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Artifacts.Put(run.ID, "trace.zip", bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Artifacts.Put(run.ID, tracesum.SummaryName, bytes.NewReader([]byte(`{"actions":[],"network":[]}`))); err != nil {
		t.Fatal(err)
	}
	r := do(t, "GET", srv.URL+"/api/runs/"+run.ID+"/trace", "")
	var s tracesum.Summary
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil || len(s.Actions) < 5 || s.Version != tracesum.Version {
		t.Fatalf("summary = %d actions, version %d, %v", len(s.Actions), s.Version, err)
	}
}
