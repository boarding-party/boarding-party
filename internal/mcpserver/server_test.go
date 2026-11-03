package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/files"
	"github.com/boarding-party/boardingparty/internal/inbox"
	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeLauncher struct{ mu sync.Mutex }

func (f *fakeLauncher) Start(_ context.Context, r store.Run, _ project.Project) (string, error) {
	return "sess-" + r.ID, nil
}
func (f *fakeLauncher) SessionName(id string) string       { return "sess-" + id }
func (f *fakeLauncher) Stop(context.Context, string) error { return nil }
func (f *fakeLauncher) Phase(context.Context, string) (string, bool, string, error) {
	return "Running", true, "", nil
}

func (f *fakeLauncher) Sessions(context.Context) (map[string]string, error) { return nil, nil }
func (f *fakeLauncher) LivePods(context.Context) (int, error)               { return 0, nil }

const director = "http://director.test"

func setup(t *testing.T) (*runs.Service, *store.Store, *httptest.Server, store.Run) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p := project.Project{Name: "p", Sites: []string{"https://x.test"}, Roles: []project.Role{{
		Name: "buyer",
		Personas: []project.Persona{{Name: "ann", Traits: []string{"impatient"},
			Profile:  project.Profile{Device: "iphone-15", Patience: "impatient"},
			Identity: map[string]string{"email": "ann@example.test"}}},
		Stories: []project.Story{{Name: "checkout", Goal: "buy a thing"}},
	}}}
	if err := st.PutProject(p); err != nil {
		t.Fatal(err)
	}
	svc := &runs.Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), 1024), Launcher: &fakeLauncher{}}
	run, err := svc.Start(context.Background(), "ann", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(svc, st, director, nil))
	t.Cleanup(srv.Close)
	return svc, st, srv, run
}

