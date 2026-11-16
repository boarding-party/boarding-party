package tracesum

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/trace.zip")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func read(t *testing.T, b []byte) (Summary, error) {
	t.Helper()
	return Read(bytes.NewReader(b), int64(len(b)))
}

func TestReadFixture(t *testing.T) {
	s, err := read(t, fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Actions) < 5 {
		t.Fatalf("actions = %d, want at least 5: %+v", len(s.Actions), s.Actions)
	}
	// The fixture was recorded on 2026-11-16 (make-trace.mjs).
	day := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	for i, a := range s.Actions {
		if a.Start.Before(day) || a.Start.After(day.Add(48*time.Hour)) || a.End.Before(a.Start) {
			t.Errorf("action %d %q at %v–%v: not on the recording's wall clock", i, a.Title, a.Start, a.End)
		}
		if i > 0 && a.Start.Before(s.Actions[i-1].Start) {
			t.Errorf("action %d out of order", i)
		}
		if a.Snapshot == "" || a.PageID == "" {
			t.Errorf("action %d %q has no snapshot or page", i, a.Title)
		}
	}
	titles := map[string]Action{}
	for _, a := range s.Actions {
		titles[a.Title] = a
	}
	pay, ok := titles[`click button "Pay now"`]
	if !ok {
		t.Fatalf("no click on the iframe's button among %v", keys(titles))
	}
	// The page URL is the main frame's, even for a click inside the iframe.
	if !strings.HasSuffix(pay.PageURL, "/") || strings.HasSuffix(pay.PageURL, "/frame") {
		t.Errorf("iframe click page URL = %q, want the main page", pay.PageURL)
	}
	if a := titles[`click link "Next"`]; !strings.HasSuffix(a.PageURL, "/next") {
		t.Errorf("after the link the page URL = %q, want …/next", a.PageURL)
	}

	statuses := map[string]int{}
	for _, r := range s.Network {
		if strings.ContainsAny(r.URL, "?#") {
			t.Errorf("URL kept its query: %s", r.URL)
		}
		statuses[r.URL[strings.LastIndex(r.URL, "/"):]] = r.Status
		if r.Start.Before(day) || r.Method != "GET" {
			t.Errorf("resource %+v", r)
		}
	}
	if statuses["/missing"] != 404 || statuses["/ok"] != 200 {
		t.Fatalf("statuses = %v", statuses)
	}
	for _, r := range s.Network {
		if strings.HasSuffix(r.URL, "/missing") && r.Failed {
			t.Error("a 404 is an answer, not a failed request")
		}
	}
}

func keys(m map[string]Action) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// rezip copies the fixture with every entry under prefix, as zipping the
// traces folder itself does.
func rezip(t *testing.T, b []byte, prefix string, keep func(string) bool) []byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range z.File {
		if !keep(f.Name) {
			continue
		}
		rc, _ := f.Open()
		dst, _ := w.Create(prefix + f.Name)
		io.Copy(dst, rc)
		rc.Close()
	}
	w.Close()
	return out.Bytes()
}

func TestReadFolderPrefix(t *testing.T) {
	want, err := read(t, fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	got, err := read(t, rezip(t, fixture(t), "traces/", func(string) bool { return true }))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Actions) != len(want.Actions) || len(got.Network) != len(want.Network) {
		t.Fatalf("prefixed zip: %d actions %d requests, want %d and %d", len(got.Actions), len(got.Network), len(want.Actions), len(want.Network))
	}
}

func TestReadBadZips(t *testing.T) {
	b := fixture(t)
	if _, err := read(t, b[:len(b)/2]); err == nil {
		t.Error("truncated zip: no error")
	}
	noTrace := rezip(t, b, "", func(n string) bool { return !strings.HasSuffix(n, ".trace") })
	if _, err := read(t, noTrace); !errors.Is(err, ErrNotTrace) {
		t.Errorf("zip without a .trace = %v, want ErrNotTrace", err)
	}
}

func TestHeadersRedacted(t *testing.T) {
	h := headers([]header{{"Cookie", "sid=1"}, {"Authorization", "Bearer x"}, {"Referer", "https://s.test/a?code=9"}, {"Accept", "*/*"}})
	if h["Cookie"] != "[redacted]" || h["Authorization"] != "[redacted]" || h["Referer"] != "https://s.test/a" || h["Accept"] != "*/*" {
		t.Fatalf("headers = %v", h)
	}
}
