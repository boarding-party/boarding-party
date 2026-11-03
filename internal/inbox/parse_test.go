package inbox

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParsePlain(t *testing.T) {
	m, err := Parse(fixture(t, "plain.eml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Text, "please confirm.") {
		t.Errorf("text = %q", m.Text)
	}
	want := []string{"https://smesolutions.uk/confirm?token=abc123"}
	if !slices.Equal(m.Links, want) {
		t.Errorf("links = %v, want %v", m.Links, want)
	}
	if m.From != "no-reply@mail.smesolutions.uk" || m.Subject != "Confirm your email" || m.Received.IsZero() {
		t.Errorf("headers: %+v", m)
	}
}

func TestParseHTMLOnly(t *testing.T) {
	m, err := Parse(fixture(t, "html.eml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<", "secret", "color:red"} {
		if strings.Contains(m.Text, bad) {
			t.Errorf("text contains %q: %q", bad, m.Text)
		}
	}
	if !strings.Contains(m.Text, "Hello there") {
		t.Errorf("text = %q", m.Text)
	}
	want := []string{"https://smesolutions.uk/a", "http://example.test/b"}
	if !slices.Equal(m.Links, want) {
		t.Errorf("links = %v, want %v", m.Links, want)
	}
}

func TestParseMultipartPrefersText(t *testing.T) {
	m, err := Parse(fixture(t, "multipart.eml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(m.Text) != "Plain café body" {
		t.Errorf("text = %q", m.Text)
	}
	if m.Subject != "Café receipt" {
		t.Errorf("subject = %q", m.Subject)
	}
}

func TestParseLimits(t *testing.T) {
	big := "From: a@example.test\r\nTo: b@ex.test\r\nSubject: big\r\n\r\n" + strings.Repeat("x", 200<<10) + "\r\n"
	m, err := Parse([]byte(big))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Text) > MaxText || len(m.Text) == 0 {
		t.Errorf("len(Text) = %d", len(m.Text))
	}
	var sb strings.Builder
	sb.WriteString("From: a@example.test\r\nTo: b@ex.test\r\nSubject: links\r\n\r\n")
	for i := range 80 {
		fmt.Fprintf(&sb, "https://example.test/%d\r\n", i)
	}
	m, err = Parse([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Links) != MaxLinks {
		t.Errorf("len(Links) = %d", len(m.Links))
	}
}

func TestParseRecipients(t *testing.T) {
	m, err := Parse(fixture(t, "plus.eml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"cast@ex.test", "cast+john@ex.test"} {
		if !slices.Contains(m.To, w) {
			t.Errorf("To = %v missing %s", m.To, w)
		}
	}
	if m.From != "sender@example.test" {
		t.Errorf("from = %q", m.From)
	}
}

func TestParseGarbage(t *testing.T) {
	if _, err := Parse([]byte("not mail")); err == nil {
		t.Error("want error")
	}
}
