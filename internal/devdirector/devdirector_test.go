package devdirector

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

func sample(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../examples/todomvc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, SampleProject()) {
		t.Fatal("testdata/todomvc.yaml differs from examples/todomvc.yaml; copy it over")
	}
	return b
}

func do(t *testing.T, d *Director, method, path, ctype, token, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, d.Base+path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestRunLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := Start(ctx, sample(t), 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if resp, _ := do(t, d, "GET", "/api/project", "", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", resp.StatusCode)
	}
	if resp, _ := do(t, d, "GET", "/api/project", "", d.Token, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("project: got %d", resp.StatusCode)
	}
	resp, b := do(t, d, "POST", "/api/runs", "application/json", d.Token, `{"persona":"Ada","story":"todos"}`)
	if resp.StatusCode >= 300 {
		t.Fatalf("start: %d %s", resp.StatusCode, b)
	}
	var run store.Run
	if err := json.Unmarshal(b, &run); err != nil {
		t.Fatal(err)
	}
	if run.State != store.RunRunning {
		t.Fatalf("state after start = %q, want running", run.State)
	}
	// A second run while one is active is queued; drop it so it does not
	// start after this one.
	resp, b = do(t, d, "POST", "/api/runs", "application/json", d.Token, `{"persona":"Ada","story":"todos"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("second start: got %d, want 202", resp.StatusCode)
	}
	var queued struct{ Queued store.Queued }
	if err := json.Unmarshal(b, &queued); err != nil {
		t.Fatal(err)
	}
	if err := d.st.DeleteQueued(queued.Queued.ID); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := d.st.RunByID(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == store.RunDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run still %q after 5s", got.State)
		}
		time.Sleep(50 * time.Millisecond)
	}
	bugs, err := d.st.Bugs(run.ID)
	if err != nil || len(bugs) != 1 || !strings.Contains(bugs[0].Step, "00:02") {
		t.Fatalf("bugs = %+v, %v", bugs, err)
	}
	resp, vid := do(t, d, "GET", "/api/runs/"+run.ID+"/artifacts/"+SampleArtifact, "", d.Token, "")
	if resp.StatusCode != http.StatusOK || len(vid) != len(sampleWebm) {
		t.Fatalf("artifact: %d, %d bytes (want %d)", resp.StatusCode, len(vid), len(sampleWebm))
	}
	// The session is gone, so the next run can start.
	if resp, _ := do(t, d, "POST", "/api/runs", "application/json", d.Token, `{"persona":"Ada","story":"todos"}`); resp.StatusCode >= 300 {
		t.Fatalf("start after finish: got %d", resp.StatusCode)
	}
}

func TestRunStoresFakeMail(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := Start(ctx, sample(t), 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	resp, b := do(t, d, "POST", "/api/runs", "application/json", d.Token, `{"persona":"Ada","story":"todos"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start: %d %s", resp.StatusCode, b)
	}
	_, b = do(t, d, "GET", "/api/messages?persona=Ada", "", d.Token, "")
	if !strings.Contains(string(b), "Welcome to") || strings.Contains(string(b), "spam@") {
		t.Fatalf("delivered: %s", b)
	}
	_, b = do(t, d, "GET", "/api/messages?filtered=1", "", d.Token, "")
	if !strings.Contains(string(b), "sender not allowed") {
		t.Fatalf("filtered: %s", b)
	}
	if resp, b := do(t, d, "PUT", "/api/mailbox", "application/json", d.Token,
		`{"host":"imap.example.test","port":993,"username":"u","password":"p"}`); resp.StatusCode != 200 {
		t.Fatalf("mailbox: %d %s", resp.StatusCode, b)
	}
}

func TestDevDirectorSeedsChain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := Start(ctx, sample(t), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	_, b := do(t, d, "GET", "/api/runs", "", d.Token, "")
	var list []struct {
		store.Run
		Wait *struct {
			WaitingFor string `json:"waitingFor"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("runs: %v: %s", err, b)
	}
	var waiting []string
	continued := ""
	for _, r := range list {
		if r.State == store.RunWaiting {
			if r.Wait == nil || !strings.Contains(r.Wait.WaitingFor, "Booking confirmed") {
				t.Fatalf("waiting run %s wait = %+v", r.ID, r.Wait)
			}
			waiting = append(waiting, r.ID)
		}
		if r.Via == "continue" {
			continued = r.ID
		}
	}
	if len(waiting) != 1 {
		t.Fatalf("waiting runs = %v, want one (%s)", waiting, b)
	}

	_, b = do(t, d, "GET", "/api/queue", "", d.Token, "")
	var queue []struct {
		Follows string `json:"follows"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(b, &queue); err != nil || len(queue) != 1 {
		t.Fatalf("queue = %s (%v), want one item", b, err)
	}
	if queue[0].Follows != waiting[0] || queue[0].Reason != "Handed off by Ada (todos)" {
		t.Fatalf("queued = %+v", queue[0])
	}

	if continued == "" {
		t.Fatal("no continued run")
	}
	_, b = do(t, d, "GET", "/api/runs/"+continued, "", d.Token, "")
	var detail struct {
		Chain []struct {
			ID string `json:"id"`
		} `json:"chain"`
	}
	if err := json.Unmarshal(b, &detail); err != nil || len(detail.Chain) != 2 {
		t.Fatalf("continued run detail chain = %s (%v), want two", b, err)
	}
}

// The dev director has a journey to replay, and a replay finishes with a
// result like a real one.
func TestDevDirectorReplaysAJourney(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := Start(ctx, sample(t), 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	_, b := do(t, d, "GET", "/api/journeys", "", d.Token, "")
	var js []struct {
		ID      string `json:"id"`
		LiveRev int    `json:"liveRev"`
	}
	if err := json.Unmarshal(b, &js); err != nil || len(js) != 1 || js[0].LiveRev == 0 {
		t.Fatalf("journeys %s %v", b, err)
	}
	resp, b := do(t, d, "POST", "/api/journeys/"+js[0].ID+"/replay", "application/json", d.Token, `{}`)
	if resp.StatusCode != 201 {
		t.Fatalf("replay %d %s", resp.StatusCode, b)
	}
	var run store.Run
	_ = json.Unmarshal(b, &run)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, b = do(t, d, "GET", "/api/runs/"+run.ID, "", d.Token, "")
		var got struct {
			Run       store.Run `json:"run"`
			Artifacts []string  `json:"artifacts"`
		}
		_ = json.Unmarshal(b, &got)
		if got.Run.State.Final() {
			if got.Run.Kind != store.RunReplay || !slices.Contains(got.Artifacts, runs.ReplayResultName) {
				t.Fatalf("replay ended as %+v with %v", got.Run, got.Artifacts)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the replay never finished")
}
