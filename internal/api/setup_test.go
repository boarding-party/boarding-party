package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/live"
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
	if code, _ := postJSON(t, srv+"/api/setup", `{"site":"https://staging.example.com","stories":[],"personas":4,"dailyBudget":5}`); code != http.StatusBadRequest {
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
