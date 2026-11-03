package inbox

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // registers non-UTF-8 charset decoding
	"golang.org/x/net/html"
)

// Limits on what a parsed message carries.
const (
	MaxText  = 64 << 10
	MaxLinks = 50

	maxRead = 1 << 20 // bytes read from any one body part
)

var urlRe = regexp.MustCompile(`https?://[^\s<>"')]+`)

// Parse reads a raw RFC 5322 message. ID, Persona and Filtered are left empty.
func Parse(raw []byte) (Message, error) {
	ent, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return Message{}, fmt.Errorf("parse message: %w", err)
	}
	if ent == nil {
		return Message{}, errors.New("parse message: no content")
	}
	h := ent.Header
	from := addrs(h.Get("From"))
	if len(from) == 0 {
		return Message{}, errors.New("parse message: no valid From header")
	}
	m := Message{From: from[0]}
	m.Verified = len(from) == 1 && authenticated(h.Get("Authentication-Results"), domainOf(from[0]))
	m.Subject, _ = h.Text("Subject")
	if t, err := mail.ParseDate(h.Get("Date")); err == nil {
		m.Received = t
	}

	seen := map[string]bool{}
	for _, k := range []string{"To", "Cc", "Delivered-To", "X-Original-To"} {
		for f := h.FieldsByKey(k); f.Next(); {
			v, _ := f.Text()
			for _, a := range addrs(v) {
				if k == "Delivered-To" || k == "X-Original-To" {
					if !slices.Contains(m.Delivered, a) {
						m.Delivered = append(m.Delivered, a)
					}
				}
				if !seen[a] {
					seen[a] = true
					m.To = append(m.To, a)
				}
			}
		}
	}

	plain, htmlBody := collect(ent)
	text, hrefs := plain, []string(nil)
	if htmlBody != "" {
		htmlText, htmlHrefs := htmlToText(htmlBody)
		hrefs = htmlHrefs
		if text == "" {
			text = htmlText
		}
	}
	m.Links = links(hrefs, text)
	m.Text = truncate(strings.TrimSpace(text), MaxText)
	return m, nil
}

func addrs(v string) []string {
	list, err := mail.ParseAddressList(v)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, strings.ToLower(a.Address))
	}
	return out
}

// collect walks the entity and returns the first text/plain body and the first text/html body.
func collect(e *message.Entity) (plain, htmlBody string) {
	var walk func(e *message.Entity)
	walk = func(e *message.Entity) {
		if mr := e.MultipartReader(); mr != nil {
			for {
				p, err := mr.NextPart()
				if err != nil {
					return
				}
				walk(p)
			}
		}
		mt, _, _ := e.Header.ContentType()
		if disp, _, _ := e.Header.ContentDisposition(); disp == "attachment" {
			return
		}
		switch mt {
		case "text/plain":
			if plain == "" {
				plain = readBody(e)
			}
		case "text/html":
			if htmlBody == "" {
				htmlBody = readBody(e)
			}
		}
	}
	walk(e)
	return plain, htmlBody
}

func readBody(e *message.Entity) string {
	b, _ := io.ReadAll(io.LimitReader(e.Body, maxRead))
	return strings.ToValidUTF8(string(b), "")
}

var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "table": true, "ul": true, "ol": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "blockquote": true, "hr": true,
}

// htmlToText flattens HTML to text and returns the href values in document order.
func htmlToText(src string) (string, []string) {
	var sb strings.Builder
	var hrefs []string
	skip := 0
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return collapse(sb.String()), hrefs
		case html.TextToken:
			if skip == 0 {
				sb.Write(z.Text())
			}
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			name, hasAttr := z.TagName()
			n := string(name)
			if n == "script" || n == "style" {
				switch tt {
				case html.StartTagToken:
					skip++
				case html.EndTagToken:
					skip = max(skip-1, 0)
				}
				continue
			}
			if blockTags[n] {
				sb.WriteByte('\n')
			}
			for hasAttr && n == "a" && tt != html.EndTagToken {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				if string(k) == "href" {
					hrefs = append(hrefs, string(v))
				}
			}
		}
	}
}

func domainOf(addr string) string {
	_, d, _ := strings.Cut(addr, "@")
	return d
}

var commentRe = regexp.MustCompile(`\([^)]*\)`)

// authenticated reports whether an Authentication-Results header (the topmost one, added by the
// receiving provider) shows dmarc=pass, or dkim=pass signed by the From domain or a parent of it.
func authenticated(ar, fromDomain string) bool {
	if ar == "" || fromDomain == "" {
		return false
	}
	ar = commentRe.ReplaceAllString(strings.ToLower(ar), " ")
	clauses := strings.Split(ar, ";")
	for _, c := range clauses[1:] { // first clause is the authserv-id
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		method, result, ok := strings.Cut(fields[0], "=")
		if !ok || result != "pass" {
			continue
		}
		switch method {
		case "dmarc":
			return true
		case "dkim":
			for _, f := range fields[1:] {
				k, v, _ := strings.Cut(f, "=")
				if k != "header.d" && k != "header.i" {
					continue
				}
				d := v
				if strings.Contains(v, "@") {
					d = domainOf(v)
				}
				if d != "" && (fromDomain == d || strings.HasSuffix(fromDomain, "."+d)) {
					return true
				}
			}
		}
	}
	return false
}

var blankRuns = regexp.MustCompile(`\n[ \t]*(\n[ \t]*)+`)

func collapse(s string) string {
	return strings.TrimSpace(blankRuns.ReplaceAllString(s, "\n\n"))
}

// links returns http(s) URLs from hrefs then from text, deduplicated, capped at MaxLinks.
func links(hrefs []string, text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		l := strings.ToLower(u)
		if !(strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")) || seen[u] || len(out) >= MaxLinks {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	for _, h := range hrefs {
		add(h)
	}
	for _, u := range urlRe.FindAllString(text, -1) {
		add(strings.TrimRight(u, ".,;:!?"))
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