func connect(t *testing.T, srv *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp/run/" + token}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

func TestNextAssignmentReturnsPersonaAndStory(t *testing.T) {
	_, st, srv, run := setup(t)
	if err := st.SavePersonaState("ann", json.RawMessage(`{"seen":1}`)); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, srv, run.Token)
	text, isErr := call(t, cs, "next_assignment", nil)
	if isErr {
		t.Fatalf("error: %s", text)
	}
	var got struct {
		Persona struct {
			Name     string            `json:"name"`
			Role     string            `json:"role"`
			Traits   []string          `json:"traits"`
			Identity map[string]string `json:"identity"`
			Profile  map[string]string `json:"profile"`
			Guidance []string          `json:"guidance"`
		} `json:"persona"`
		Story struct {
			Name string `json:"name"`
			Goal string `json:"goal"`
		} `json:"story"`
		Sites        []string        `json:"sites"`
		PersonaState json.RawMessage `json:"personaState"`
		UploadURL    string          `json:"uploadURL"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	if got.Persona.Name != "ann" || got.Persona.Identity["email"] != "ann@example.test" || got.Persona.Traits[0] != "impatient" {
		t.Errorf("persona: %+v", got.Persona)
	}
	if got.Persona.Profile["device"] != "iPhone 15" || got.Persona.Profile["patience"] != "Gives up quickly" || len(got.Persona.Profile) != 2 || len(got.Persona.Guidance) != 2 {
		t.Errorf("profile: %+v", got.Persona)
	}
	if got.Story.Name != "checkout" || got.Story.Goal != "buy a thing" {
		t.Errorf("story: %+v", got.Story)
	}
	if len(got.Sites) != 1 || got.Sites[0] != "https://x.test" {
		t.Errorf("sites: %v", got.Sites)
	}
	if string(got.PersonaState) != `{"seen":1}` {
		t.Errorf("state: %s", got.PersonaState)
	}
	if want := director + "/artifacts/" + run.Token + "/"; got.UploadURL != want {
		t.Errorf("uploadURL %q want %q", got.UploadURL, want)
	}

	if text, isErr := call(t, cs, "persona_save", map[string]any{"state": map[string]any{"x": 2}}); isErr || text != "saved" {
		t.Fatalf("persona_save: %q %v", text, isErr)
	}
	s, _ := st.PersonaState("ann")
	if string(s) != `{"x":2}` {
		t.Errorf("stored state %s", s)
	}
}

func TestUnknownTokenEveryToolErrors(t *testing.T) {
	_, _, srv, _ := setup(t)
	cs := connect(t, srv, "nope")
	calls := map[string]map[string]any{
		"next_assignment": nil,
		"persona_save":    {"state": map[string]any{}},
		"report_bug":      {"summary": "s", "step": "st", "evidence": "e", "severity_guess": "low", "note": "n"},
		"finish_run":      {"outcome": "done", "summary": "x"},
	}
	for name, args := range calls {
		text, isErr := call(t, cs, name, args)
		if !isErr || text != "unknown or finished run" {
			t.Errorf("%s: isErr=%v text=%q", name, isErr, text)
		}
	}
}

func TestFinishThenToolsError(t *testing.T) {
	_, st, srv, run := setup(t)
	cs := connect(t, srv, run.Token)
	if text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "stuck", "summary": "blocked"}); isErr || text != "finished" {
		t.Fatalf("finish: %q %v", text, isErr)
	}
	r, _ := st.RunByID(run.ID)
	if r.State != store.RunStuck || r.Outcome != "blocked" {
		t.Errorf("run: %+v", r)
	}
	for name, args := range map[string]map[string]any{"next_assignment": nil, "finish_run": {"outcome": "done", "summary": "x"}} {
		text, isErr := call(t, cs, name, args)
		if !isErr || text != "unknown or finished run" {
			t.Errorf("%s: %v %q", name, isErr, text)
		}
	}
}

func TestReportBugStored(t *testing.T) {
	_, st, srv, run := setup(t)
	cs := connect(t, srv, run.Token)
	text, isErr := call(t, cs, "report_bug", map[string]any{
		"summary": "button dead", "step": "3", "evidence": "shot.png", "severity_guess": "high", "note": "n"})
	if isErr || text == "" {
		t.Fatalf("report_bug: %q %v", text, isErr)
	}
	bugs, err := st.Bugs(run.ID)
	if err != nil || len(bugs) != 1 {
		t.Fatalf("bugs %v %v", bugs, err)
	}
	b := bugs[0]
	if b.ID != text || b.Summary != "button dead" || b.Severity != "high" || b.Evidence != "shot.png" || b.Step != "3" {
		t.Errorf("bug %+v (id returned %q)", b, text)
	}
	if text, isErr := call(t, cs, "report_bug", map[string]any{"summary": "s", "severity_guess": "critical"}); !isErr {
		t.Errorf("bad severity accepted: %q", text)
	}
}

func TestFinishRejectsBadOutcome(t *testing.T) {
	_, st, srv, run := setup(t)
	cs := connect(t, srv, run.Token)
	if text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "pending", "summary": "x"}); !isErr {
		t.Fatalf("accepted: %q", text)
	}
	r, _ := st.RunByID(run.ID)
	if r.State != store.RunRunning {
		t.Errorf("state %s", r.State)
	}
}

// Two active runs for two personas: each token only ever touches its own
// persona state, bugs and run state. Runs are created in the store directly
// because Service.Start allows only one active run at a time.
func TestCrossRunIsolation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p := project.Project{Name: "p", Sites: []string{"https://x.test"}, Roles: []project.Role{{
		Name:     "buyer",
		Personas: []project.Persona{{Name: "ann"}, {Name: "bob"}},
		Stories:  []project.Story{{Name: "checkout", Goal: "buy a thing"}},
	}}}
	if err := st.PutProject(p); err != nil {
		t.Fatal(err)
	}
	svc := &runs.Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), 1024), Launcher: &fakeLauncher{}}
	mk := func(id, token, persona string) store.Run {
		r := store.Run{ID: id, Token: token, Persona: persona, Role: "buyer", Story: "checkout",
			State: store.RunRunning, Session: "sess-" + id, Started: time.Now().UTC()}
		if err := st.CreateRun(r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b := mk("run-a", "token-a", "ann"), mk("run-b", "token-b", "bob")
	if err := st.SavePersonaState("bob", json.RawMessage(`{"owner":"bob"}`)); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(svc, st, director, nil))
	t.Cleanup(srv.Close)

	cs := connect(t, srv, a.Token)
	if text, isErr := call(t, cs, "persona_save", map[string]any{"state": map[string]any{"owner": "ann"}}); isErr {
		t.Fatalf("persona_save: %s", text)
	}
	if text, isErr := call(t, cs, "report_bug", map[string]any{
		"summary": "bug for A", "step": "1", "evidence": "e", "severity_guess": "low", "note": "n"}); isErr {
		t.Fatalf("report_bug: %s", text)
	}
	if text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "done", "summary": "A done"}); isErr {
		t.Fatalf("finish_run: %s", text)
	}

	if s, _ := st.PersonaState("ann"); string(s) != `{"owner":"ann"}` {
		t.Errorf("ann state = %s", s)
	}
	if s, _ := st.PersonaState("bob"); string(s) != `{"owner":"bob"}` {
		t.Errorf("bob state touched by run A: %s", s)
	}
	if bugs, _ := st.Bugs(b.ID); len(bugs) != 0 {
		t.Errorf("run B has bugs from run A: %+v", bugs)
	}
	if bugs, _ := st.Bugs(a.ID); len(bugs) != 1 {
		t.Errorf("run A bugs = %+v", bugs)
	}
	ra, _ := st.RunByID(a.ID)
	rb, _ := st.RunByID(b.ID)
	if ra.State != store.RunDone {
		t.Errorf("run A state %s", ra.State)
	}
	if rb.State != store.RunRunning || rb.Outcome != "" {
		t.Errorf("run B affected: %+v", rb)
	}
}

func (f *fakeLauncher) Stage(context.Context, string) (string, string, error) {
	return "running", "", nil
}

func TestNextAssignmentListsFiles(t *testing.T) {
	svc, _, srv, run := setup(t)
	fs := files.New(t.TempDir())
	svc.Files = fs
	fs.Put("project", "logo.png", strings.NewReader("p"))
	fs.Put("persona:ann", "cv.pdf", strings.NewReader("a"))
	fs.Put("persona:bob", "bob.pdf", strings.NewReader("b"))
	text, isErr := call(t, connect(t, srv, run.Token), "next_assignment", nil)
	if isErr {
		t.Fatal(text)
	}
	var got struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Files, ",") != "cv.pdf,logo.png" {
		t.Errorf("files = %v", got.Files)
	}
}

func TestNextAssignmentFilesEmptyIsList(t *testing.T) {
	_, _, srv, run := setup(t) // no file store at all
	text, _ := call(t, connect(t, srv, run.Token), "next_assignment", nil)
	if !strings.Contains(text, `"files":[]`) {
		t.Errorf("want an empty files list, got %s", text)
	}
}

// fakeMail is a real inbox.Service (for Wait/Poke) whose status the test controls.
type fakeMail struct {
	*inbox.Service
	state string
}

func (f fakeMail) Status() inbox.Status { return inbox.Status{State: f.state} }

func setupMail(t *testing.T, state string) (*store.Store, *httptest.Server, store.Run, fakeMail) {
	t.Helper()
	svc, st, _, run := setup(t)
	mb := fakeMail{Service: &inbox.Service{Store: st}, state: state}
	srv := httptest.NewServer(handler(svc, st, director, mb))
	t.Cleanup(srv.Close)
	return st, srv, run, mb
}

func addMsg(t *testing.T, st *store.Store, id, persona, filtered string, links ...string) {
	t.Helper()
	_, err := st.AddMessage(store.Message{ID: id, UIDValidity: 1, UID: uint32(len(id)) + uint32(id[0]), Persona: persona,
		From: "Shop <hi@shop.test>", Subject: "Welcome " + id, Text: "body " + id, Links: links,
		Received: time.Now(), Filtered: filtered})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMessagesListOnlyOwnPersona(t *testing.T) {
	st, srv, run, _ := setupMail(t, "ok")
	addMsg(t, st, "mine", "ann", "")
	addMsg(t, st, "theirs", "bob", "")
	addMsg(t, st, "spam", "ann", "sender-not-allowed")
	out, isErr := call(t, connect(t, srv, run.Token), "messages_list", nil)
	if isErr {
		t.Fatal(out)
	}
	var got []map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(got) != 1 || got[0]["id"] != "mine" || got[0]["subject"] == "" || got[0]["from"] == "" || got[0]["received"] == "" {
		t.Fatalf("got %s", out)
	}
}

func TestMessageReadOtherPersonaNotFound(t *testing.T) {
	st, srv, run, _ := setupMail(t, "ok")
	addMsg(t, st, "theirs", "bob", "")
	addMsg(t, st, "spam", "ann", "sender-not-allowed")
	cs := connect(t, srv, run.Token)
	for _, id := range []string{"theirs", "spam", "nope"} {
		out, isErr := call(t, cs, "message_read", map[string]any{"id": id})
		if !isErr || out != "message not found" {
			t.Errorf("%s: isErr=%v out=%q", id, isErr, out)
		}
	}
}

func TestMessageReadMarksAllowedLinks(t *testing.T) {
	st, srv, run, _ := setupMail(t, "ok")
	if err := st.PutProject(project.Project{Name: "p", Sites: []string{"https://x.test"}, ExtraHosts: []string{"auth.test"},
		Roles: []project.Role{{Name: "buyer", Personas: []project.Persona{{Name: "ann"}},
			Stories: []project.Story{{Name: "checkout"}}}}}); err != nil {
		t.Fatal(err)
	}
	addMsg(t, st, "m1", "ann", "", "https://X.test/verify?t=1", "https://auth.test/x", "https://evil.test/x",
		"https://sub.x.test/", "ftp://x.test/f", "not a url")
	out, isErr := call(t, connect(t, srv, run.Token), "message_read", map[string]any{"id": "m1"})
	if isErr {
		t.Fatal(out)
	}
	var got struct {
		Text  string `json:"text"`
		Links []struct {
			URL     string `json:"url"`
			Allowed bool   `json:"allowed"`
		} `json:"links"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	want := []bool{true, true, false, false, false, false}
	if got.Text != "body m1" || len(got.Links) != len(want) {
		t.Fatalf("got %s", out)
	}
	for i, l := range got.Links {
		if l.Allowed != want[i] {
			t.Errorf("%s allowed=%v want %v", l.URL, l.Allowed, want[i])
		}
	}
}

func TestMessageWaitClampsAndMatches(t *testing.T) {
	if clampWait(0) != 60*time.Second || clampWait(-5) != 60*time.Second || clampWait(999) != 120*time.Second ||
		clampWait(1) != time.Second || clampWait(30) != 30*time.Second {
		t.Fatal("clampWait")
	}
	st, srv, run, mb := setupMail(t, "ok")
	cs := connect(t, srv, run.Token)

	waitUnit = 10 * time.Millisecond // 999 -> 120 units = 1.2s
	t.Cleanup(func() { waitUnit = time.Second })
	out, isErr := call(t, cs, "message_wait", map[string]any{"timeout_seconds": 999})
	if isErr || out != "no matching message yet" {
		t.Fatalf("timeout: %v %q", isErr, out)
	}

	waitUnit = time.Second
	go func() {
		time.Sleep(200 * time.Millisecond)
		addMsg(t, st, "other", "ann", "")
		addMsg(t, st, "verify", "ann", "")
		mb.Notify() // the poller does this after storing mail
	}()
	out, isErr = call(t, cs, "message_wait", map[string]any{"subject_contains": "WELCOME VER", "from_contains": "shop.TEST", "timeout_seconds": 10})
	if isErr || !strings.Contains(out, `"id":"verify"`) {
		t.Fatalf("match: %v %q", isErr, out)
	}
}

func TestAssignmentInboxFlag(t *testing.T) {
	flag := func(srv *httptest.Server, run store.Run) bool {
		out, isErr := call(t, connect(t, srv, run.Token), "next_assignment", nil)
		if isErr {
			t.Fatal(out)
		}
		var a struct {
			Inbox bool `json:"inbox"`
		}
		if err := json.Unmarshal([]byte(out), &a); err != nil {
			t.Fatal(err)
		}
		return a.Inbox
	}
	_, srv, run, _ := setupMail(t, "ok")
	if !flag(srv, run) {
		t.Error("ok mailbox + email: want true")
	}
	_, srv, run, _ = setupMail(t, "error")
	if !flag(srv, run) {
		t.Error("configured but erroring mailbox: want true")
	}
	_, srv, run, _ = setupMail(t, "off")
	if flag(srv, run) {
		t.Error("off: want false")
	}
	_, _, srv, run = setup(t) // nil mail
	if flag(srv, run) {
		t.Error("nil mail: want false")
	}
	out, isErr := call(t, connect(t, srv, run.Token), "messages_list", nil)
	if !isErr || out != "no mailbox is set up for this project" {
		t.Errorf("nil mail tool: %v %q", isErr, out)
	}
}

func TestMailToolsInactiveToken(t *testing.T) {
	_, srv, _, _ := setupMail(t, "ok")
	cs := connect(t, srv, "bogus")
	for _, n := range []string{"messages_list", "message_read", "message_wait"} {
		var args map[string]any
		if n == "message_read" {
			args = map[string]any{"id": "x"}
		}
		if out, isErr := call(t, cs, n, args); !isErr || out != errNotActive {
			t.Errorf("%s: %v %q", n, isErr, out)
		}
	}
}
