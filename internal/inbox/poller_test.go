package inbox

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-imap/server"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// The memory backend's only user.
const (
	testUser = "username"
	testPass = "password"
)

type memSecrets struct {
	mu  sync.Mutex
	cfg *Config
}

func (m *memSecrets) Load(context.Context) (Config, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return Config{}, false, nil
	}
	return *m.cfg, true, nil
}

func (m *memSecrets) Save(_ context.Context, c Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = &c
	return nil
}

func (m *memSecrets) Delete(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = nil
	return nil
}

// testServer is a TLS IMAP server over the memory backend.
type testServer struct {
	host string
	port int
	tls  *tls.Config // client side: trusts the server's cert
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "imap.test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

func startServer(t *testing.T) *testServer {
	t.Helper()
	cert, pool := selfSigned(t)
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	s := server.New(memory.New())
	go s.Serve(l)
	t.Cleanup(func() { s.Close() })
	addr := l.Addr().(*net.TCPAddr)
	return &testServer{host: "127.0.0.1", port: addr.Port, tls: &tls.Config{RootCAs: pool}}
}

func (ts *testServer) config(pass string) Config {
	return Config{Host: ts.host, Port: ts.port, Username: testUser, Password: pass}
}

func (ts *testServer) client(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.DialTLS(net.JoinHostPort(ts.host, strconv.Itoa(ts.port)), ts.tls)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(testUser, testPass); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Logout() })
	return c
}

// add appends raw to INBOX through a second IMAP connection.
func (ts *testServer) add(t *testing.T, raw string) {
	t.Helper()
	c := ts.client(t)
	if err := c.Append("INBOX", nil, time.Now(), bytes.NewBufferString(raw)); err != nil {
		t.Fatal(err)
	}
}

func goodMail(subject string) string {
	return "From: Shop <no-reply@smesolutions.uk>\r\n" +
		"To: cast+john@ex.test\r\n" +
		"Subject: " + subject + "\r\n" +
		"Authentication-Results: mx.google.com;\r\n" +
		"       dkim=pass header.i=@smesolutions.uk header.s=s1 header.b=abc;\r\n" +
		"       spf=pass smtp.mailfrom=smesolutions.uk;\r\n" +
		"       dmarc=pass (p=NONE sp=NONE dis=NONE) header.from=smesolutions.uk\r\n" +
		"\r\n" +
		"hello https://smesolutions.uk/verify?t=1\r\n"
}

func testProject() project.Project {
	return project.Project{
		Roles: []project.Role{{Name: "r", Personas: []project.Persona{
			{Name: "John", Identity: map[string]string{"email": "cast+john@ex.test"}},
			{Name: "Ada", Identity: map[string]string{"email": "cast+ada@ex.test"}},
		}}},
		Inbox: &project.Inbox{AllowFrom: []string{"smesolutions.uk"}},
	}
}

func newService(t *testing.T, ts *testServer, pass string) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.PutProject(testProject()); err != nil {
		t.Fatal(err)
	}
	sec := &memSecrets{}
	if ts != nil {
		c := ts.config(pass)
		sec.cfg = &c
	}
	var tlsCfg *tls.Config
	if ts != nil {
		tlsCfg = ts.tls
	}
	return &Service{Store: st, Secrets: sec, TLS: tlsCfg, Every: 10 * time.Millisecond, Backoff: 10 * time.Millisecond}
}

