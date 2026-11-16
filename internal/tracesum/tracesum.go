// Package tracesum reads a Playwright trace.zip into what the board's run
// page shows beside the recording: the actions with their page URLs and
// snapshot names, and the network requests, all on the wall clock.
//
// The trace is the site's data: every string from it is untrusted text.
// URLs lose their query and fragment, and credential headers are redacted,
// as the console recorder does.
package tracesum

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SummaryName is the artifact the director keeps a run's summary in.
const SummaryName = "trace-summary.json"

// ErrNotTrace means the zip holds no Playwright trace.
var ErrNotTrace = errors.New("not a Playwright trace")

// Bounds on what is read: a trace is the site's data and can be anything.
const (
	maxEntryBytes = 64 << 20 // uncompressed, per .trace or .network entry
	maxLine       = 1 << 20  // snapshot lines carry whole pages
	maxResources  = 5000
	maxHeader     = 1024
	maxTitle      = 200
)

// Summary is a trace as the run page uses it.
type Summary struct {
	Actions []Action   `json:"actions"`
	Network []Resource `json:"network"`
}

// Action is one Playwright call: a click, a navigation, a wait.
type Action struct {
	Title    string    `json:"title"`
	PageURL  string    `json:"pageUrl,omitempty"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Error    string    `json:"error,omitempty"`
	Snapshot string    `json:"snapshot,omitempty"` // the "after" snapshot name, else "before"
	PageID   string    `json:"pageId,omitempty"`
	// CallID and Phase address the snapshot in the trace viewer's service
	// worker: /snapshot/<callId>?phase=<phase>.
	CallID string `json:"callId,omitempty"`
	Phase  string `json:"phase,omitempty"`
}

// Resource is one network request the page made.
type Resource struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Type            string            `json:"type,omitempty"`
	Status          int               `json:"status"`
	Start           time.Time         `json:"start"`
	Duration        time.Duration     `json:"duration"`
	Failed          bool              `json:"failed,omitempty"`
	RequestHeaders  map[string]string `json:"requestHeaders,omitempty"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
}

// Read summarises the trace zip in r. Entries named *.trace and *.network
// are read at any folder depth, so a zip of the traces folder itself works;
// resources are never read.
func Read(r io.ReaderAt, size int64) (Summary, error) {
	z, err := zip.NewReader(r, size)
	if err != nil {
		return Summary{}, err
	}
	out := Summary{Actions: []Action{}, Network: []Resource{}}
	traces := 0
	for _, f := range z.File {
		switch path.Ext(f.Name) {
		case ".trace":
			acts, err := readEntry(f, readActions)
			if err != nil {
				return Summary{}, err
			}
			traces++
			out.Actions = append(out.Actions, acts...)
		case ".network":
			res, err := readEntry(f, readNetwork)
			if err != nil {
				return Summary{}, err
			}
			out.Network = append(out.Network, res...)
		}
	}
	if traces == 0 {
		return Summary{}, ErrNotTrace
	}
	sort.SliceStable(out.Actions, func(i, j int) bool { return out.Actions[i].Start.Before(out.Actions[j].Start) })
	sort.SliceStable(out.Network, func(i, j int) bool { return out.Network[i].Start.Before(out.Network[j].Start) })
	if len(out.Network) > maxResources {
		out.Network = out.Network[:maxResources]
	}
	return out, nil
}

func readEntry[T any](f *zip.File, read func(*bufio.Scanner) ([]T, error)) ([]T, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	sc := bufio.NewScanner(io.LimitReader(rc, maxEntryBytes))
	sc.Buffer(make([]byte, 64<<10), maxLine)
	out, err := read(sc)
	if err != nil {
		return nil, err
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return nil, err
	}
	return out, nil
}

