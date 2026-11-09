package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

// withLead stores lead Kate, hand-made ann and Kate's children Ana and Bo.
func withLead(t *testing.T, svc *runs.Service) {
	t.Helper()
	p := project.Project{Name: "p", Sites: []string{"https://x.test"}, Roles: []project.Role{{
		Name:     "buyer",
		Personas: []project.Persona{{Name: "ann"}, {Name: "Kate", Lead: &project.Lead{CreatePersonas: true, StartRuns: true}}},
		Stories:  []project.Story{{Name: "checkout", Goal: "buy a thing"}},
	}}}
	if err := svc.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Ana", "Bo"} {
		if err := svc.Store.AddChild(store.Child{Name: n, Lead: "Kate", Reason: "phones", Persona: project.Persona{Name: n},
			Stories: []project.Story{{Name: "pay", Goal: "pay"}}, Created: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Store.CreateRun(store.Run{ID: "c1", Token: "tok-c1", Persona: "Ana", Role: "Ana", Story: "pay",
		State: store.RunDone, Started: time.Now(), Journey: "c1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.LogLead(store.LeadAction{Lead: "Kate", RunID: "l1", Action: "create", Subject: "Ana", Reason: "phones", Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func send(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestLeadsAPI(t *testing.T) {
	srv, _, svc := setupBoth(t, 1024)
	withLead(t, svc)

	var lead struct {
		Lead     project.Persona
		Usage    runs.LeadUsage
		Children []struct {
			store.Child
			Runs int `json:"runs"`
			Bugs int `json:"bugs"`
		}
		Log []store.LeadAction
	}
	resp := send(t, "GET", srv.URL+"/api/leads/Kate", "")
	if resp.StatusCode != 200 {
		t.Fatalf("get lead %d", resp.StatusCode)
	}
	json.NewDecoder(resp.Body).Decode(&lead)
	if lead.Lead.Name != "Kate" || len(lead.Children) != 2 || lead.Children[0].Name != "Ana" || lead.Children[0].Runs != 1 ||
		lead.Usage.Runs != 1 || len(lead.Log) != 1 || lead.Log[0].Subject != "Ana" {
		t.Fatalf("lead %+v", lead)
	}
	if code := send(t, "GET", srv.URL+"/api/leads/ann", "").StatusCode; code != 404 {
		t.Fatalf("non-lead %d", code)
	}

	if code := send(t, "POST", srv.URL+"/api/leads/Kate/wake", "").StatusCode; code != 202 {
		t.Fatalf("wake %d", code)
	}
	if code := send(t, "POST", srv.URL+"/api/leads/Kate/wake", "").StatusCode; code != 409 {
		t.Fatalf("second wake %d", code)
	}
	if code := send(t, "POST", srv.URL+"/api/leads/ann/wake", "").StatusCode; code != 404 {
		t.Fatalf("wake non-lead %d", code)
	}

	if code := send(t, "PUT", srv.URL+"/api/children/Ana", `{"persona":{"profile":{"device":"toaster"}},"stories":[{"name":"pay","goal":"g"}]}`).StatusCode; code != 400 {
		t.Fatalf("bad adopt %d", code)
	}
	if code := send(t, "PUT", srv.URL+"/api/children/Ana", `{"persona":{"traits":["phone"]},"stories":[{"name":"pay","goal":"pay twice"}]}`).StatusCode; code != 204 {
		t.Fatalf("adopt %d", code)
	}
	if code := send(t, "PUT", srv.URL+"/api/children/Ana", `{"persona":{},"stories":[]}`).StatusCode; code != 404 {
		t.Fatalf("adopt unknown %d", code)
	}
	if code := send(t, "DELETE", srv.URL+"/api/children/Bo", "").StatusCode; code != 204 {
		t.Fatalf("delete %d", code)
	}
	if code := send(t, "DELETE", srv.URL+"/api/children/Bo", "").StatusCode; code != 404 {
		t.Fatalf("delete unknown %d", code)
	}
	raw, _ := svc.Store.RawProject()
	if st, ok := raw.FindStory("Ana", "pay"); !ok || st.Goal != "pay twice" {
		t.Fatal("adopted child not in the project file")
	}
}

func TestGetProjectIsRaw(t *testing.T) {
	srv, _, svc := setupBoth(t, 1024)
	withLead(t, svc)
	var p project.Project
	json.NewDecoder(send(t, "GET", srv.URL+"/api/project", "").Body).Decode(&p)
	if _, ok := p.Role("Ana"); ok || len(p.Roles) != 1 {
		t.Fatalf("project shows children: %+v", p.Roles)
	}
}

func TestSaveProjectChildNameIs400(t *testing.T) {
	srv, _, svc := setupBoth(t, 1024)
	withLead(t, svc)
	body := `{"name":"p","sites":["https://x.test"],"roles":[{"name":"buyer","personas":[{"name":"Ana"}],"stories":[]}]}`
	if code := send(t, "PUT", srv.URL+"/api/project", body).StatusCode; code != 400 {
		t.Fatalf("code %d", code)
	}
}
