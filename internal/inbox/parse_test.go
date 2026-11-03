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

func TestParseVerified(t *testing.T) {
	tests := []struct {
		file string
		want bool
	}{
		{"verified.eml", true},
		{"wrongdomain.eml", false},
		{"forged.eml", false},
		{"plain.eml", false},
		{"twofrom.eml", false},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			m, err := Parse(fixture(t, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if m.Verified != tc.want {
				t.Errorf("Verified = %v, want %v", m.Verified, tc.want)
			}
		})
	}
	m, _ := Parse(fixture(t, "twofrom.eml"))
	if m.From != "a@smesolutions.uk" {
		t.Errorf("from = %q", m.From)
	}
}

func TestParseHTMLLinksWithPlain(t *testing.T) {
	m, err := Parse(fixture(t, "buttons.eml"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://example.test/button", "https://example.test/plain"}
	if !slices.Equal(m.Links, want) {
		t.Errorf("links = %v, want %v", m.Links, want)
	}
	if strings.Contains(m.Text, "button") {
		t.Errorf("text should be the plain part: %q", m.Text)
	}
}

func TestParseDelivered(t *testing.T) {
	m, err := Parse(fixture(t, "delivered.eml"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Delivered, []string{"ada-b@ex.test"}) {
		t.Errorf("delivered = %v", m.Delivered)
	}
}

func withAR(ar string) []byte {
	return []byte("From: Shop <no-reply@smesolutions.uk>\r\nTo: john@ex.test\r\nSubject: s\r\nAuthentication-Results: " + ar + "\r\n\r\nhi\r\n")
}

func TestParseAuthenticationResults(t *testing.T) {
	tests := []struct {
		name, ar string
		verified bool
		serv     string
	}{
		{"gmail", "mx.google.com; dmarc=pass (p=NONE) header.from=smesolutions.uk", true, "mx.google.com"},
		{"version and case", "MX.Google.com 1; DMARC=PASS header.from=SMESolutions.uk", true, "mx.google.com"},
		{"quoted semicolon", `mx.google.com; spf=fail reason="x; dmarc=pass header.from=smesolutions.uk"`, false, "mx.google.com"},
		{"nested comment", "mx.google.com; spf=fail (a (b) dmarc=pass header.from=smesolutions.uk)", false, "mx.google.com"},
		{"dmarc other domain", "mx.google.com; dmarc=pass header.from=other.test", false, "mx.google.com"},
		{"dmarc no header.from", "mx.google.com; dmarc=pass", false, "mx.google.com"},
		{"dkim parent domain", "mx.google.com; dkim=pass header.d=smesolutions.uk", true, "mx.google.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Parse(withAR(tc.ar))
			if err != nil {
				t.Fatal(err)
			}
			if m.Verified != tc.verified || m.AuthServ != tc.serv {
				t.Errorf("Verified=%v AuthServ=%q, want %v %q", m.Verified, m.AuthServ, tc.verified, tc.serv)
			}
		})
	}
}