// event is the union of the trace lines read here.
type event struct {
	Type          string          `json:"type"`
	WallTime      float64         `json:"wallTime"`
	MonotonicTime float64         `json:"monotonicTime"`
	CallID        string          `json:"callId"`
	StartTime     float64         `json:"startTime"`
	EndTime       float64         `json:"endTime"`
	Method        string          `json:"method"`
	Params        json.RawMessage `json:"params"`
	PageID        string          `json:"pageId"`
	Before        string          `json:"beforeSnapshot"`
	After         string          `json:"afterSnapshot"`
	Error         *struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"error"`
	Snapshot *struct {
		SnapshotName string `json:"snapshotName"`
		FrameURL     string `json:"frameUrl"`
	} `json:"snapshot"`
}

// readActions reads one .trace entry. Its times are monotonic milliseconds;
// the context-options line pins them to the wall clock.
func readActions(sc *bufio.Scanner) ([]Action, error) {
	var (
		wall, mono float64
		clocked    bool
		order      []string
		calls      = map[string]*Action{}
		snaps      = map[string]string{} // call id -> snapshot name to show
		pageURL    = map[string]string{} // snapshot name -> main frame URL (its first frame)
	)
	at := func(ms float64) time.Time {
		return time.UnixMilli(0).Add(time.Duration((wall + ms - mono) * float64(time.Millisecond))).UTC()
	}
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "context-options":
			wall, mono, clocked = e.WallTime, e.MonotonicTime, true
		case "before":
			if e.PageID == "" || !clocked {
				continue // browser and context calls: nothing on the page
			}
			calls[e.CallID] = &Action{Title: title(e.Method, e.Params), Start: at(e.StartTime), End: at(e.StartTime), PageID: e.PageID}
			snaps[e.CallID] = e.Before
			order = append(order, e.CallID)
		case "after":
			a := calls[e.CallID]
			if a == nil {
				continue
			}
			a.End = at(e.EndTime)
			if e.After != "" {
				snaps[e.CallID] = e.After
			}
			if e.Error != nil && e.Error.Error.Message != "" {
				a.Error = cut(cleanText(firstLine(e.Error.Error.Message)), maxTitle)
			}
		case "frame-snapshot":
			if s := e.Snapshot; s != nil && s.SnapshotName != "" {
				if _, ok := pageURL[s.SnapshotName]; !ok {
					pageURL[s.SnapshotName] = cleanURL(s.FrameURL)
				}
			}
		}
	}
	if !clocked {
		return nil, ErrNotTrace
	}
	out := make([]Action, 0, len(order))
	for _, id := range order {
		a := calls[id]
		a.Snapshot = snaps[id]
		a.CallID = id
		a.Phase, _, _ = strings.Cut(a.Snapshot, "@")
		a.PageURL = pageURL[a.Snapshot]
		out = append(out, *a)
	}
	return out, nil
}

type header struct{ Name, Value string }

type harEntry struct {
	Type     string `json:"type"`
	Snapshot struct {
		Started  time.Time `json:"startedDateTime"`
		Time     float64   `json:"time"`
		Resource string    `json:"_resourceType"`
		Request  struct {
			Method  string   `json:"method"`
			URL     string   `json:"url"`
			Headers []header `json:"headers"`
		} `json:"request"`
		Response struct {
			Status  int      `json:"status"`
			Failure string   `json:"_failureText"`
			Headers []header `json:"headers"`
			Content struct {
				MimeType string `json:"mimeType"`
			} `json:"content"`
		} `json:"response"`
	} `json:"snapshot"`
}

func readNetwork(sc *bufio.Scanner) ([]Resource, error) {
	var out []Resource
	for sc.Scan() && len(out) < maxResources {
		var e harEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Type != "resource-snapshot" {
			continue
		}
		s := e.Snapshot
		typ := s.Resource
		if typ == "" {
			typ, _, _ = strings.Cut(s.Response.Content.MimeType, ";")
		}
		out = append(out, Resource{
			Method:          s.Request.Method,
			URL:             cleanURL(s.Request.URL),
			Type:            typ,
			Status:          s.Response.Status,
			Start:           s.Started.UTC(),
			Duration:        time.Duration(max(s.Time, 0) * float64(time.Millisecond)),
			Failed:          s.Response.Failure != "" || s.Response.Status == 0,
			RequestHeaders:  headers(s.Request.Headers),
			ResponseHeaders: headers(s.Response.Headers),
		})
	}
	return out, nil
}

// secretHeader matches the names of headers that may hold credentials:
// their values never leave the director.
var secretHeader = regexp.MustCompile(`(?i)cookie|auth|token|key|secret|session|csrf|xsrf|signature|password`)

func headers(hs []header) map[string]string {
	if len(hs) == 0 {
		return nil
	}
	out := make(map[string]string, len(hs))
	for _, h := range hs {
		v := h.Value
		switch {
		case secretHeader.MatchString(h.Name):
			v = "[redacted]"
		default:
			v = cleanText(v)
		}
		out[h.Name] = cut(v, maxHeader)
	}
	return out
}

// cleanURL drops the query, fragment and credentials: they can carry tokens
// from emails.
func cleanURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		if i := strings.IndexAny(s, "?#"); i >= 0 {
			s = s[:i]
		}
		return cut(s, 2048)
	}
	u.RawQuery, u.Fragment, u.RawFragment, u.User = "", "", "", nil
	return cut(u.String(), 2048)
}

// urlInText matches a URL inside free text, up to a space, quote or angle
// bracket. A comma may sit inside a path, so it never ends the match.
var urlInText = regexp.MustCompile(`(?i)(?:https?|wss?)://[^\s"'<>]+`)

// cleanText runs cleanURL over every URL in s: error messages and headers
// quote page URLs.
func cleanText(s string) string { return urlInText.ReplaceAllStringFunc(s, cleanURL) }

var (
	roleName = regexp.MustCompile(`^internal:role=([a-z]+)\[name="(.*)"[is]?\]$`)
	quoted   = regexp.MustCompile(`^internal:(?:text|label|testid|attr)=(?:\[[a-z-]+=)?"(.*)"[is]?\]?$`)
)

// title names a call the way a person reads it: "click button "Pay now"".
func title(method string, params json.RawMessage) string {
	var p struct {
		URL      string `json:"url"`
		Selector string `json:"selector"`
	}
	_ = json.Unmarshal(params, &p)
	what := ""
	switch {
	case p.URL != "":
		what = cleanURL(p.URL)
	case p.Selector != "":
		p.Selector = cleanText(p.Selector)
		parts := strings.Split(p.Selector, " >> ")
		last := parts[len(parts)-1]
		if m := roleName.FindStringSubmatch(last); m != nil {
			what = m[1] + ` "` + m[2] + `"`
		} else if m := quoted.FindStringSubmatch(last); m != nil {
			what = `"` + m[1] + `"`
		} else {
			what = last
		}
	}
	if what == "" {
		return cut(method, maxTitle)
	}
	return cut(method+" "+what, maxTitle)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8Start(s[n]) {
		n--
	}
	return s[:n]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
