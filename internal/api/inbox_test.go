package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/inbox"
	"github.com/boarding-party/boardingparty/internal/live"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

type memSecrets struct {
	mu  sync.Mutex
	c   inbox.Config
	set bool
}

func (m *memSecrets) Load(context.Context) (inbox.Config, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.c, m.set, nil
}
func (m *memSecrets) Save(_ context.Context, c inbox.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.c, m.set = c, true
	return nil
}
func (m *memSecrets) Delete(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.c, m.set = inbox.Config{}, false
	return nil
}

func setupMail(t *testing.T, check func(context.Context, inbox.Config) error) (*httptest.Server, *inbox.Service, *memSecrets, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sec := &memSecrets{}
	mail := &inbox.Service{Store: st, Secrets: sec, CheckFunc: check}
	svc := &runs.Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), 0), Launcher: noopLauncher{}}
	srv := httptest.NewServer(Admin(svc, 0, testToken, live.NewHub(), mail))
	t.Cleanup(srv.Close)
	return srv, mail, sec, st
}

func mailDo(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func okCheck(context.Context, inbox.Config) error { return nil }

const putBody = `{"host":"imap.example.test","port":993,"username":"u@example.test","password":"s3cret-pw"}`

func TestMailboxGetNeverReturnsPassword(t *testing.T) {
	srv, _, sec, _ := setupMail(t, okCheck)
	sec.Save(context.Background(), inbox.Config{Host: "h.test", Port: 993, Username: "u", Password: "s3cret-pw"})
	code, body := mailDo(t, "GET", srv.URL+"/api/mailbox", "")
	if code != 200 || strings.Contains(body, "s3cret-pw") || !strings.Contains(body, `"passwordSet":true`) {
		t.Fatalf("%d %s", code, body)
	}
	code, body = mailDo(t, "PUT", srv.URL+"/api/mailbox", putBody)
	if code != 200 || strings.Contains(body, "s3cret-pw") {
		t.Fatalf("put: %d %s", code, body)
	}
}

func TestMailboxPutReportsTheCheckedLogin(t *testing.T) {
	srv, _, _, _ := setupMail(t, okCheck)
	code, body := mailDo(t, "PUT", srv.URL+"/api/mailbox", putBody)
	if code != 200 || !strings.Contains(body, `"state":"ok"`) || !strings.Contains(body, `"checked":`) {
		t.Fatalf("put: %d %s", code, body)
	}
}

func TestMailboxPutChecksBeforeSaving(t *testing.T) {
	srv, _, sec, _ := setupMail(t, func(context.Context, inbox.Config) error { return errors.New("bad login") })
	code, body := mailDo(t, "PUT", srv.URL+"/api/mailbox", putBody)
	if code != 400 || !strings.Contains(body, "Could not sign in: bad login") {
		t.Fatalf("%d %s", code, body)
	}
	if _, ok, _ := sec.Load(context.Background()); ok {
		t.Fatal("saved despite failed check")
	}
}

func TestMailboxPutKeepsPassword(t *testing.T) {
	calls := 0
	var got inbox.Config
	srv, _, sec, _ := setupMail(t, func(_ context.Context, c inbox.Config) error { calls++; got = c; return nil })
	if code, body := mailDo(t, "PUT", srv.URL+"/api/mailbox", `{"host":"h.test","username":"u","password":""}`); code != 400 {
		t.Fatalf("no saved password: %d %s", code, body)
	}
	saved := inbox.Config{Host: "Imap.Test", Port: 0, Username: "u", Password: "kept"}
	sec.Save(context.Background(), saved)
	calls = 0
	for _, b := range []string{
		`{"host":"other.test","port":993,"username":"u","password":""}`,
		`{"host":"imap.test","port":143,"username":"u","password":""}`,
		`{"host":"imap.test","port":993,"username":"u2","password":""}`,
	} {
		code, body := mailDo(t, "PUT", srv.URL+"/api/mailbox", b)
		if code != 400 || !strings.Contains(body, "Enter the app password again to change the server or username.") {
			t.Fatalf("%s -> %d %s", b, code, body)
		}
	}
	if c, _, _ := sec.Load(context.Background()); c != saved || calls != 0 {
		t.Fatalf("saved %+v, check calls %d", c, calls)
	}
	// Same mailbox (host case-insensitive, port defaulting to 993) keeps the password.
	code, body := mailDo(t, "PUT", srv.URL+"/api/mailbox", `{"host":"IMAP.test","port":993,"username":"u","password":""}`)
	if code != 200 || got.Password != "kept" {
		t.Fatalf("%d %s %+v", code, body, got)
	}
	if c, _, _ := sec.Load(context.Background()); c.Password != "kept" {
		t.Fatalf("saved %+v", c)
	}
}

func TestMailboxPutValidates(t *testing.T) {
	srv, _, _, _ := setupMail(t, okCheck)
	for _, b := range []string{
		`{"host":"https://imap.example.test","port":993,"username":"u","password":"p"}`,
		`{"host":"h.test","port":70000,"username":"u","password":"p"}`,
		`{"host":"h.test","port":-1,"username":"u","password":"p"}`,
		`{"host":"","port":993,"username":"u","password":"p"}`,
		`not json`,
	} {
		if code, body := mailDo(t, "PUT", srv.URL+"/api/mailbox", b); code != 400 {
			t.Errorf("%s -> %d %s", b, code, body)
		}
	}
}

func TestMailboxDelete(t *testing.T) {
	srv, _, sec, _ := setupMail(t, okCheck)
	sec.Save(context.Background(), inbox.Config{Host: "h", Username: "u", Password: "p"})
	if code, _ := mailDo(t, "DELETE", srv.URL+"/api/mailbox", ""); code != 204 {
		t.Fatalf("%d", code)
	}
	if _, ok, _ := sec.Load(context.Background()); ok {
		t.Fatal("not deleted")
	}
}

func TestMessagesListAndFilteredSplit(t *testing.T) {
	srv, _, _, st := setupMail(t, okCheck)
	now := time.Now()
	add := func(id string, uid uint32, persona, filtered string) {
		if _, err := st.AddMessage(store.Message{ID: id, UIDValidity: 1, UID: uid, Persona: persona, From: "a@x.test",
			To: []string{"p@x.test"}, Subject: "s-" + id, Text: "body " + id, Links: []string{"https://x.test/" + id},
			Received: now, Stored: now, Filtered: filtered}); err != nil {
			t.Fatal(err)
		}
	}
	add("m1", 1, "ann", "")
	add("m2", 2, "bob", "")
	add("m3", 3, "ann", "sender not allowed")
	_, body := mailDo(t, "GET", srv.URL+"/api/messages", "")
	if !strings.Contains(body, `"m1"`) || !strings.Contains(body, `"m2"`) || strings.Contains(body, `"m3"`) || strings.Contains(body, "body m1") {
		t.Fatalf("delivered: %s", body)
	}
	_, body = mailDo(t, "GET", srv.URL+"/api/messages?persona=ann", "")
	if !strings.Contains(body, `"m1"`) || strings.Contains(body, `"m2"`) {
		t.Fatalf("persona: %s", body)
	}
	_, body = mailDo(t, "GET", srv.URL+"/api/messages?filtered=1", "")
	if !strings.Contains(body, `"m3"`) || strings.Contains(body, `"m1"`) || !strings.Contains(body, "sender not allowed") {
		t.Fatalf("filtered: %s", body)
	}
	_, body = mailDo(t, "GET", srv.URL+"/api/messages/m1", "")
	if !strings.Contains(body, "body m1") || !strings.Contains(body, `"links"`) || !strings.Contains(body, `"to"`) {
		t.Fatalf("get: %s", body)
	}
	_, body = mailDo(t, "GET", srv.URL+"/api/messages", "")
	if body[0] != '[' {
		t.Fatal(body)
	}
	_, body = mailDo(t, "GET", srv.URL+"/api/messages?persona=nobody", "")
	if strings.TrimSpace(body) != "[]" {
		t.Fatalf("empty list must be [], got %s", body)
	}
}

func TestMessagesListShowsStartedStory(t *testing.T) {
	srv, _, _, st := setupMail(t, okCheck)
	now := time.Now()
	for i, id := range []string{"m1", "m2"} {
		if _, err := st.AddMessage(store.Message{ID: id, UIDValidity: 1, UID: uint32(i + 1), Persona: "ann", From: "a@x.test",
			Subject: "s-" + id, Received: now, Stored: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.MarkMessageFired("m1", "confirm-signup", now); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkMessageFired("m2", "", now); err != nil {
		t.Fatal(err)
	}
	_, body := mailDo(t, "GET", srv.URL+"/api/messages", "")
	if !strings.Contains(body, `"started":"confirm-signup"`) || strings.Count(body, `"started"`) != 1 {
		t.Fatalf("started: %s", body)
	}
}

func TestMessageGetUnknown404(t *testing.T) {
	srv, _, _, _ := setupMail(t, okCheck)
	if code, _ := mailDo(t, "GET", srv.URL+"/api/messages/nope", ""); code != 404 {
		t.Fatalf("%d", code)
	}
}

func TestMailboxRoutesWithoutService404(t *testing.T) {
	srv, _ := setup(t, 0)
	for _, p := range []string{"/api/mailbox", "/api/messages", "/api/messages/x"} {
		code, body := mailDo(t, "GET", srv.URL+p, "")
		if code != 404 || !strings.Contains(body, "inbox not available") {
			t.Errorf("%s: %d %s", p, code, body)
		}
	}
	if code, _ := mailDo(t, "PUT", srv.URL+"/api/mailbox", putBody); code != 404 {
		t.Fatalf("put %d", code)
	}
}

func TestMailboxChangeAndDeleteClearCursor(t *testing.T) {
	srv, _, sec, st := setupMail(t, okCheck)
	ctx := context.Background()
	sec.Save(ctx, inbox.Config{Host: "imap.example.test", Port: 993, Username: "u@example.test", Password: "s3cret-pw"})
	hasCursor := func() bool { _, _, ok, _ := st.MailCursor(); return ok }
	reset := func() { st.SetMailCursor(1, 10) }

	reset()
	if code, body := mailDo(t, "PUT", srv.URL+"/api/mailbox", putBody); code != 200 || !hasCursor() {
		t.Fatalf("same mailbox: %d %s cursor=%v", code, body, hasCursor())
	}
	if code, body := mailDo(t, "PUT", srv.URL+"/api/mailbox", `{"host":"IMAP.example.test","port":0,"username":"u@example.test","password":"new-pw"}`); code != 200 || !hasCursor() {
		t.Fatalf("same mailbox, new password: %d %s cursor=%v", code, body, hasCursor())
	}
	for _, b := range []string{
		`{"host":"imap.other.test","port":993,"username":"u@example.test","password":"pw"}`,
		`{"host":"imap.example.test","port":143,"username":"u@example.test","password":"pw"}`,
		`{"host":"imap.example.test","port":993,"username":"v@example.test","password":"pw"}`,
	} {
		reset()
		sec.Save(ctx, inbox.Config{Host: "imap.example.test", Port: 993, Username: "u@example.test", Password: "s3cret-pw"})
		if code, body := mailDo(t, "PUT", srv.URL+"/api/mailbox", b); code != 200 || hasCursor() {
			t.Fatalf("%s: %d %s cursor=%v", b, code, body, hasCursor())
		}
	}
	reset()
	if code, _ := mailDo(t, "DELETE", srv.URL+"/api/mailbox", ""); code != 204 || hasCursor() {
		t.Fatalf("delete: %d cursor=%v", code, hasCursor())
	}
}
