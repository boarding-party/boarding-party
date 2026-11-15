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
	installed string // "docker", "native" or ""
	waited    bool
}

func newFakeK3s(t *testing.T, goos string, docker bool) *fakeK3s {
	f := &fakeK3s{}
	way := func(name string, real k3sWay) k3sWay {
		real.install = func(context.Context) error { f.installed = name; return nil }
		real.readKubeconfig = func(context.Context) ([]byte, error) { return []byte(k3sYAML), nil }
		return real
	}
	f.k3sDeps = k3sDeps{
		goos:           goos,
		kubeconfigPath: filepath.Join(t.TempDir(), "config"),
		dockerRunning:  func(context.Context) bool { return docker },
		docker:         way("docker", dockerK3s()),
		native:         way("native", nativeK3s()),
		waitReady:      func(context.Context, string) error { f.waited = true; return nil },
	}
	return f
}

func TestUpLeavesAnExistingClusterAlone(t *testing.T) {
	f := newFakeK3s(t, "linux", true)
	made, err := ensureCluster(context.Background(), f.k3sDeps, []string{"gke"}, false, true, strings.NewReader(""), &bytes.Buffer{})
	if err != nil || made != "" || f.installed != "" {
		t.Fatalf("made=%q err=%v installed=%q, want nothing done", made, err, f.installed)
	}
}

func TestUpInstallsK3sOnYes(t *testing.T) {
	f := newFakeK3s(t, "linux", false)
	var out bytes.Buffer
	made, err := ensureCluster(context.Background(), f.k3sDeps, nil, false, true, strings.NewReader("\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if made != k3sContext || f.installed != "native" || !f.waited {
		t.Fatalf("made=%q installed=%q waited=%v", made, f.installed, f.waited)
	}
	if !strings.Contains(out.String(), "No Kubernetes cluster found") || !strings.Contains(out.String(), k3sInstall) {
		t.Fatalf("the question does not say what will run:\n%s", out.String())
	}
}

func TestUpStopsOnNo(t *testing.T) {
	f := newFakeK3s(t, "linux", true)
	_, err := ensureCluster(context.Background(), f.k3sDeps, nil, false, true, strings.NewReader("n\n"), &bytes.Buffer{})
	if err == nil || f.installed != "" {
		t.Fatalf("err=%v installed=%q, want a stop and no install", err, f.installed)
	}
}

func TestUpNeedsYesWithoutATerminal(t *testing.T) {
	f := newFakeK3s(t, "linux", false)
	_, err := ensureCluster(context.Background(), f.k3sDeps, nil, false, false, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--yes") || f.installed != "" {
		t.Fatalf("err=%v installed=%q, want a pointer to --yes", err, f.installed)
	}
	if _, err := ensureCluster(context.Background(), f.k3sDeps, nil, true, false, strings.NewReader(""), &bytes.Buffer{}); err != nil || f.installed == "" {
		t.Fatalf("--yes: err=%v installed=%q", err, f.installed)
	}
}

// Docker is the one requirement on every OS: with it running, k3s starts
// in a container, also on a Mac.
func TestUpStartsK3sInDockerWhenDockerRuns(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		f := newFakeK3s(t, goos, true)
		var out bytes.Buffer
		made, err := ensureCluster(context.Background(), f.k3sDeps, nil, true, false, nil, &out)
		if err != nil || made != k3sContext || f.installed != "docker" {
			t.Fatalf("%s: made=%q err=%v installed=%q, want k3s in Docker", goos, made, err, f.installed)
		}
		if !strings.Contains(out.String(), "docker run") || !strings.Contains(out.String(), k3sImage) {
			t.Fatalf("%s: the question does not say what will run:\n%s", goos, out.String())
		}
	}
}

func TestUpAsksForDockerWithoutIt(t *testing.T) {
	f := newFakeK3s(t, "darwin", false)
	_, err := ensureCluster(context.Background(), f.k3sDeps, nil, true, true, strings.NewReader("y\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Docker") || f.installed != "" {
		t.Fatalf("err=%v installed=%q, want a pointer to Docker", err, f.installed)
	}
}

func TestUpLocalPicksTheWay(t *testing.T) {
	for _, c := range []struct {
		goos, local string
		docker      bool
		want        string // "" = an error
	}{
		{"linux", "k3s", true, "native"},
		{"linux", "docker", true, "docker"},
		{"linux", "docker", false, ""},
		{"darwin", "k3s", true, ""},
		{"linux", "podman", true, ""},
	} {
		f := newFakeK3s(t, c.goos, c.docker)
		f.local = c.local
		_, err := ensureCluster(context.Background(), f.k3sDeps, nil, true, false, nil, &bytes.Buffer{})
		if f.installed != c.want || (c.want == "") != (err != nil) {
			t.Errorf("%s --local %s docker=%v: installed=%q err=%v, want %q", c.goos, c.local, c.docker, f.installed, err, c.want)
		}
	}
}

func TestUpReportsAFailedInstall(t *testing.T) {
	f := newFakeK3s(t, "linux", false)
	f.native.install = func(context.Context) error { return errors.New("exit status 1") }
	if _, err := ensureCluster(context.Background(), f.k3sDeps, nil, true, false, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("a failed install was not reported")
	}
}

func TestUpPointsAtTheLogsWhenTheNodeStaysDown(t *testing.T) {
	f := newFakeK3s(t, "darwin", true)
	f.waitReady = func(context.Context, string) error { return errors.New("k3s node not ready after 3 minutes") }
	_, err := ensureCluster(context.Background(), f.k3sDeps, nil, true, false, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "docker logs "+k3sContainer) {
		t.Fatalf("err=%v, want a pointer to the container's logs", err)
	}
}

// The k3s API carries the cluster's admin key: published on loopback only.
func TestK3sContainerListensOnLoopbackOnly(t *testing.T) {
	args := strings.Join(k3sDockerArgs("51234"), " ")
	for _, want := range []string{"-p 127.0.0.1:51234:6443", "--privileged", "--restart unless-stopped", k3sContainer + ":/var/lib/rancher/k3s", k3sImage} {
		if !strings.Contains(args, want) {
			t.Errorf("docker run %s\nmissing %q", args, want)
		}
	}
}

func TestDockerHostPort(t *testing.T) {
	for out, want := range map[string]string{
		"127.0.0.1:51234\n":           "127.0.0.1:51234",
		"0.0.0.0:51234\n[::]:51234\n": "127.0.0.1:51234",
	} {
		if got, err := dockerHostPort(out); err != nil || got != want {
			t.Errorf("dockerHostPort(%q) = %q, %v; want %q", out, got, err, want)
		}
	}
	if _, err := dockerHostPort(""); err == nil {
		t.Error("no port, no error")
	}
}

func TestPointKubeconfigAt(t *testing.T) {
	raw, err := pointKubeconfigAt([]byte(k3sYAML), "https://127.0.0.1:51234")
	if err != nil {
		t.Fatal(err)
	}
	k, err := clientcmd.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s := k.Clusters["default"].Server; s != "https://127.0.0.1:51234" {
		t.Fatalf("server %q", s)
	}
}

func TestDockerMemoryNote(t *testing.T) {
	if dockerMemoryNote(8<<30) != "" {
		t.Error("a note with 8 GB")
	}
	if n := dockerMemoryNote(2 << 30); !strings.Contains(n, "2.0 GB") || !strings.Contains(n, "6 GB") {
		t.Errorf("note %q", n)
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
