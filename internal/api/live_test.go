package api

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/live"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

var jpeg1 = append([]byte{0xFF, 0xD8, 0xFF}, bytes.Repeat([]byte("x"), 100)...)

func putLive(t *testing.T, base, token string, body []byte) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("PUT", base+"/live/"+token, bytes.NewReader(body))
	req.Header.Set("Content-Type", "image/jpeg")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// setupLive returns admin and worker servers sharing a hub, with fast stream checks.
func setupLive(t *testing.T) (*httptest.Server, *httptest.Server, *runs.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := &runs.Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), 1024), Launcher: noopLauncher{}}
	hub := live.NewHub()
	as, ah := admin(svc, 1024, testToken, hub)
	as.liveCheck = 20 * time.Millisecond
	asrv := httptest.NewServer(ah)
	t.Cleanup(asrv.Close)
	wsrv := httptest.NewServer(Worker(svc, 1024, hub))
	t.Cleanup(wsrv.Close)
	return asrv, wsrv, svc
}

func openStream(t *testing.T, base, id string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", base+"/api/runs/"+id+"/live", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPutLiveValidation(t *testing.T) {
	srv, w, svc := setupLive(t)
	run := startRun(t, srv, svc)
	if r := putLive(t, w.URL, run.Token, []byte("PNG not jpeg")); r.StatusCode != 400 {
		t.Errorf("non-jpeg: %d", r.StatusCode)
	}
	if r := putLive(t, w.URL, run.Token, append([]byte{0xFF, 0xD8}, make([]byte, 512<<10)...)); r.StatusCode != 413 {
		t.Errorf("oversize: %d", r.StatusCode)
	}
	if r := putLive(t, w.URL, "deadbeef", jpeg1); r.StatusCode != 404 {
		t.Errorf("unknown token: %d", r.StatusCode)
	}
	if r := putLive(t, w.URL, run.Token, jpeg1); r.StatusCode != 204 {
		t.Errorf("ok: %d", r.StatusCode)
	}
	svc.Finish(context.Background(), run.Token, store.RunDone, "ok")
	art := do(t, "PUT", w.URL+"/artifacts/"+run.Token+"/a.png", "x").StatusCode
	if r := putLive(t, w.URL, run.Token, jpeg1); r.StatusCode != art {
		t.Errorf("finished: %d, artifacts give %d", r.StatusCode, art)
	}
}

func readPart(t *testing.T, br *bufio.Reader) []byte {
	t.Helper()
	var n int
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimRight(line, "\r\n")
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			n, _ = strconv.Atoi(v)
		}
		if line == "" && n > 0 {
			break
		}
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(br, b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLiveStreamAndWatchers(t *testing.T) {
	srv, w, svc := setupLive(t)
	run := startRun(t, srv, svc)
	if r := putLive(t, w.URL, run.Token, jpeg1); r.Header.Get("X-Bp-Watchers") != "0" {
		t.Errorf("watchers before: %q", r.Header.Get("X-Bp-Watchers"))
	}
	resp := openStream(t, srv.URL, run.ID)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "multipart/x-mixed-replace; boundary=frame" {
		t.Fatalf("stream: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	br := bufio.NewReader(resp.Body)
	if got := readPart(t, br); !bytes.Equal(got, jpeg1) {
		t.Error("first part is not the stored frame")
	}
	if r := putLive(t, w.URL, run.Token, jpeg1); r.Header.Get("X-Bp-Watchers") != "1" {
		t.Errorf("watchers during: %q", r.Header.Get("X-Bp-Watchers"))
	}
	next := append([]byte{0xFF, 0xD8}, []byte("second")...)
	putLive(t, w.URL, run.Token, next)
	found := make(chan bool, 1)
	go func() {
		for i := 0; i < 10; i++ {
			if bytes.Equal(readPart(t, br), next) {
				found <- true
				return
			}
		}
		found <- false
	}()
	select {
	case ok := <-found:
		if !ok {
			t.Error("published frame never arrived")
		}
	case <-time.After(3 * time.Second):
		t.Error("timed out waiting for the published frame")
	}
}

func TestLiveStreamStatuses(t *testing.T) {
	srv, _, svc := setupLive(t)
	if r := do(t, "GET", srv.URL+"/api/runs/nope/live", ""); r.StatusCode != 404 {
		t.Errorf("unknown: %d", r.StatusCode)
	}
	run := startRun(t, srv, svc)
	svc.Finish(context.Background(), run.Token, store.RunDone, "ok")
	r := do(t, "GET", srv.URL+"/api/runs/"+run.ID+"/live", "")
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 410 || strings.TrimSpace(string(b)) != `{"error":"run finished"}` {
		t.Errorf("finished: %d %s", r.StatusCode, b)
	}
}

func TestLiveStreamEndsWhenRunFinishes(t *testing.T) {
	srv, _, svc := setupLive(t)
	run := startRun(t, srv, svc)
	resp := openStream(t, srv.URL, run.ID)
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, resp.Body); close(done) }()
	svc.Finish(context.Background(), run.Token, store.RunDone, "ok")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream still open after run finished")
	}
}

func TestLiveStreamEndsAtMaxDuration(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	t.Cleanup(func() { st.Close() })
	svc := &runs.Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), 1024), Launcher: noopLauncher{}}
	hub := live.NewHub()
	as, ah := admin(svc, 1024, testToken, hub)
	as.liveMax = 100 * time.Millisecond
	asrv := httptest.NewServer(ah)
	t.Cleanup(asrv.Close)
	run := startRun(t, asrv, svc)
	resp := openStream(t, asrv.URL, run.ID)
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, resp.Body); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream outlived its cap")
	}
}