func all(t *testing.T, s *Service) []store.Message {
	t.Helper()
	var out []store.Message
	for _, f := range []bool{false, true} {
		ms, err := s.Store.Messages(store.MessageQuery{Filtered: f, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ms...)
	}
	slices.SortFunc(out, func(a, b store.Message) int { return int(a.UID) - int(b.UID) })
	return out
}

func runCycle(t *testing.T, s *Service) {
	t.Helper()
	s.cycle(context.Background())
	if st := s.Status(); st.State != "ok" {
		t.Fatalf("status = %+v, want ok", st)
	}
}

func TestFirstConnectSkipsExistingMail(t *testing.T) {
	ts := startServer(t)
	s := newService(t, ts, testPass)
	runCycle(t, s)
	if got := all(t, s); len(got) != 0 {
		t.Fatalf("stored %d messages, want 0", len(got))
	}
	v, last, ok, err := s.Store.MailCursor()
	if err != nil || !ok || v != 1 || last != 6 {
		t.Fatalf("cursor = (%d, %d, %v, %v), want (1, 6, true)", v, last, ok, err)
	}
}

func TestNewMailIsRoutedAndStored(t *testing.T) {
	ts := startServer(t)
	s := newService(t, ts, testPass)
	runCycle(t, s)
	ts.add(t, goodMail("welcome"))
	runCycle(t, s)
	got := all(t, s)
	if len(got) != 1 {
		t.Fatalf("stored %d, want 1: %+v", len(got), got)
	}
	m := got[0]
	if m.Persona != "John" || m.Filtered != "" || m.Subject != "welcome" || m.UID != 7 || m.UIDValidity != 1 {
		t.Fatalf("message = %+v", m)
	}
	if m.Mailbox != strings.ToLower(ts.host)+"|"+testUser {
		t.Fatalf("mailbox = %q", m.Mailbox)
	}
	if len(m.ID) != 16 || strings.Trim(m.ID, "0123456789abcdef") != "" {
		t.Fatalf("id %q is not 16 hex chars", m.ID)
	}
	if len(m.Links) != 1 || m.Links[0] != "https://smesolutions.uk/verify?t=1" {
		t.Fatalf("links = %v", m.Links)
	}
	runCycle(t, s)
	if got := all(t, s); len(got) != 1 {
		t.Fatalf("second cycle: stored %d, want 1", len(got))
	}
	if _, last, _, _ := s.Store.MailCursor(); last != 7 {
		t.Fatalf("cursor last = %d, want 7", last)
	}
}

func TestTooLargeAndGarbageDoNotStall(t *testing.T) {
	ts := startServer(t)
	s := newService(t, ts, testPass)
	runCycle(t, s)
	big := goodMail("huge") + strings.Repeat("x", 2<<20) + "\r\n"
	ts.add(t, big)
	ts.add(t, "this is not an email at all\r\n")
	ts.add(t, goodMail("fine"))
	runCycle(t, s)
	got := all(t, s)
	if len(got) != 3 {
		t.Fatalf("stored %d, want 3: %+v", len(got), got)
	}
	if got[0].Filtered != "too large" || got[0].Subject != "huge" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Filtered != "could not read" {
		t.Errorf("second = %+v", got[1])
	}
	if got[2].Filtered != "" || got[2].Persona != "John" || got[2].Subject != "fine" {
		t.Errorf("third = %+v", got[2])
	}
}

func TestValidityChangeResetsCursor(t *testing.T) {
	ts := startServer(t)
	s := newService(t, ts, testPass)
	ts.add(t, goodMail("before"))
	if err := s.Store.SetMailCursor(999, 5); err != nil {
		t.Fatal(err)
	}
	runCycle(t, s)
	if got := all(t, s); len(got) != 0 {
		t.Fatalf("stored %d, want 0", len(got))
	}
	v, last, _, _ := s.Store.MailCursor()
	if v != 1 || last != 7 {
		t.Fatalf("cursor = (%d, %d), want (1, 7)", v, last)
	}
}

func TestNeverChangesMailbox(t *testing.T) {
	ts := startServer(t)
	s := newService(t, ts, testPass)
	runCycle(t, s)
	ts.add(t, goodMail("one"))
	ts.add(t, goodMail("two"))
	runCycle(t, s)
	runCycle(t, s)

	c := ts.client(t)
	mb, err := c.Select("INBOX", true)
	if err != nil {
		t.Fatal(err)
	}
	if mb.Messages != 3 {
		t.Fatalf("mailbox has %d messages, want 3", mb.Messages)
	}
	seq := new(imap.SeqSet)
	seq.AddRange(7, 8)
	ch := make(chan *imap.Message, 4)
	if err := c.UidFetch(seq, []imap.FetchItem{imap.FetchUid, imap.FetchFlags}, ch); err != nil {
		t.Fatal(err)
	}
	n := 0
	for m := range ch {
		n++
		if slices.Contains(m.Flags, imap.SeenFlag) {
			t.Errorf("uid %d has flags %v", m.Uid, m.Flags)
		}
	}
	if n != 2 {
		t.Fatalf("fetched %d, want 2", n)
	}
}

func TestBadPasswordSetsErrorStatus(t *testing.T) {
	ts := startServer(t)
	const pw = "hunter2-very-secret"
	s := newService(t, ts, pw)
	s.cycle(context.Background())
	st := s.Status()
	if st.State != "error" || st.Error == "" {
		t.Fatalf("status = %+v, want error", st)
	}
	if strings.Contains(st.Error, pw) {
		t.Fatalf("status error leaks the password: %q", st.Error)
	}
	if err := s.Check(context.Background(), ts.config(pw)); err == nil || strings.Contains(err.Error(), pw) {
		t.Fatalf("Check = %v", err)
	}
	if err := s.Check(context.Background(), ts.config(testPass)); err != nil {
		t.Fatalf("Check with right password: %v", err)
	}
}

func TestScrubRemovesPassword(t *testing.T) {
	err := scrub(errors.New("LOGIN user s3cr3t rejected"), "s3cr3t")
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("scrub left the password: %q", err)
	}
}

func TestNoConfigIsOff(t *testing.T) {
	s := newService(t, nil, "")
	s.cycle(context.Background())
	if st := s.Status(); st.State != "off" {
		t.Fatalf("status = %+v, want off", st)
	}
}

func TestRunStopsWithContext(t *testing.T) {
	ts := startServer(t)
	s := newService(t, ts, testPass)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	ts.add(t, goodMail("x")) // may or may not land before the first cycle
	s.Poke()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestConfigValidate(t *testing.T) {
	ok := Config{Host: "imap.gmail.com", Username: "u", Password: "p"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	bad := map[string]Config{
		"no host":     {Username: "u", Password: "p"},
		"scheme":      {Host: "imaps://imap.gmail.com", Username: "u", Password: "p"},
		"slash":       {Host: "imap.gmail.com/x", Username: "u", Password: "p"},
		"port":        {Host: "h", Port: 70000, Username: "u", Password: "p"},
		"neg port":    {Host: "h", Port: -1, Username: "u", Password: "p"},
		"no username": {Host: "h", Password: "p"},
		"no password": {Host: "h", Username: "u"},
	}
	for name, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestClearedCursorStartsAtUIDNextAgain(t *testing.T) {
	ts := startServer(t)
	s := newService(t, ts, testPass)
	runCycle(t, s)
	ts.add(t, goodMail("one"))
	runCycle(t, s)
	if err := s.Store.ClearMailCursor(); err != nil {
		t.Fatal(err)
	}
	ts.add(t, goodMail("two")) // arrives while the cursor is gone
	runCycle(t, s)             // starts at UIDNEXT-1: imports nothing
	if got := all(t, s); len(got) != 1 || got[0].Subject != "one" {
		t.Fatalf("stored %+v, want only 'one'", got)
	}
	if _, last, ok, _ := s.Store.MailCursor(); !ok || last != 8 {
		t.Fatalf("cursor last=%d ok=%v, want 8", last, ok)
	}
}
