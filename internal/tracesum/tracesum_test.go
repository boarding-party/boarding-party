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
		if a.Snapshot != a.Phase+"@"+a.CallID || (a.Phase != "after" && a.Phase != "before") {
			t.Errorf("action %d snapshot %q, call %q, phase %q: the viewer could not find it", i, a.Snapshot, a.CallID, a.Phase)
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
	h := headers([]header{{"Cookie", "sid=1"}, {"Authorization", "Bearer x"}, {"Referer", "https://s.test/a?code=9"}, {"Accept", "*/*"},
		{"X-Api-Key", "k"}, {"X-CSRF-Token", "c"}, {"X-Session-Id", "s"}, {"X-Client-Secret", "z"}, {"Link", "<https://s.test/n?page=2>; rel=next"}})
	for _, n := range []string{"Cookie", "Authorization", "X-Api-Key", "X-CSRF-Token", "X-Session-Id", "X-Client-Secret"} {
		if h[n] != "[redacted]" {
			t.Errorf("%s = %q, want redacted", n, h[n])
		}
	}
	if h["Referer"] != "https://s.test/a" || h["Accept"] != "*/*" || strings.Contains(h["Link"], "page=2") {
		t.Fatalf("headers = %v", h)
	}
}

func TestErrorTextLosesQueries(t *testing.T) {
	got := cleanText(`page.goto: net::ERR_ABORTED at https://bob:pw@s.test/reset?code=123#x, then "http://s.test/b?t=1"`)
	if strings.Contains(got, "code=123") || strings.Contains(got, "t=1") || strings.Contains(got, "bob:pw") || !strings.Contains(got, "https://s.test/reset") {
		t.Fatalf("cleanText = %q", got)
	}
	if got := cleanText(`see https://s.test/a,b?token=1 now`); strings.Contains(got, "token") {
		t.Fatalf("comma in path kept the query: %q", got)
	}
}

// A snapshot line over the line limit is skipped; the actions after it stay.
func TestReadSkipsHugeLines(t *testing.T) {
	b := fixture(t)
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range z.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		if f.Name == "trace.trace" {
			lines := bytes.Split(data, []byte("\n"))
			mid := len(lines) / 2
			huge := []byte(`{"type":"frame-snapshot","snapshot":{"html":"` + strings.Repeat("x", 2<<20) + `"}}`)
			lines = append(lines[:mid], append([][]byte{huge}, lines[mid:]...)...)
			data = bytes.Join(lines, []byte("\n"))
		}
		dst, _ := w.Create(f.Name)
		dst.Write(data)
	}
	w.Close()
	want, _ := read(t, b)
	got, err := read(t, out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Actions) != len(want.Actions) {
		t.Fatalf("with a 2 MiB line: %d actions, want %d", len(got.Actions), len(want.Actions))
	}
	for i, a := range got.Actions {
		if a.End.Equal(a.Start) && want.Actions[i].End.After(want.Actions[i].Start) {
			t.Errorf("action %d %q lost its end", i, a.Title)
		}
	}
}

// A zip of many trace entries cannot multiply the per-entry read limit.
func TestReadCapsWholeTrace(t *testing.T) {
	b := fixture(t)
	z, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	var data []byte
	for _, f := range z.File {
		if f.Name == "trace.trace" {
			rc, _ := f.Open()
			data, _ = io.ReadAll(rc)
			rc.Close()
		}
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for i := range maxEntries + 1 {
		dst, _ := w.Create(strings.Repeat("a", i+1) + ".trace")
		dst.Write(data)
	}
	w.Close()
	if _, err := read(t, out.Bytes()); !errors.Is(err, ErrTooBig) {
		t.Fatalf("%d trace entries = %v, want ErrTooBig", maxEntries+1, err)
	}
}

// The trace format the worker's Playwright writes (version 10) names no page
// or snapshot on a call: its frame snapshots carry the call id, the phase
// and the page. Only calls with a snapshot touch the page.
func TestReadVersion10(t *testing.T) {
	lines := []string{
		`{"type":"context-options","version":10,"wallTime":1794786831088,"monotonicTime":5468.559}`,
		`{"type":"before","callId":"c@13","startTime":10956.9,"class":"BrowserContext","method":"newPage","params":{}}`,
		`{"type":"after","callId":"c@13","endTime":11205.5}`,
		`{"type":"before","callId":"c@42","startTime":11391.57,"class":"Frame","method":"goto","params":{"url":"https://example.com/?token=abc"}}`,
		`{"type":"frame-snapshot","snapshot":{"callId":"c@42","phase":"before","pageId":"page@1","frameUrl":"about:blank","isMainFrame":true}}`,
		`{"type":"after","callId":"c@42","endTime":11891.57}`,
		`{"type":"frame-snapshot","snapshot":{"callId":"c@42","phase":"after","pageId":"page@1","frameUrl":"https://ads.example.net/frame","isMainFrame":false}}`,
		`{"type":"frame-snapshot","snapshot":{"callId":"c@42","phase":"after","pageId":"page@1","frameUrl":"https://example.com/?token=abc","isMainFrame":true}}`,
		`{"type":"before","callId":"c@50","startTime":12000,"class":"Frame","method":"title","params":{}}`,
		`{"type":"after","callId":"c@50","endTime":12001}`,
		`{"type":"before","callId":"c@84","startTime":16501.7,"class":"Frame","method":"click","params":{"selector":"aria-ref=e136 >> internal:describe=\"Open Invoice Generator link\""}}`,
		`{"type":"frame-snapshot","snapshot":{"callId":"c@84","phase":"before","pageId":"page@1","frameUrl":"https://example.com/","isMainFrame":true}}`,
		`{"type":"after","callId":"c@84","endTime":16600,"error":{"error":{"message":"Timeout 5000ms exceeded"}}}`,
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	dst, _ := w.Create("traces/trace-1.trace")
	dst.Write([]byte(strings.Join(lines, "\n")))
	w.Close()
	s, err := read(t, out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Actions) != 2 {
		t.Fatalf("got %d actions, want the goto and the click: %+v", len(s.Actions), s.Actions)
	}
	g, c := s.Actions[0], s.Actions[1]
	if g.CallID != "c@42" || g.Phase != "after" || g.PageID != "page@1" || g.PageURL != "https://example.com/" {
		t.Errorf("goto: %+v", g)
	}
	if want := time.UnixMilli(1794786831088 + 11391 - 5468).UTC(); g.Start.Sub(want).Abs() > time.Millisecond {
		t.Errorf("goto starts %v, want %v", g.Start, want)
	}
	if c.CallID != "c@84" || c.Phase != "before" || c.Error == "" || c.Title != `click "Open Invoice Generator link"` {
		t.Errorf("click: %+v", c)
	}
}
