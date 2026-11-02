package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/files"
	"github.com/boarding-party/boardingparty/internal/live"
	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

type noopLauncher struct{}

func (noopLauncher) Start(_ context.Context, r store.Run, _ project.Project) (string, error) {
	return "sess-" + r.ID, nil
}
func (noopLauncher) SessionName(id string) string       { return "sess-" + id }
func (noopLauncher) Stop(context.Context, string) error { return nil }
func (noopLauncher) Phase(context.Context, string) (string, bool, string, error) {
	return "Running", true, "", nil
}

func (noopLauncher) Sessions(context.Context) (map[string]string, error)   { return nil, nil }
func (noopLauncher) LivePods(context.Context) (int, error)                 { return 0, nil }
func (noopLauncher) Stage(context.Context, string) (string, string, error) { return "running", "", nil }

const projYAML = `name: p
sites: ["https://x.test"]
roles:
  - name: buyer
    personas: [{name: ann}]
    stories: [{name: checkout, goal: buy a thing}]
`

func setup(t *testing.T, maxBytes int64) (*httptest.Server, *runs.Service) {
	srv, _, svc := setupBoth(t, maxBytes)
	return srv, svc
}

// setupBoth returns the admin server, the worker server and the service.
func setupBoth(t *testing.T, maxBytes int64) (*httptest.Server, *httptest.Server, *runs.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := &runs.Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), maxBytes), Launcher: noopLauncher{}}
	hub := live.NewHub()
	srv := httptest.NewServer(Admin(svc, maxBytes, testToken, hub))
	t.Cleanup(srv.Close)
	wsrv := httptest.NewServer(Worker(svc, maxBytes, hub))
	t.Cleanup(wsrv.Close)
	return srv, wsrv, svc
}

const testToken = "admin-secret"

