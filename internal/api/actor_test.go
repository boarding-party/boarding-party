package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func doAs(t *testing.T, actor, method, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	if actor != "" {
		req.Header.Set("X-BP-Actor", actor)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func auditOf(t *testing.T, base string) []struct{ Actor, Action, Subject string } {
	t.Helper()
	var out []struct{ Actor, Action, Subject string }
	if err := json.NewDecoder(do(t, "GET", base+"/api/audit", "").Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

const tinyProject = `{"name":"p","sites":["https://x.test"],"roles":[{"name":"r","personas":[{"name":"ann"}],"stories":[{"name":"s","goal":"g"}]}]}`

func TestActorHeader(t *testing.T) {
	srv, _, _ := setupBoth(t, 1<<20)
	if r := doAs(t, "ann@x.io", "PUT", srv.URL+"/api/project", tinyProject); r.StatusCode != 204 {
		t.Fatalf("save = %d", r.StatusCode)
	}
	if r := doAs(t, "", "PUT", srv.URL+"/api/project", tinyProject); r.StatusCode != 204 {
		t.Fatalf("save = %d", r.StatusCode)
	}
	for _, bad := range []string{strings.Repeat("a", 255), "ann@x.io\x01"} {
		// Straight to the handler: Go's client refuses to send a control character.
		req := httptest.NewRequest("PUT", "/api/project", strings.NewReader(tinyProject))
		req.Host = "127.0.0.1"
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header["X-Bp-Actor"] = []string{bad}
		w := httptest.NewRecorder()
		srv.Config.Handler.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("actor %q = %d, want 400", bad[:8], w.Code)
		}
	}
	got := auditOf(t, srv.URL)
	if len(got) != 2 || got[0].Actor != "local" || got[1].Actor != "ann@x.io" || got[1].Action != "project.save" {
		t.Fatalf("audit %+v", got)
	}
}

func TestAgentKeySaveAuditedWithoutKey(t *testing.T) {
	srv, _, svc := setupBoth(t, 1<<20)
	svc.AgentKey = &fakeKeys{}
	if r := doAs(t, "ann@x.io", "PUT", srv.URL+"/api/agent-key", `{"key":"sk-ant-topsecret"}`); r.StatusCode != 204 {
		t.Fatalf("put = %d", r.StatusCode)
	}
	got := auditOf(t, srv.URL)
	b, _ := json.Marshal(got)
	if len(got) != 1 || got[0].Action != "agentkey.save" || got[0].Subject != "" || strings.Contains(string(b), "topsecret") {
		t.Fatalf("audit %s", b)
	}
}

func TestAuditSubjectIsWhatTheHandlerSaw(t *testing.T) {
	_, _, svc := setupBoth(t, 1<<20)
	s := newServer(svc, 1<<20, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/children/{name}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	req := httptest.NewRequest("DELETE", "/api/children/a%2Fb", nil)
	w := httptest.NewRecorder()
	s.withActor(mux).ServeHTTP(w, req)
	got, err := svc.Store.AuditLog(1)
	if err != nil || w.Code != 204 || len(got) != 1 || got[0].Subject != "a/b" {
		t.Fatalf("code %d audit %+v %v", w.Code, got, err)
	}
}
