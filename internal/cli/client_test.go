package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSendsBearer(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		w.Write([]byte("[]"))
	}))
	defer srv.Close()
	c := newClient(srv.URL, "s3cret")
	if _, err := c.Runs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.PutProject(context.Background(), []byte("name: x")); err != nil {
		t.Fatal(err)
	}
	for _, h := range got {
		if h != "Bearer s3cret" {
			t.Errorf("Authorization = %q", h)
		}
	}
	if len(got) != 2 {
		t.Errorf("requests = %d", len(got))
	}
}

func TestTokenReusesWhatEnsureRead(t *testing.T) {
	g := &globals{adminToken: "from-ensure"}
	// nil config: any cluster call would fail.
	if tok, err := g.token(context.Background(), nil, "bp-test"); err != nil || tok != "from-ensure" {
		t.Errorf("token = %q, %v", tok, err)
	}
}
