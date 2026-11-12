// Package devdirector runs a director admin API in this process with a fake
// launcher, so the board UI can be checked without a cluster. Runs never touch
// Kubernetes: a started run goes to running, then finishes after a delay with
// a sample recording and one bug.
package devdirector

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/boarding-party/boardingparty/internal/api"
	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/board"
	"github.com/boarding-party/boardingparty/internal/files"
	"github.com/boarding-party/boardingparty/internal/inbox"
	"github.com/boarding-party/boardingparty/internal/live"
	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/rewind"
	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

//go:embed testdata/sample.webm
var sampleWebm []byte

//go:embed testdata/todomvc.yaml
var sampleProject []byte

// SampleProject is the project a dev director starts with (a copy of
// examples/todomvc.yaml; a test keeps them equal).
func SampleProject() []byte { return append([]byte(nil), sampleProject...) }

// RunDuration is how long a fake run takes before it finishes.
const RunDuration = 20 * time.Second

// SampleArtifact is the name of the recording a fake run uploads.
const SampleArtifact = "run.webm"

// Launcher is a runs.Launcher with no cluster behind it. A started run
// finishes "done" after Duration with a sample recording and one bug.
type Launcher struct {
	Duration time.Duration
	Svc      *runs.Service  // set after the Service is built
	Hub      *live.Hub      // receives a fake frame every 500 ms while a run is active
	Mail     *inbox.Service // receives fake mail when a run starts; nil = none

	mailUID atomic.Uint32

	mu       sync.Mutex
	sessions map[string]string // session -> run ID
	started  map[string]time.Time
}

// Fake start-up timeline so the board's pre-frame status can be seen:
// waiting for a node, then starting, then running, then the first frame.
const (
	fakeNodeAfter    = 4 * time.Second
	fakeRunningAfter = 8 * time.Second
	fakeFrameAfter   = 11 * time.Second
)

func (l *Launcher) Stage(_ context.Context, session string) (string, string, error) {
	l.mu.Lock()
	t0, ok := l.started[session]
	l.mu.Unlock()
	if !ok {
		return "starting", "", nil
	}
	switch d := time.Since(t0); {
	case d < fakeNodeAfter:
		return "waiting-for-node", "", nil
	case d < fakeRunningAfter:
		return "starting", "", nil
	}
	return "running", "", nil
}

func (l *Launcher) SessionName(runID string) string { return "fake-" + runID }

func (l *Launcher) Start(ctx context.Context, r store.Run, p project.Project) (string, error) {
	name := l.SessionName(r.ID)
	l.mu.Lock()
	if l.sessions == nil {
		l.sessions = map[string]string{}
	}
	l.sessions[name] = r.ID
	if l.started == nil {
		l.started = map[string]time.Time{}
	}
	l.started[name] = time.Now()
	l.mu.Unlock()
	go l.finishLater(r)
	l.fakeMail(r, p)
	if l.Hub != nil {
		go l.publishFrames(r)
	}
	return name, nil
}

// fakeMail stores what a real mailbox would hold after a sign-up: a welcome
// message routed to the run's persona and one that the router filtered.
func (l *Launcher) fakeMail(r store.Run, p project.Project) {
	if l.Mail == nil || len(p.Sites) == 0 {
		return
	}
	site := p.Sites[0]
	host := site
	if u, err := url.Parse(site); err == nil && u.Host != "" {
		host = u.Hostname()
	}
	now := time.Now().UTC()
	add := func(from, subject, text, filtered string, links []string) {
		uid := l.mailUID.Add(1)
		_, _ = l.Svc.Store.AddMessage(store.Message{
			ID: fmt.Sprintf("fake-%s-%d", r.ID, uid), UIDValidity: 1, UID: uid, Persona: r.Persona,
			From: from, To: []string{r.Persona + "@" + host}, Subject: subject, Text: text, Links: links,
			Received: now, Stored: now, Filtered: filtered,
		})
	}
	add("hello@"+host, "Welcome to "+p.Name, "Thanks for signing up. Start here: "+site, "", []string{site})
	add("spam@example.test", "You won a prize", "Claim it now.", "sender not allowed", nil)
	l.Mail.Notify()
}

