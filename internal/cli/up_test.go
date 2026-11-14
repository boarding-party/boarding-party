package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// k3sYAML is the shape of /etc/rancher/k3s/k3s.yaml: everything named default.
const k3sYAML = `apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: Y2E=
    server: https://127.0.0.1:6443
  name: default
contexts:
- context:
    cluster: default
    user: default
  name: default
current-context: default
users:
- name: default
  user:
    client-certificate-data: Y2VydA==
    client-key-data: a2V5
`

// fakeK3s records what ensureCluster did instead of installing anything.
type fakeK3s struct {
	k3sDeps
	installed, waited bool
}

func newFakeK3s(t *testing.T, goos string) *fakeK3s {
	f := &fakeK3s{}
	f.k3sDeps = k3sDeps{
		goos:           goos,
		kubeconfigPath: filepath.Join(t.TempDir(), "config"),
		install:        func(context.Context) error { f.installed = true; return nil },
		readKubeconfig: func(context.Context) ([]byte, error) { return []byte(k3sYAML), nil },
		waitReady:      func(context.Context, string) error { f.waited = true; return nil },
	}
	return f
}

func TestUpLeavesAnExistingClusterAlone(t *testing.T) {
	f := newFakeK3s(t, "linux")
	made, err := ensureCluster(context.Background(), f.k3sDeps, []string{"gke"}, false, true, strings.NewReader(""), &bytes.Buffer{})
	if err != nil || made != "" || f.installed {
		t.Fatalf("made=%q err=%v installed=%v, want nothing done", made, err, f.installed)
	}
}

func TestUpInstallsK3sOnYes(t *testing.T) {
	f := newFakeK3s(t, "linux")
	var out bytes.Buffer
	made, err := ensureCluster(context.Background(), f.k3sDeps, nil, false, true, strings.NewReader("\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if made != k3sContext || !f.installed || !f.waited {
		t.Fatalf("made=%q installed=%v waited=%v", made, f.installed, f.waited)
	}
	if !strings.Contains(out.String(), "No Kubernetes cluster found") || !strings.Contains(out.String(), k3sInstall) {
		t.Fatalf("the question does not say what will run:\n%s", out.String())
	}
}

func TestUpStopsOnNo(t *testing.T) {
	f := newFakeK3s(t, "linux")
	_, err := ensureCluster(context.Background(), f.k3sDeps, nil, false, true, strings.NewReader("n\n"), &bytes.Buffer{})
	if err == nil || f.installed {
		t.Fatalf("err=%v installed=%v, want a stop and no install", err, f.installed)
	}
}

func TestUpNeedsYesWithoutATerminal(t *testing.T) {
	f := newFakeK3s(t, "linux")
	_, err := ensureCluster(context.Background(), f.k3sDeps, nil, false, false, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--yes") || f.installed {
		t.Fatalf("err=%v installed=%v, want a pointer to --yes", err, f.installed)
	}
	if _, err := ensureCluster(context.Background(), f.k3sDeps, nil, true, false, strings.NewReader(""), &bytes.Buffer{}); err != nil || !f.installed {
		t.Fatalf("--yes: err=%v installed=%v", err, f.installed)
	}
}

func TestUpOffersK3sOnlyOnLinux(t *testing.T) {
	f := newFakeK3s(t, "darwin")
	_, err := ensureCluster(context.Background(), f.k3sDeps, nil, true, true, strings.NewReader("y\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Linux") || f.installed {
		t.Fatalf("err=%v installed=%v, want a Linux-only refusal", err, f.installed)
	}
}

func TestUpReportsAFailedInstall(t *testing.T) {
	f := newFakeK3s(t, "linux")
	f.install = func(context.Context) error { return errors.New("exit status 1") }
	if _, err := ensureCluster(context.Background(), f.k3sDeps, nil, true, false, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("a failed install was not reported")
	}
}

func TestMergeK3sKeepsOtherContexts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	cur := clientcmdapi.NewConfig()
	cur.Clusters["gke"] = &clientcmdapi.Cluster{Server: "https://gke"}
	cur.AuthInfos["gke"] = &clientcmdapi.AuthInfo{Token: "t"}
	cur.Contexts["gke"] = &clientcmdapi.Context{Cluster: "gke", AuthInfo: "gke"}
	cur.CurrentContext = "gke"
	if err := clientcmd.WriteToFile(*cur, path); err != nil {
		t.Fatal(err)
	}
	if err := mergeK3s(path, []byte(k3sYAML)); err != nil {
		t.Fatal(err)
	}
	got, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Contexts["gke"] == nil || got.Contexts[k3sContext] == nil {
		t.Fatalf("contexts %v, want gke and k3s", got.Contexts)
	}
	if got.CurrentContext != k3sContext {
		t.Fatalf("current context %q, want k3s", got.CurrentContext)
	}
	c := got.Contexts[k3sContext]
	if c.Cluster != k3sContext || c.AuthInfo != k3sContext || got.Clusters[k3sContext].Server != "https://127.0.0.1:6443" {
		t.Fatalf("k3s entries not renamed: %+v", c)
	}
	if _, ok := got.Contexts["default"]; ok {
		t.Fatal("k3s's default names leaked in")
	}
}

func TestMergeK3sMakesAPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kube", "config")
	if err := mergeK3s(path, []byte(k3sYAML)); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600: it holds the cluster's admin key", st.Mode().Perm())
	}
}

func TestSSHHintNamesTheTunnel(t *testing.T) {
	env := map[string]string{"SSH_CONNECTION": "81.2.69.160 52144 10.0.0.5 22", "USER": "ops"}
	got := sshHint(func(k string) string { return env[k] }, 8790)
	if !strings.Contains(got, "ssh -N -L 8790:127.0.0.1:8790 ops@10.0.0.5") {
		t.Fatalf("hint %q", got)
	}
	if sshHint(func(string) string { return "" }, 8790) != "" {
		t.Fatal("a hint outside SSH")
	}
}

func TestK3sIsToldFromItsServerVersion(t *testing.T) {
	for v, want := range map[string]bool{
		"v1.33.5+k3s1":        true,
		"v1.34.1-gke.1829001": false,
		"v1.35.0":             false,
	} {
		if got := isK3s(v); got != want {
			t.Errorf("isK3s(%q) = %v, want %v", v, got, want)
		}
	}
}
