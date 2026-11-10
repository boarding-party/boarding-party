package api

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/boarding-party/boardingparty/internal/agentkey"
)

type fakeKeys struct{ key string }

func (f *fakeKeys) Status(context.Context) (agentkey.Status, error) {
	if f.key == "" {
		return agentkey.Status{}, nil
	}
	return agentkey.Status{Set: true, Kind: "api-key"}, nil
}

func (f *fakeKeys) SetAPIKey(_ context.Context, k string) error { f.key = k; return nil }

func TestAgentKeyRoutes(t *testing.T) {
	keys := &fakeKeys{}
	srv, _, svc := setupBoth(t, 1<<20)
	svc.AgentKey = keys
	for body, want := range map[string]int{
		`{"key":"bad key"}`: 400,
		`{"key":""}`:        400,
		`{"key":"sk-ant-` + strings.Repeat("a", 301) + `"}`: 400,
		`{"key":"sk-ant-good"}`: 204,
	} {
		if r := do(t, "PUT", srv.URL+"/api/agent-key", body); r.StatusCode != want {
			t.Errorf("PUT %.30s = %d, want %d", body, r.StatusCode, want)
		}
	}
	r := do(t, "GET", srv.URL+"/api/agent-key", "")
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || strings.Contains(string(b), "sk-ant-good") || !strings.Contains(string(b), `"set":true`) || !strings.Contains(string(b), `"kind":"api-key"`) {
		t.Fatalf("GET = %d %s", r.StatusCode, b)
	}
}
