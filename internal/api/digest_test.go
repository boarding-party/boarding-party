package api

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
	"github.com/boarding-party/boardingparty/pkg/digest"
)

func TestDigestEndpoint(t *testing.T) {
	srv, svc := setup(t, 1024)
	now := time.Now().UTC()
	if err := svc.Store.CreateRun(store.Run{ID: "r1", Token: "tok-r1", Persona: "ann", Role: "buyer", Story: "checkout",
		State: store.RunRunning, Started: now.Add(-time.Hour), Chain: "r1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AddBug(store.Bug{ID: "b1", RunID: "r1", Summary: "total wrong", Created: now.Add(-30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	get := func(q string) (digest.Digest, int) {
		r := do(t, "GET", srv.URL+"/api/digest"+q, "")
		var d digest.Digest
		if r.StatusCode == 200 {
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
				t.Fatal(err)
			}
		}
		return d, r.StatusCode
	}
	d, code := get("")
	if code != 200 || len(d.NewBugs.Items) != 1 || d.NewBugs.Items[0].Title != "total wrong" {
		t.Fatalf("digest %d %+v", code, d)
	}
	if got := time.Since(d.Since); got < 24*time.Hour-time.Minute || got > 24*time.Hour+time.Minute {
		t.Fatalf("default since %s, want a day back", d.Since)
	}
	d, code = get("?since=" + url.QueryEscape(now.Add(-30*24*time.Hour).Format(time.RFC3339)))
	if got := time.Since(d.Since); code != 200 || got < 7*24*time.Hour-time.Minute || got > 7*24*time.Hour+time.Minute {
		t.Fatalf("since 30 days back: %d %s, want clamped to 7 days", code, d.Since)
	}
	d, code = get("?since=" + url.QueryEscape(now.Add(-10*time.Minute).Format(time.RFC3339Nano)))
	if code != 200 || len(d.NewBugs.Items) != 0 {
		t.Fatalf("since 10 minutes back: %d %+v, want no bugs", code, d.NewBugs)
	}
	for _, bad := range []string{"x", now.Add(24 * time.Hour).Format(time.RFC3339)} {
		if _, code := get("?since=" + url.QueryEscape(bad)); code != 400 {
			t.Fatalf("since=%s: %d, want 400", bad, code)
		}
	}
}