// do sends an authorised request with the content type the CLI would use.
func do(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	if method == "POST" && strings.HasSuffix(url, "/api/project") {
		req.Header.Set("Content-Type", "application/yaml")
	} else if method == "POST" && strings.HasSuffix(url, "/api/runs") {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// startRun starts a run through the API and returns it with its token read
// from the store (the API response redacts it).
func startRun(t *testing.T, srv *httptest.Server, svc *runs.Service) store.Run {
	t.Helper()
	if r := do(t, "POST", srv.URL+"/api/project", projYAML); r.StatusCode != 204 {
		t.Fatalf("project: %d", r.StatusCode)
	}
	resp := do(t, "POST", srv.URL+"/api/runs", `{"persona":"ann","story":"checkout"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	var run store.Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	full, err := svc.Store.RunByID(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return full
}

func TestWorkerUploadNeedsActiveToken(t *testing.T) {
	srv, w, svc := setupBoth(t, 1024)
	if r := do(t, "PUT", w.URL+"/artifacts/deadbeef/a.png", "x"); r.StatusCode != 404 {
		t.Errorf("random token: %d", r.StatusCode)
	}
	run := startRun(t, srv, svc)
	if r := do(t, "PUT", w.URL+"/artifacts/"+run.Token+"/a.png", "x"); r.StatusCode != 201 {
		t.Errorf("active: %d", r.StatusCode)
	}
	if err := svc.Finish(context.Background(), run.Token, store.RunDone, "ok"); err != nil {
		t.Fatal(err)
	}
	if r := do(t, "PUT", w.URL+"/artifacts/"+run.Token+"/b.png", "x"); r.StatusCode != 404 {
		t.Errorf("finished: %d", r.StatusCode)
	}
}

func TestUploadOversizeIs413(t *testing.T) {
	srv, w, svc := setupBoth(t, 10)
	run := startRun(t, srv, svc)
	if r := do(t, "PUT", w.URL+"/artifacts/"+run.Token+"/a.png", strings.Repeat("x", 11)); r.StatusCode != 413 {
		t.Errorf("oversize: %d", r.StatusCode)
	}
}

func TestBadNameIs400(t *testing.T) {
	srv, w, svc := setupBoth(t, 1024)
	run := startRun(t, srv, svc)
	if r := do(t, "PUT", w.URL+"/artifacts/"+run.Token+"/Bad", "x"); r.StatusCode != 400 {
		t.Errorf("bad name: %d", r.StatusCode)
	}
}

func TestRunsReadEndpoints(t *testing.T) {
	srv, w, svc := setupBoth(t, 1024)
	run := startRun(t, srv, svc)
	do(t, "PUT", w.URL+"/artifacts/"+run.Token+"/a.txt", "hello")

	resp := do(t, "GET", srv.URL+"/api/runs", "")
	var list []store.Run
	json.NewDecoder(resp.Body).Decode(&list)
	if resp.StatusCode != 200 || len(list) != 1 {
		t.Fatalf("list: %d %v", resp.StatusCode, list)
	}
	if list[0].Token != "" {
		t.Errorf("token leaked in list: %q", list[0].Token)
	}

	resp = do(t, "GET", srv.URL+"/api/runs/"+run.ID, "")
	var detail struct {
		Run       store.Run   `json:"run"`
		Bugs      []store.Bug `json:"bugs"`
		Artifacts []string    `json:"artifacts"`
	}
	json.NewDecoder(resp.Body).Decode(&detail)
	if resp.StatusCode != 200 || detail.Run.ID != run.ID || len(detail.Artifacts) != 1 || detail.Artifacts[0] != "a.txt" {
		t.Fatalf("detail: %d %+v", resp.StatusCode, detail)
	}

	resp = do(t, "GET", srv.URL+"/api/runs/"+run.ID+"/artifacts/a.txt", "")
	b := new(strings.Builder)
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	b.Write(buf[:n])
	if resp.StatusCode != 200 || b.String() != "hello" {
		t.Fatalf("artifact: %d %q", resp.StatusCode, b.String())
	}
	if r := do(t, "GET", srv.URL+"/api/runs/nope", ""); r.StatusCode != 404 {
		t.Errorf("missing run: %d", r.StatusCode)
	}
	if r := do(t, "POST", srv.URL+"/api/runs", `{"persona":"x","story":"y"}`); r.StatusCode != 400 {
		t.Errorf("unknown persona: %d", r.StatusCode)
	}
}

func TestAdminDoesNotServeWorkerUpload(t *testing.T) {
	srv, _, svc := setupBoth(t, 1024)
	run := startRun(t, srv, svc)
	if r := do(t, "PUT", srv.URL+"/artifacts/"+run.Token+"/a.png", "x"); r.StatusCode != 404 && r.StatusCode != 405 {
		t.Errorf("admin served upload: %d", r.StatusCode)
	}
}

func TestWorkerServesNoAPI(t *testing.T) {
	srv, w, svc := setupBoth(t, 1024)
	run := startRun(t, srv, svc)
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/project"}, {"POST", "/api/runs"}, {"GET", "/api/runs"},
		{"GET", "/api/runs/" + run.ID}, {"GET", "/api/runs/" + run.ID + "/artifacts/a.txt"},
	} {
		if r := do(t, c.method, w.URL+c.path, projYAML); r.StatusCode != 404 {
			t.Errorf("%s %s on worker: %d", c.method, c.path, r.StatusCode)
		}
	}
	if r := do(t, "GET", w.URL+"/healthz", ""); r.StatusCode != 200 {
		t.Errorf("healthz: %d", r.StatusCode)
	}
}

func TestStartRunLauncherErrorIsGeneric(t *testing.T) {
	srv, svc := setup(t, 1024)
	svc.Launcher = failLauncher{}
	do(t, "POST", srv.URL+"/api/project", projYAML)
	resp := do(t, "POST", srv.URL+"/api/runs", `{"persona":"ann","story":"checkout"}`)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 502 || strings.Contains(string(b), "secret-detail") {
		t.Errorf("got %d %q", resp.StatusCode, b)
	}
}

type failLauncher struct{ noopLauncher }

func (failLauncher) Start(context.Context, store.Run, project.Project) (string, error) {
	return "", errors.New("secret-detail")
}

func TestStartRunBusyIs409AndTokenRedacted(t *testing.T) {
	srv, _ := setup(t, 1024)
	if r := do(t, "POST", srv.URL+"/api/project", projYAML); r.StatusCode != 204 {
		t.Fatalf("project: %d", r.StatusCode)
	}
	resp := do(t, "POST", srv.URL+"/api/runs", `{"persona":"ann","story":"checkout"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	var run store.Run
	if err := json.Unmarshal(b, &run); err != nil {
		t.Fatal(err)
	}
	if run.Token != "" {
		t.Errorf("start response leaks token: %s", b)
	}
	resp = do(t, "POST", srv.URL+"/api/runs", `{"persona":"ann","story":"checkout"}`)
	if resp.StatusCode != 409 {
		t.Fatalf("second start: %d, want 409", resp.StatusCode)
	}
	b, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "another run is active") {
		t.Errorf("message: %s", b)
	}
}

type podErrLauncher struct{ noopLauncher }

func (podErrLauncher) LivePods(context.Context) (int, error) { return 0, errors.New("secret-detail") }

func TestStartRunInternalErrorIs500NotRawText(t *testing.T) {
	srv, svc := setup(t, 1024)
	svc.Launcher = podErrLauncher{}
	do(t, "POST", srv.URL+"/api/project", projYAML)
	resp := do(t, "POST", srv.URL+"/api/runs", `{"persona":"ann","story":"checkout"}`)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 500 || strings.Contains(string(b), "secret-detail") {
		t.Errorf("got %d %q", resp.StatusCode, b)
	}
}

func TestStartRunUnknownPersonaIs400(t *testing.T) {
	srv, _ := setup(t, 1024)
	do(t, "POST", srv.URL+"/api/project", projYAML)
	resp := do(t, "POST", srv.URL+"/api/runs", `{"persona":"nobody","story":"checkout"}`)
	if resp.StatusCode != 400 {
		t.Errorf("got %d, want 400", resp.StatusCode)
	}
}

const fullProject = `{"name":"shop","sites":["https://a.test","https://b.test"],"extraHosts":["cdn.test"],"model":"m-1","nodePool":"pool=runs","resources":{"cpu":"500m","memory":"2Gi"},
"roles":[{"name":"buyer","description":"buys","canWatchRecordings":true,
"personas":[{"name":"ann","traits":["impatient","phone"],"profile":{"device":"iphone-15","techLevel":"new","assistive":"zoom"},"identity":{"email":"a@x.test","password":"pw"}}],
"stories":[{"name":"checkout","goal":"buy a thing"}]}]}`

func doJSON(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestProjectJSONRoundTrip(t *testing.T) {
	srv, _ := setup(t, 0)
	if r := do(t, "GET", srv.URL+"/api/project", ""); r.StatusCode != 404 {
		t.Fatalf("empty get: %d", r.StatusCode)
	} else if b, _ := io.ReadAll(r.Body); !strings.Contains(string(b), `{"error":"no project yet"}`) {
		t.Fatalf("body: %s", b)
	}
	if r := doJSON(t, "PUT", srv.URL+"/api/project", fullProject); r.StatusCode != 204 {
		t.Fatalf("put: %d", r.StatusCode)
	}
	r := do(t, "GET", srv.URL+"/api/project", "")
	if r.StatusCode != 200 {
		t.Fatalf("get: %d", r.StatusCode)
	}
	var got, want project.Project
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fullProject), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestProjectPutInvalidIs400(t *testing.T) {
	srv, _ := setup(t, 0)
	r := doJSON(t, "PUT", srv.URL+"/api/project", `{"name":"p","sites":[]}`)
	if b, _ := io.ReadAll(r.Body); r.StatusCode != 400 || !strings.Contains(string(b), "at least one site") {
		t.Fatalf("no sites: %d %s", r.StatusCode, b)
	}
	dup := `{"name":"p","sites":["https://x.test"],"roles":[{"name":"a","personas":[{"name":"ann"}]},{"name":"b","personas":[{"name":"ann"}]}]}`
	r = doJSON(t, "PUT", srv.URL+"/api/project", dup)
	if b, _ := io.ReadAll(r.Body); r.StatusCode != 400 || !strings.Contains(string(b), `duplicate persona "ann"`) {
		t.Fatalf("dup: %d %s", r.StatusCode, b)
	}
	if r := doJSON(t, "PUT", srv.URL+"/api/project", `{"bogus":1}`); r.StatusCode != 400 {
		t.Fatalf("unknown field: %d", r.StatusCode)
	}
	if r := do(t, "GET", srv.URL+"/api/project", ""); r.StatusCode != 404 {
		t.Fatalf("invalid put stored something: %d", r.StatusCode)
	}
}

func TestArtifactRangeAndTypes(t *testing.T) {
	srv, svc := setup(t, 1<<20)
	run := startRun(t, srv, svc)
	data := "0123456789abcdefghij"
	for _, n := range []string{"rec.webm", "shots.zip", "a.png", "bug-1.png", "b.jpg", "c.jpeg", "x.bin"} {
		if _, err := svc.Artifacts.Put(run.ID, n, strings.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/runs/"+run.ID+"/artifacts/rec.webm", nil)
	req.Header.Set("Range", "bytes=0-9")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 206 || string(b) != data[:10] {
		t.Fatalf("range: %d %q", resp.StatusCode, b)
	}
	if resp.Header.Get("Content-Range") != "bytes 0-9/20" {
		t.Fatalf("content-range %q", resp.Header.Get("Content-Range"))
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "inline" {
		t.Fatalf("webm disposition %q", cd)
	}
	for name, ct := range map[string]string{"rec.webm": "video/webm", "shots.zip": "application/zip", "a.png": "image/png", "bug-1.png": "image/png", "b.jpg": "image/jpeg", "c.jpeg": "image/jpeg", "x.bin": "application/octet-stream"} {
		r := do(t, "GET", srv.URL+"/api/runs/"+run.ID+"/artifacts/"+name, "")
		if r.StatusCode != 200 || r.Header.Get("Content-Type") != ct {
			t.Fatalf("%s: %d %q", name, r.StatusCode, r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: no nosniff", name)
		}
		if name == "shots.zip" && r.Header.Get("Content-Disposition") == "inline" {
			t.Fatal("zip must not be inline")
		}
	}
}

func TestProjectPutNeedsJSONContentType(t *testing.T) {
	srv, _ := setup(t, 0)
	req, _ := http.NewRequest("PUT", srv.URL+"/api/project", strings.NewReader(fullProject))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 415 {
		t.Fatalf("text/plain: %d", resp.StatusCode)
	}
	if r := do(t, "PUT", srv.URL+"/api/project", fullProject); r.StatusCode != 415 {
		t.Fatalf("none: %d", r.StatusCode)
	}
}

func TestArtifactHardening(t *testing.T) {
	root := t.TempDir()
	srv, svc := setup(t, 1<<20)
	run := startRun(t, srv, svc)
	for _, n := range []string{"x.html", "x.svg", "a.png"} {
		if _, err := svc.Artifacts.Put(run.ID, n, strings.NewReader("<script>1</script>")); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"x.html", "x.svg"} {
		r := do(t, "GET", srv.URL+"/api/runs/"+run.ID+"/artifacts/"+n, "")
		if r.StatusCode != 200 || r.Header.Get("Content-Type") != "application/octet-stream" ||
			r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("Content-Disposition") == "inline" {
			t.Fatalf("%s: %d %v", n, r.StatusCode, r.Header)
		}
	}
	if r := do(t, "GET", srv.URL+"/api/runs/"+run.ID+"/artifacts/a.png", ""); r.Header.Get("Content-Disposition") != "" {
		t.Fatalf("png disposition %q", r.Header.Get("Content-Disposition"))
	}
	// Symlink and directory in a run dir under a store we control.
	fsStore := artifacts.NewFS(root, 1<<20)
	svc.Artifacts = fsStore
	dir := filepath.Join(root, run.ID)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret.txt")
	os.WriteFile(secret, []byte("s"), 0o600)
	if err := os.Symlink(secret, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"link.txt", "sub"} {
		if r := do(t, "GET", srv.URL+"/api/runs/"+run.ID+"/artifacts/"+n, ""); r.StatusCode != 404 {
			t.Fatalf("%s: %d", n, r.StatusCode)
		}
	}
}

// raw sends a request with exactly the given headers.
func raw(t *testing.T, method, url, body string, h map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range h {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestAdminNeedsBearer(t *testing.T) {
	srv, svc := setup(t, 1024)
	run := startRun(t, srv, svc)
	routes := []struct{ method, path, ctype, body string }{
		{"GET", "/api/project", "", ""},
		{"PUT", "/api/project", "application/json", fullProject},
		{"POST", "/api/project", "application/yaml", projYAML},
		{"GET", "/api/runs", "", ""},
		{"POST", "/api/runs", "application/json", `{"persona":"ann","story":"checkout"}`},
		{"GET", "/api/runs/" + run.ID, "", ""},
		{"GET", "/api/runs/" + run.ID + "/artifacts/a.txt", "", ""},
		{"GET", "/api/nosuch", "", ""},
	}
	for _, auth := range []string{"", "Bearer", "Bearer ", "Bearer wrong", "admin-secret", "Basic admin-secret", "Bearer admin-secret2"} {
		for _, r := range routes {
			h := map[string]string{"Content-Type": r.ctype}
			if auth != "" {
				h["Authorization"] = auth
			}
			if c := raw(t, r.method, srv.URL+r.path, r.body, h); c != 401 {
				t.Errorf("%s %s with %q = %d, want 401", r.method, r.path, auth, c)
			}
		}
	}
	if c := raw(t, "GET", srv.URL+"/api/runs", "", map[string]string{"Authorization": "Bearer " + testToken}); c != 200 {
		t.Errorf("with bearer = %d", c)
	}
}

func TestAdminEmptyTokenDeniesAll(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := httptest.NewServer(Admin(&runs.Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), 0), Launcher: noopLauncher{}}, 0, "", live.NewHub()))
	defer srv.Close()
	for _, a := range []string{"", "Bearer ", "Bearer"} {
		if c := raw(t, "GET", srv.URL+"/api/runs", "", map[string]string{"Authorization": a}); c != 401 {
			t.Errorf("empty token with %q = %d", a, c)
		}
	}
}

func TestAdminHostGuard(t *testing.T) {
	srv, _ := setup(t, 0)
	auth := "Bearer " + testToken
	for _, host := range []string{"evil.example", "evil.example:8081", "10.0.0.1:8081", "[::1]:8081", "127.0.0.1.evil.example:80"} {
		if c := raw(t, "GET", srv.URL+"/api/runs", "", map[string]string{"Host": host, "Authorization": auth}); c != 403 {
			t.Errorf("Host %q = %d, want 403", host, c)
		}
	}
	for _, host := range []string{"127.0.0.1:8081", "localhost:1234", "LOCALHOST", "127.0.0.1"} {
		if c := raw(t, "GET", srv.URL+"/api/runs", "", map[string]string{"Host": host, "Authorization": auth}); c != 200 {
			t.Errorf("Host %q = %d, want 200", host, c)
		}
	}
}

func TestAdminRejectsBrowserRequests(t *testing.T) {
	srv, _ := setup(t, 0)
	auth := "Bearer " + testToken
	for _, h := range []map[string]string{
		{"Origin": "http://127.0.0.1:4321"},
		{"Origin": "null"},
		{"Sec-Fetch-Site": "cross-site"},
		{"Sec-Fetch-Site": "same-origin"},
	} {
		h["Authorization"] = auth
		h["Content-Type"] = "application/json"
		if c := raw(t, "POST", srv.URL+"/api/runs", `{}`, h); c != 403 {
			t.Errorf("%v = %d, want 403", h, c)
		}
	}
	if c := raw(t, "GET", srv.URL+"/api/runs", "", map[string]string{"Authorization": auth, "Sec-Fetch-Site": "none"}); c != 200 {
		t.Errorf("Sec-Fetch-Site none = %d", c)
	}
}

func TestAdminMutatingContentTypes(t *testing.T) {
	srv, _ := setup(t, 0)
	auth := "Bearer " + testToken
	bad := []string{"", "text/plain", "text/plain; charset=utf-8", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"}
	for _, ct := range bad {
		if c := raw(t, "POST", srv.URL+"/api/project", projYAML, map[string]string{"Authorization": auth, "Content-Type": ct}); c != 415 {
			t.Errorf("POST project %q = %d, want 415", ct, c)
		}
		if c := raw(t, "POST", srv.URL+"/api/runs", `{"persona":"ann","story":"checkout"}`, map[string]string{"Authorization": auth, "Content-Type": ct}); c != 415 {
			t.Errorf("POST runs %q = %d, want 415", ct, c)
		}
		if c := raw(t, "PUT", srv.URL+"/api/project", fullProject, map[string]string{"Authorization": auth, "Content-Type": ct}); c != 415 {
			t.Errorf("PUT project %q = %d, want 415", ct, c)
		}
	}
	for _, ct := range []string{"application/yaml", "application/x-yaml", "text/yaml; charset=utf-8"} {
		if c := raw(t, "POST", srv.URL+"/api/project", projYAML, map[string]string{"Authorization": auth, "Content-Type": ct}); c != 204 {
			t.Errorf("POST project %q = %d, want 204", ct, c)
		}
	}
	if c := raw(t, "POST", srv.URL+"/api/project", projYAML, map[string]string{"Authorization": auth, "Content-Type": "application/json"}); c != 415 {
		t.Errorf("POST project as json = %d, want 415", c)
	}
}

func TestWorkerUnaffectedByAdminGuard(t *testing.T) {
	srv, w, svc := setupBoth(t, 1024)
	run := startRun(t, srv, svc)
	// No bearer, a foreign Host and a browser Origin: the worker surface doesn't care.
	h := map[string]string{"Host": "bp-director.ns.svc:8080", "Origin": "http://x.test"}
	if c := raw(t, "PUT", w.URL+"/artifacts/"+run.Token+"/a.png", "x", h); c != 201 {
		t.Errorf("worker upload = %d", c)
	}
	if c := raw(t, "GET", w.URL+"/healthz", "", h); c != 200 {
		t.Errorf("worker healthz = %d", c)
	}
}

func TestProfileFieldsEndpoint(t *testing.T) {
	srv, _ := setup(t, 0)
	r := do(t, "GET", srv.URL+"/api/profile-fields", "")
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	b, _ := io.ReadAll(r.Body)
	var got []struct {
		Key, Label, Hint string
		Options          []struct{ Value, Label string }
	}
	if err := json.Unmarshal(b, &got); err != nil || len(got) != len(project.ProfileFields) || got[0].Key != "device" || len(got[0].Options) == 0 {
		t.Fatalf("%v: %s", err, b)
	}
	if strings.Contains(string(b), "uide") || !strings.Contains(string(b), `"key"`) || !strings.Contains(string(b), `"value"`) {
		t.Fatalf("keys: %s", b)
	}
}

func TestGetRunStageAndLive(t *testing.T) {
	srv, w, svc := setupBoth(t, 1024)
	run := startRun(t, srv, svc)
	get := func() map[string]any {
		var d map[string]any
		json.NewDecoder(do(t, "GET", srv.URL+"/api/runs/"+run.ID, "").Body).Decode(&d)
		return d
	}
	d := get()
	if d["stage"] != "running" || d["live"] != nil {
		t.Fatalf("before frame: %v", d)
	}
	if r := do(t, "PUT", w.URL+"/live/"+run.Token, "\xff\xd8x"); r.StatusCode != 204 {
		t.Fatalf("put live: %d", r.StatusCode)
	}
	if d = get(); d["stage"] != "running" || d["live"] != true {
		t.Fatalf("after frame: %v", d)
	}
	if err := svc.Finish(context.Background(), run.Token, store.RunDone, "ok"); err != nil {
		t.Fatal(err)
	}
	if d = get(); d["stage"] != nil || d["live"] != nil {
		t.Fatalf("finished run: %v", d)
	}
}

func TestLiveStill(t *testing.T) {
	srv, w, svc := setupBoth(t, 1024)
	run := startRun(t, srv, svc)
	url := srv.URL + "/api/runs/" + run.ID + "/live.jpg"
	resp := do(t, "GET", url, "")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 || !strings.Contains(string(b), "no frame yet") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("no frame: %d %q", resp.StatusCode, b)
	}
	if r := do(t, "PUT", w.URL+"/live/"+run.Token, "\xff\xd8x"); r.StatusCode != 204 {
		t.Fatalf("put live: %d", r.StatusCode)
	}
	resp = do(t, "GET", url, "")
	b, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "\xff\xd8x" || resp.Header.Get("Content-Type") != "image/jpeg" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("with frame: %d %q %v", resp.StatusCode, b, resp.Header)
	}
	if err := svc.Finish(context.Background(), run.Token, store.RunDone, "ok"); err != nil {
		t.Fatal(err)
	}
	if r := do(t, "GET", url, ""); r.StatusCode != 404 {
		t.Fatalf("finished run: %d", r.StatusCode)
	}
	if r := do(t, "GET", srv.URL+"/api/runs/nope/live.jpg", ""); r.StatusCode != 404 {
		t.Fatalf("unknown run: %d", r.StatusCode)
	}
}

type failingLauncher struct{ noopLauncher }

func (failingLauncher) Stage(context.Context, string) (string, string, error) {
	return "failing", "ImagePullBackOff", nil
}

type slowLauncher struct{ noopLauncher }

func (slowLauncher) Stage(ctx context.Context, _ string) (string, string, error) {
	<-ctx.Done()
	return "", "", ctx.Err()
}

func TestRunListAndDetailCarryStage(t *testing.T) {
	srv, w, svc := setupBoth(t, 1024)
	svc.Launcher = failingLauncher{}
	run := startRun(t, srv, svc)
	do(t, "PUT", w.URL+"/live/"+run.Token, "\xff\xd8x")
	var list []map[string]any
	json.NewDecoder(do(t, "GET", srv.URL+"/api/runs", "").Body).Decode(&list)
	if len(list) != 1 || list[0]["stage"] != "failing" || list[0]["stageReason"] != "ImagePullBackOff" || list[0]["live"] != true {
		t.Fatalf("list: %v", list)
	}
	var d map[string]any
	json.NewDecoder(do(t, "GET", srv.URL+"/api/runs/"+run.ID, "").Body).Decode(&d)
	if d["stage"] != "failing" || d["stageReason"] != "ImagePullBackOff" {
		t.Fatalf("detail: %v", d)
	}
	svc.Finish(context.Background(), run.Token, store.RunDone, "ok")
	list = nil
	json.NewDecoder(do(t, "GET", srv.URL+"/api/runs", "").Body).Decode(&list)
	if list[0]["stage"] != nil || list[0]["live"] != nil {
		t.Fatalf("finished list item: %v", list[0])
	}
}

func TestStageLookupTimesOutWithoutFailingRequest(t *testing.T) {
	srv, _, svc := setupBoth(t, 1024)
	run := startRun(t, srv, svc)
	svc.Launcher = slowLauncher{}
	start := time.Now()
	resp := do(t, "GET", srv.URL+"/api/runs/"+run.ID, "")
	var d map[string]any
	json.NewDecoder(resp.Body).Decode(&d)
	if resp.StatusCode != 200 || d["stage"] != nil || time.Since(start) > 4*time.Second {
		t.Fatalf("got %d %v after %v", resp.StatusCode, d, time.Since(start))
	}
}

type countingSlowLauncher struct {
	noopLauncher
	calls *int
}

func (l countingSlowLauncher) Stage(ctx context.Context, _ string) (string, string, error) {
	*l.calls++
	<-ctx.Done()
	return "", "", ctx.Err()
}

func TestRunListStageLookupsShareOneDeadline(t *testing.T) {
	old := listStageBudget
	listStageBudget = 300 * time.Millisecond
	defer func() { listStageBudget = old }()
	srv, _, svc := setupBoth(t, 1024)
	run := startRun(t, srv, svc)
	// Two more active runs, written straight to the store.
	for i := 0; i < 2; i++ {
		r := run
		r.ID, r.Token = fmt.Sprintf("extra%d", i), fmt.Sprintf("tok%d", i)
		if err := svc.Store.CreateRun(r); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	svc.Launcher = countingSlowLauncher{calls: &calls}
	start := time.Now()
	resp := do(t, "GET", srv.URL+"/api/runs", "")
	var list []map[string]any
	json.NewDecoder(resp.Body).Decode(&list)
	if resp.StatusCode != 200 || len(list) != 3 || time.Since(start) > time.Second {
		t.Fatalf("got %d, %d runs after %v", resp.StatusCode, len(list), time.Since(start))
	}
	for _, it := range list {
		if it["stage"] != nil {
			t.Fatalf("stage not omitted: %v", it)
		}
	}
}

// ---- files ----

func putFile(t *testing.T, base, name, scope, body string) *http.Response {
	t.Helper()
	u := base + "/api/files/" + name
	if scope != "" {
		u += "?scope=" + url.QueryEscape(scope)
	}
	req, _ := http.NewRequest("PUT", u, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func withFiles(svc *runs.Service, t *testing.T) *files.Store {
	fs := files.New(t.TempDir())
	svc.Files = fs
	return fs
}

func TestAdminFilesCRUD(t *testing.T) {
	srv, svc := setup(t, 0)
	withFiles(svc, t)
	if r := do(t, "POST", srv.URL+"/api/project", projYAML); r.StatusCode != 204 {
		t.Fatalf("project: %d", r.StatusCode)
	}
	if r := putFile(t, srv.URL, "logo.png", "", "\x89PNG\r\n\x1a\n0000"); r.StatusCode != 200 {
		t.Fatalf("put project file: %d", r.StatusCode)
	}
	if r := putFile(t, srv.URL, "cv.pdf", "persona:ann", "%PDF-1.4 x"); r.StatusCode != 200 {
		t.Fatalf("put persona file: %d", r.StatusCode)
	}
	// Replace keeps one entry.
	if r := putFile(t, srv.URL, "logo.png", "project", "\x89PNG\r\n\x1a\n1111111"); r.StatusCode != 200 {
		t.Fatalf("replace: %d", r.StatusCode)
	}
	r := do(t, "GET", srv.URL+"/api/files", "")
	var got []files.Info
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil || r.StatusCode != 200 {
		t.Fatalf("list: %d %v", r.StatusCode, err)
	}
	if len(got) != 2 || got[0].Name != "logo.png" || got[0].Scope != "project" || got[0].Size != 15 || got[0].ContentType != "image/png" ||
		got[1].Name != "cv.pdf" || got[1].Scope != "persona:ann" || got[1].ContentType != "application/pdf" || got[1].Uploaded.IsZero() {
		t.Fatalf("list = %+v", got)
	}
	raw, _ := json.Marshal(got[0])
	for _, k := range []string{`"name"`, `"scope"`, `"size"`, `"contentType"`, `"uploaded"`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("json %s missing %s", raw, k)
		}
	}
	if r := do(t, "DELETE", srv.URL+"/api/files/cv.pdf?scope=persona:ann", ""); r.StatusCode != 204 {
		t.Fatalf("delete: %d", r.StatusCode)
	}
	if r := do(t, "DELETE", srv.URL+"/api/files/cv.pdf?scope=persona:ann", ""); r.StatusCode != 404 {
		t.Fatalf("delete again: %d", r.StatusCode)
	}
	if r := do(t, "DELETE", srv.URL+"/api/files/logo.png", ""); r.StatusCode != 204 {
		t.Fatalf("delete default scope: %d", r.StatusCode)
	}
	r = do(t, "GET", srv.URL+"/api/files", "")
	if b, _ := io.ReadAll(r.Body); strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("empty list = %s", b)
	}
}

func TestAdminFilesRejects(t *testing.T) {
	srv, svc := setup(t, 0)
	fs := withFiles(svc, t)
	fs.MaxFile, fs.MaxTotal = 10, 15
	do(t, "POST", srv.URL+"/api/project", projYAML)
	body := func(r *http.Response) string { b, _ := io.ReadAll(r.Body); return string(b) }

	if r := putFile(t, srv.URL, "Bad_Name.png", "", "x"); r.StatusCode != 400 || !strings.Contains(body(r), "bad file name") {
		t.Errorf("bad name: %d", r.StatusCode)
	}
	if r := putFile(t, srv.URL, "a.txt", "persona:ghost", "x"); r.StatusCode != 400 || !strings.Contains(body(r), "unknown persona") {
		t.Errorf("unknown persona: %d", r.StatusCode)
	}
	if r := putFile(t, srv.URL, "a.txt", "everyone", "x"); r.StatusCode != 400 {
		t.Errorf("bad scope: %d", r.StatusCode)
	}
	if r := putFile(t, srv.URL, "a.txt", "", strings.Repeat("x", 11)); r.StatusCode != 413 || !strings.Contains(body(r), "limit") {
		t.Errorf("too large: %d", r.StatusCode)
	}
	if r := putFile(t, srv.URL, "a.txt", "", strings.Repeat("x", 10)); r.StatusCode != 200 {
		t.Fatalf("a: %d", r.StatusCode)
	}
	if r := putFile(t, srv.URL, "b.txt", "", strings.Repeat("x", 6)); r.StatusCode != 413 || !strings.Contains(body(r), "total") {
		t.Errorf("total: %d", r.StatusCode)
	}
	// The guard still applies to the new routes.
	req, _ := http.NewRequest("PUT", srv.URL+"/api/files/a.txt", strings.NewReader("x"))
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
		t.Errorf("no bearer: %v %v", resp, err)
	}
	req, _ = http.NewRequest("PUT", srv.URL+"/api/files/a.txt", strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Origin", "https://evil.test")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 403 {
		t.Errorf("origin: %v %v", resp, err)
	}
}

func TestWorkerFiles(t *testing.T) {
	srv, wsrv, svc := setupBoth(t, 0)
	fs := withFiles(svc, t)
	run := startRun(t, srv, svc) // persona ann
	fs.Put("project", "logo.png", strings.NewReader("project-logo"))
	fs.Put("project", "shared.csv", strings.NewReader("a,b"))
	fs.Put("persona:ann", "logo.png", strings.NewReader("ann-logo"))
	fs.Put("persona:bob", "bob.pdf", strings.NewReader("bob"))
	get := func(path string) (int, string) {
		resp, err := http.Get(wsrv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, b := get("/files/" + run.Token + "/")
	var names []string
	if json.Unmarshal([]byte(b), &names) != nil || code != 200 || strings.Join(names, ",") != "logo.png,shared.csv" {
		t.Fatalf("list: %d %s", code, b)
	}
	if code, b := get("/files/" + run.Token + "/logo.png"); code != 200 || b != "ann-logo" {
		t.Errorf("persona wins: %d %q", code, b)
	}
	if code, b := get("/files/" + run.Token + "/shared.csv"); code != 200 || b != "a,b" {
		t.Errorf("project file: %d %q", code, b)
	}
	if code, _ := get("/files/" + run.Token + "/bob.pdf"); code != 404 {
		t.Errorf("other persona's file: %d", code)
	}
	if code, _ := get("/files/" + run.Token + "/..%2Fx"); code != 404 {
		t.Errorf("traversal: %d", code)
	}
	if code, _ := get("/files/deadbeef/"); code != 404 {
		t.Errorf("unknown token list: %d", code)
	}
	if code, _ := get("/files/deadbeef/logo.png"); code != 404 {
		t.Errorf("unknown token file: %d", code)
	}
	if err := svc.Finish(context.Background(), run.Token, store.RunDone, "ok"); err != nil {
		t.Fatal(err)
	}
	if code, _ := get("/files/" + run.Token + "/"); code != 404 {
		t.Errorf("inactive list: %d", code)
	}
	if code, _ := get("/files/" + run.Token + "/logo.png"); code != 404 {
		t.Errorf("inactive file: %d", code)
	}
	// The admin surface is not on the worker listener.
	if code, _ := get("/api/files"); code != 404 {
		t.Errorf("worker serves /api/files: %d", code)
	}
}

func TestAdminFilesTooLargeWithoutContentLength(t *testing.T) {
	srv, svc := setup(t, 0)
	fs := withFiles(svc, t)
	fs.MaxFile = 10
	req, _ := http.NewRequest("PUT", srv.URL+"/api/files/a.bin", io.NopCloser(strings.NewReader(strings.Repeat("x", 50)))) // unknown length: chunked
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("chunked oversize: %d", resp.StatusCode)
	}
	if l, _ := fs.List(); len(l) != 0 {
		t.Fatalf("stored %+v", l)
	}
}
