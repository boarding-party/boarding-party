package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/boarding-party/boardingparty/internal/rewind"
	"github.com/boarding-party/boardingparty/internal/store"
)

func TestRewindServesKeptFrames(t *testing.T) {
	srv, w, svc := setupLive(t)
	svc.Rewind = rewind.New(t.TempDir(), 1<<20)
	run := startRun(t, srv, svc)
	putLive(t, w.URL, run.Token, jpeg1)

	resp := do(t, "GET", srv.URL+"/api/runs/"+run.ID+"/rewind", "")
	var idx struct {
		Frames []int64 `json:"frames"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&idx) != nil || len(idx.Frames) != 1 {
		t.Fatalf("index: %d %v", resp.StatusCode, idx)
	}
	resp = do(t, "GET", srv.URL+"/api/runs/"+run.ID+"/rewind/0", "")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal(body, jpeg1) {
		t.Fatalf("frame: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, n := range []string{"5", "-1", "x"} {
		if r := do(t, "GET", srv.URL+"/api/runs/"+run.ID+"/rewind/"+n, ""); r.StatusCode != 404 {
			t.Errorf("frame %s: %d", n, r.StatusCode)
		}
	}
}

func TestRewindDroppedWhenTheRunHasARecording(t *testing.T) {
	srv, w, svc := setupLive(t)
	svc.Rewind = rewind.New(t.TempDir(), 1<<20)
	run := startRun(t, srv, svc)
	putLive(t, w.URL, run.Token, jpeg1)
	if r := do(t, "PUT", w.URL+"/artifacts/"+run.Token+"/story-video.webm", "webm"); r.StatusCode >= 300 {
		t.Fatalf("upload: %d", r.StatusCode)
	}
	if err := svc.Finish(context.Background(), run.Token, store.RunDone, "ok"); err != nil {
		t.Fatal(err)
	}
	if at, _ := svc.Rewind.Index(run.ID); len(at) != 0 {
		t.Fatalf("frames kept beside a recording: %v", at)
	}
}

func TestRewindKeptWithoutARecording(t *testing.T) {
	srv, w, svc := setupLive(t)
	svc.Rewind = rewind.New(t.TempDir(), 1<<20)
	run := startRun(t, srv, svc)
	putLive(t, w.URL, run.Token, jpeg1)
	if err := svc.Finish(context.Background(), run.Token, store.RunDone, "ok"); err != nil {
		t.Fatal(err)
	}
	if at, _ := svc.Rewind.Index(run.ID); len(at) != 1 {
		t.Fatalf("frames %v", at)
	}
}