// publishFrames feeds the hub a generated frame twice a second until the run ends.
func (l *Launcher) publishFrames(r store.Run) {
	begin := time.Now()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		if _, err := l.Svc.Active(r.Token); err != nil {
			return
		}
		if time.Since(begin) < fakeFrameAfter {
			continue
		}
		if b := fakeFrame(time.Since(begin)); b != nil {
			l.Hub.Publish(r.ID, b)
			if l.Svc.Rewind != nil {
				_ = l.Svc.Rewind.Append(r.ID, time.Now(), b)
			}
		}
	}
}

// fakeFrame draws a phone-shaped JPEG whose colour drifts with the elapsed
// time and whose block row counts the seconds, so a live view visibly moves.
func fakeFrame(elapsed time.Duration) []byte {
	const w, h = 180, 320
	secs := int(elapsed / time.Second)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{R: uint8(40 + secs*7%120), G: uint8(90 + secs*13%100), B: uint8(160 - secs*5%100), A: 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	white := &image.Uniform{color.RGBA{255, 255, 255, 255}}
	for i := 0; i <= secs%30; i++ { // one block per elapsed second, wrapping
		x, y := 8+(i%10)*17, 20+(i/10)*20
		draw.Draw(img, image.Rect(x, y, x+12, y+14), white, image.Point{}, draw.Src)
	}
	// a block sweeping down the screen at half-second steps
	y := int(elapsed/(500*time.Millisecond)) * 10 % (h - 20)
	draw.Draw(img, image.Rect(0, 100+y%200, w, 110+y%200), &image.Uniform{color.RGBA{20, 20, 20, 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60}) != nil {
		return nil
	}
	return buf.Bytes()
}

// fakeScreenshot draws a small PNG standing in for a bug screenshot.
func fakeScreenshot() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 640, 400))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{238, 241, 244, 255}}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(40, 40, 600, 80), &image.Uniform{color.RGBA{60, 110, 190, 255}}, image.Point{}, draw.Src)
	for i := 0; i < 3; i++ {
		draw.Draw(img, image.Rect(40, 110+i*60, 600, 150+i*60), &image.Uniform{color.RGBA{255, 255, 255, 255}}, image.Point{}, draw.Src)
	}
	var buf bytes.Buffer
	if png.Encode(&buf, img) != nil {
		return nil
	}
	return buf.Bytes()
}

func (l *Launcher) finishLater(r store.Run) {
	time.Sleep(l.Duration)
	if _, err := l.Svc.Active(r.Token); err != nil {
		return // already ended
	}
	if r.Kind == store.RunReplay {
		// A replay plays the journey back: a recording and a result, no bugs.
		_, _ = l.Svc.Artifacts.Put(r.ID, "replay-1.webm", bytes.NewReader(sampleWebm))
		_ = l.Svc.FinishReplay(r.Token, runs.ReplayResult{Status: "passed", Seed: r.Seed, DurationMs: int(l.Duration / time.Millisecond),
			Inputs: map[string]any{"int1": 3, "plusEmail1": fmt.Sprintf("sam+%d@example.test", r.Seed)}}, time.Now().UTC())
		return
	}
	if _, err := l.Svc.Artifacts.Put(r.ID, SampleArtifact, bytes.NewReader(sampleWebm)); err != nil {
		_ = l.Svc.Finish(context.Background(), r.Token, store.RunFailed, "fake run: "+err.Error())
		return
	}
	if shot := fakeScreenshot(); shot != nil {
		_, _ = l.Svc.Artifacts.Put(r.ID, "bug-1.png", bytes.NewReader(shot))
	}
	_ = l.Svc.Store.AddBug(store.Bug{
		ID: "bug-" + r.ID, RunID: r.ID,
		Summary:  "Counter still says 3 items left after completing one",
		Step:     "00:02 ticked the first todo; the counter did not change",
		Evidence: "bug-1.png",
		Severity: "medium",
		Note:     "I expected 2 items left, so I stopped trusting the count.",
		Created:  time.Now().UTC(),
	})
	_ = l.Svc.Finish(context.Background(), r.Token, store.RunDone, "Added three todos and completed one. The counter did not update (fake run).")
}

func (l *Launcher) Stop(_ context.Context, session string) error {
	l.mu.Lock()
	delete(l.sessions, session)
	l.mu.Unlock()
	return nil
}

func (l *Launcher) Phase(_ context.Context, session string) (string, bool, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.sessions[session]; !ok {
		return "", false, "", nil
	}
	return "Running", true, "", nil
}

