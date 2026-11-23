package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/live"
	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

func TestSetupEndpoints(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := &runs.Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), 1024), Launcher: noopLauncher{},
		StagingURL: func(context.Context) string { return "https://staging.example.com" }}
	srv := newTestServerFor(t, svc)
	get := func() map[string]any {
		resp := do(t, "GET", srv+"/api/setup", "")
		var v map[string]any
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || json.Unmarshal(b, &v) != nil {
			t.Fatalf("GET %d %s", resp.StatusCode, b)
		}
		return v
	}
	if v := get(); v["needed"] != true || v["stagingURL"] != "https://staging.example.com" {
		t.Fatalf("before: %v", v)
	}
	body := `{"site":"https://staging.example.com","stories":["sign up"],"personas":4,"dailyBudget":5}`
	if code, b := postJSON(t, srv+"/api/setup", body); code != http.StatusNoContent {
		t.Fatalf("POST %d %s", code, b)
	}
	if code, b := postJSON(t, srv+"/api/setup", body); code != http.StatusConflict {
		t.Fatalf("second POST %d %s", code, b)
	}
	if code, _ := postJSON(t, srv+"/api/setup", `{"site":"https://staging.example.com","stories":[],"personas":0,"dailyBudget":5}`); code != http.StatusBadRequest {
		t.Fatalf("bad request %d", code)
	}
	if v := get(); v["needed"] != false {
		t.Fatalf("after: %v", v)
	}
}

// newTestServerFor serves the admin API of svc and returns its URL.
func newTestServerFor(t *testing.T, svc *runs.Service) string {
	t.Helper()
	srv := httptest.NewServer(Admin(svc, 1024, testToken, live.NewHub(), nil))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestLoginEndpointAndWaitView(t *testing.T) {
	srv, svc := setup(t, 1024)
	if err := svc.Store.PutProject(mustProject(t)); err != nil {
		t.Fatal(err)
	}
	mk := func(id, kind string) {
		if err := svc.Store.CreateRun(store.Run{ID: id, Token: "tok-" + id, Persona: "ann", Role: "buyer", Story: "checkout",
			State: store.RunRunning, Started: time.Now()}); err != nil {
			t.Fatal(err)
		}
		w := store.Wait{Kind: kind, Login: "admin", Note: "open Settings", From: "a", Deadline: time.Now().Add(time.Hour)}
		if ok, err := svc.Store.FinishRunWaiting(id, "needs admin", time.Now(), w); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	mk("r1", "login")
	mk("r2", "email")
	resp := do(t, "GET", srv.URL+"/api/runs/r1", "")
	b, _ := io.ReadAll(resp.Body)
	var run struct {
		Wait struct{ Kind, Login, Note, WaitingFor string }
	}
	if err := json.Unmarshal(b, &run); err != nil || run.Wait.Kind != "login" || run.Wait.Login != "admin" ||
		run.Wait.Note != "open Settings" || run.Wait.WaitingFor != `a login for "admin"` {
		t.Fatalf("run %s", b)
	}
	if code, b := postJSON(t, srv.URL+"/api/runs/r1/login", `{"username":"","password":"pw"}`); code != http.StatusBadRequest {
		t.Fatalf("empty username %d %s", code, b)
	}
	if code, b := postJSON(t, srv.URL+"/api/runs/r2/login", `{"username":"ann@x.test","password":"pw"}`); code != http.StatusConflict {
		t.Fatalf("email wait %d %s", code, b)
	}
	code, body := postJSON(t, srv.URL+"/api/runs/r1/login", `{"username":"ann@x.test","password":"s3cret"}`)
	if code != http.StatusNoContent || strings.Contains(body, "s3cret") {
		t.Fatalf("give %d %s", code, body)
	}
	if w, _ := svc.Store.WaitFor("r1"); w.At.IsZero() {
		t.Fatal("wait not answered")
	}
}

func mustProject(t *testing.T) project.Project {
	t.Helper()
	p, err := project.Load([]byte(projYAML))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