func (l *Launcher) Sessions(_ context.Context) (map[string]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]string, len(l.sessions))
	for k, v := range l.sessions {
		out[k] = v
	}
	return out, nil
}

func (l *Launcher) LivePods(context.Context) (int, error) { return 0, nil }

// Director is a running in-process director admin API.
type Director struct {
	Base  string // http://127.0.0.1:<port>
	Token string // bearer secret the admin API requires

	srv *http.Server
	st  *store.Store
	dir string
}

// Start serves the admin API on a loopback port, with a temp data directory,
// the project loaded from projectYAML, and a fake launcher taking duration per run.
// Call Close to stop it and delete the data.
func Start(ctx context.Context, projectYAML []byte, duration time.Duration) (*Director, error) {
	dir, err := os.MkdirTemp("", "bp-dev-director-")
	if err != nil {
		return nil, err
	}
	d, err := start(ctx, dir, projectYAML, duration)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return d, nil
}

func start(ctx context.Context, dir string, projectYAML []byte, duration time.Duration) (*Director, error) {
	st, err := store.Open(filepath.Join(dir, "boardingparty.db"))
	if err != nil {
		return nil, err
	}
	p, err := project.Load(projectYAML)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("sample project: %w", err)
	}
	if err := st.PutProject(p); err != nil {
		st.Close()
		return nil, err
	}
	if err := seedJourney(st, p, time.Now().UTC()); err != nil {
		st.Close()
		return nil, err
	}
	if err := seedChain(st, p, time.Now().UTC()); err != nil {
		st.Close()
		return nil, fmt.Errorf("seed runs: %w", err)
	}
	hub := live.NewHub()
	mail := &inbox.Service{
		Store:   st,
		Secrets: &memSecrets{},
		CheckFunc: func(_ context.Context, c inbox.Config) error {
			if c.Host != "imap.example.test" {
				return fmt.Errorf("dev director accepts only imap.example.test")
			}
			return nil
		},
	}
	l := &Launcher{Duration: duration, Hub: hub, Mail: mail}
	svc := &runs.Service{
		Store:     st,
		Artifacts: artifacts.NewFS(filepath.Join(dir, "artifacts"), api.MaxArtifactBytes),
		Files:     files.New(filepath.Join(dir, "files")),
		Launcher:  l,
		LastFrame: hub.Latest,
		Egress:    &devEgress{},
		Rewind:    rewind.New(filepath.Join(dir, "rewind"), 48<<20),
	}
	l.Svc = svc
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		st.Close()
		return nil, err
	}
	token := hex.EncodeToString(b)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		st.Close()
		return nil, err
	}
	// Serve the UI beside the API, as the real director does, so the CLI's
	// board finds a director new enough to serve it.
	ui, err := board.UI()
	if err != nil {
		return nil, err
	}
	routes := api.AdminRoutes(svc, 0, hub, mail)
	static := board.Static(ui)
	top := http.NewServeMux()
	top.Handle("/api", routes)
	top.Handle("/api/", routes)
	top.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		board.UIHeaders(w.Header(), "'none'")
		static.ServeHTTP(w, r)
	}))
	srv := &http.Server{Handler: api.Guard(token, top), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "dev director:", err)
		}
	}()
	go svc.Watch(ctx, time.Second, 10*time.Minute)
	return &Director{Base: "http://" + ln.Addr().String(), Token: token, srv: srv, st: st, dir: dir}, nil
}

// devJourney is the code of the journey a dev director starts with.
const devJourney = `export default async function journey({ page, data, bp, expect }) {
  const n = data.int(1, 5);
  await bp.step('Open the app', async () => { await page.goto('https://demo.playwright.dev/todomvc/'); });
  await bp.step('Add the todos', async () => {
    for (let i = 1; i <= n; i++) await page.getByPlaceholder('What needs to be done?').fill('Todo ' + i).then(() => page.keyboard.press('Enter'));
  });
  await bp.step('Check the counter', async () => { await expect(page.getByTestId('todo-count')).toContainText(String(n)); });
}
`

// seedJourney gives the first persona's first story a journey with two
// revisions, so Start run can offer a replay.
func seedJourney(st *store.Store, p project.Project, now time.Time) error {
	if len(p.Roles) == 0 || len(p.Roles[0].Personas) == 0 || len(p.Roles[0].Stories) == 0 {
		return nil
	}
	persona, story := p.Roles[0].Personas[0].Name, p.Roles[0].Stories[0].Name
	for i, reason := range []string{"First recording of the story.", "The counter check now waits for the list to settle."} {
		at := now.Add(time.Duration(i-2) * time.Hour)
		if _, _, err := st.AddRevision(persona, story, store.Revision{Kind: store.RevisionFull, Code: devJourney, Reason: reason, Created: at},
			store.Decision{Kind: "recorded", Reason: reason, Actor: store.ActorModel, Created: at}); err != nil {
			return err
		}
	}
	return nil
}

// seedChain stores finished runs so the board has a chain, a waiting run and
// a queued run to show, using the first role's first persona and story: a
// done run, a run that continued it, a run waiting for an email and a run
// queued after it. The dev director has no scheduler, so they stay put.
func seedChain(st *store.Store, p project.Project, now time.Time) error {
	if len(p.Roles) == 0 || len(p.Roles[0].Personas) == 0 || len(p.Roles[0].Stories) == 0 {
		return nil
	}
	role := p.Roles[0]
	persona, story := role.Personas[0].Name, role.Stories[0].Name
	var err error
	id := func() string {
		b := make([]byte, 16)
		if _, e := rand.Read(b); e != nil && err == nil {
			err = e
		}
		return hex.EncodeToString(b)
	}
	run := func(state store.RunState, outcome string, started, finished time.Time) store.Run {
		r := store.Run{ID: id(), Token: id() + id(), Persona: persona, Role: role.Name, Story: story,
			State: state, Outcome: outcome, Started: started, Finished: finished}
		r.Chain = r.ID
		return r
	}

	first := run(store.RunDone, "Added three todos and left before finishing the list (sample run).",
		now.Add(-3*time.Hour), now.Add(-3*time.Hour+4*time.Minute))
	next := run(store.RunDone, "Came back, completed one todo and checked the counter (sample run).",
		now.Add(-2*time.Hour), now.Add(-2*time.Hour+3*time.Minute))
	next.Chain, next.Follows, next.Via = first.Chain, first.ID, "continue"
	ctxJSON, e := json.Marshal(store.RunContext{Kind: "continue", PreviousRun: first.ID, Previous: first.Outcome})
	if e != nil {
		return e
	}
	next.Context = ctxJSON
	waiting := run(store.RunRunning, "", now.Add(-40*time.Minute), time.Time{})
	if err != nil {
		return err
	}
	for _, r := range []store.Run{first, next, waiting} {
		if err := st.CreateRun(r); err != nil {
			return err
		}
	}
	ok, e := st.FinishRunWaiting(waiting.ID, "Booked a slot and is waiting for the confirmation email (sample run).",
		now.Add(-30*time.Minute), store.Wait{Kind: "email", Subject: "Booking confirmed", Deadline: now.Add(24 * time.Hour)})
	if e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("sample waiting run %s was not active", waiting.ID)
	}
	q := store.Queued{ID: id(), Persona: persona, Role: role.Name, Story: story, Chain: waiting.Chain,
		Follows: waiting.ID, Via: "handoff", Reason: fmt.Sprintf("Handed off by %s (%s)", persona, story),
		Created: now.Add(-30 * time.Minute)}
	if err != nil {
		return err
	}
	return st.Enqueue(q)
}

// memSecrets keeps the mailbox login in memory for the dev director.
// devEgress fakes the network fence: every run is refused one font host the
// project hasn't allowed, so the board's blocked-hosts card has something to show.
type devEgress struct {
	mu      sync.Mutex
	allowed []string
}

func (e *devEgress) Sync(_ context.Context, hosts []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.allowed = hosts
	return nil
}

func (e *devEgress) Denied(context.Context, time.Time, time.Time) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if slices.Contains(e.allowed, devBlockedHost) {
		return nil, nil
	}
	return []string{devBlockedHost}, nil
}

const devBlockedHost = "fonts.gstatic.com"

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

// Close stops the server and removes the temp data.
func (d *Director) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if d.srv.Shutdown(ctx) != nil {
		d.srv.Close()
	}
	d.st.Close()
	os.RemoveAll(d.dir)
}

// Forwarder is a board.Forwarder that always points at the in-process server.
type Forwarder struct{ Base string }

func (f Forwarder) Open(context.Context) (string, error) { return f.Base, nil }
func (f Forwarder) Close()                               {}
