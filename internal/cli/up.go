package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	tinyruntime "github.com/tiny-systems/tiny/pkg/runtime"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	// k3sInstall is k3s's official installer; it uses sudo itself when not root.
	k3sInstall    = "curl -sfL https://get.k3s.io | sh -"
	k3sKubeconfig = "/etc/rancher/k3s/k3s.yaml"
	// k3sImage is the k3s that up starts in Docker: the stable channel's
	// release, which is what k3sInstall installs.
	k3sImage       = "rancher/k3s:v1.37.2-k3s1"
	k3sContainer   = "boardingparty-k3s" // also the name of its volume
	k3sContext     = "k3s"
	upNamespace    = "boardingparty"
	sshDefaultPort = 8790
	// dockerMinMemory is what Docker should have for k3s, the director and
	// a few agent pods; Docker Desktop's default is often less.
	dockerMinMemory = 6 << 30
)

// k3sWay is one way to get k3s on this machine.
type k3sWay struct {
	where          string // "in Docker", "on this machine"
	shows          string // what install runs, shown before asking
	logs           string // where to look when the node stays down
	remove         string // how to take it away again
	install        func(ctx context.Context) error
	readKubeconfig func(ctx context.Context) ([]byte, error)
}

// k3sDeps is how up gets a cluster on a machine that has none; tests swap
// every part so nothing is installed.
type k3sDeps struct {
	goos           string
	kubeconfigPath string
	local          string // --local: "", "docker" or "k3s"
	dockerRunning  func(ctx context.Context) bool
	docker, native k3sWay
	waitReady      func(ctx context.Context, kubeContext string) error
}

func realK3s(local string) k3sDeps {
	return k3sDeps{
		goos:           runtime.GOOS,
		kubeconfigPath: clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename(),
		local:          local,
		dockerRunning:  dockerRunning,
		docker:         dockerK3s(),
		native:         nativeK3s(),
		waitReady:      waitNodeReady,
	}
}

// nativeK3s installs k3s as a service with its official installer (Linux).
func nativeK3s() k3sWay {
	return k3sWay{
		where:  "on this machine",
		shows:  "k3s's official installer, as root:\n  " + k3sInstall,
		logs:   "sudo journalctl -u k3s",
		remove: "/usr/local/bin/k3s-uninstall.sh",
		install: func(ctx context.Context) error {
			c := exec.CommandContext(ctx, "sh", "-c", k3sInstall)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stderr, os.Stderr
			return c.Run()
		},
		readKubeconfig: func(ctx context.Context) ([]byte, error) {
			if os.Geteuid() == 0 {
				return os.ReadFile(k3sKubeconfig)
			}
			c := exec.CommandContext(ctx, "sudo", "cat", k3sKubeconfig)
			c.Stdin, c.Stderr = os.Stdin, os.Stderr
			return c.Output()
		},
	}
}

// k3sDockerArgs is the docker run that starts k3s, its API on hostPort.
// k3s needs a privileged container (it runs containerd and iptables); its
// state lives in a volume, so a restart keeps the runs.
func k3sDockerArgs(hostPort string) []string {
	return []string{"run", "-d", "--name", k3sContainer, "--hostname", k3sContainer,
		"--privileged", "--restart", "unless-stopped",
		"--tmpfs", "/run", "--tmpfs", "/var/run",
		"-v", k3sContainer + ":/var/lib/rancher/k3s",
		"-p", "127.0.0.1:" + hostPort + ":6443",
		k3sImage, "server", "--disable=traefik"}
}

// dockerK3s runs k3s in a Docker container: every OS with Docker.
func dockerK3s() k3sWay {
	return k3sWay{
		where:  "in Docker",
		shows:  "docker " + strings.Join(k3sDockerArgs("<free port>"), " "),
		logs:   "docker logs " + k3sContainer,
		remove: "docker rm -f " + k3sContainer + " && docker volume rm " + k3sContainer,
		install: func(ctx context.Context) error {
			// Left from an earlier up whose kubeconfig entry is gone: start it again.
			if exec.CommandContext(ctx, "docker", "inspect", k3sContainer).Run() == nil {
				return dockerRun(ctx, "start", k3sContainer)
			}
			if out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.MemTotal}}").Output(); err == nil {
				if n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
					if note := dockerMemoryNote(n); note != "" {
						fmt.Fprintln(os.Stderr, note)
					}
				}
			}
			port, err := freePort()
			if err != nil {
				return err
			}
			return dockerRun(ctx, k3sDockerArgs(strconv.Itoa(port))...)
		},
		readKubeconfig: func(ctx context.Context) ([]byte, error) {
			// k3s writes its kubeconfig a few seconds after the container starts.
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			var raw []byte
			for {
				var err error
				if raw, err = exec.CommandContext(ctx, "docker", "exec", k3sContainer, "cat", k3sKubeconfig).Output(); err == nil {
					break
				}
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("k3s wrote no kubeconfig in its container (see `docker logs %s`)", k3sContainer)
				case <-time.After(2 * time.Second):
				}
			}
			out, err := exec.CommandContext(ctx, "docker", "port", k3sContainer, "6443/tcp").Output()
			if err != nil {
				return nil, fmt.Errorf("docker port %s: %w", k3sContainer, err)
			}
			hp, err := dockerHostPort(string(out))
			if err != nil {
				return nil, err
			}
			return pointKubeconfigAt(raw, "https://"+hp)
		},
	}
}

func dockerRun(ctx context.Context, args ...string) error {
	c := exec.CommandContext(ctx, "docker", args...)
	c.Stdout, c.Stderr = io.Discard, os.Stderr
	return c.Run()
}

// dockerRunning reports whether a Docker daemon answers. A daemon that
// hangs counts as none.
func dockerRunning(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// dockerHostPort turns `docker port` output into a host:port to dial.
func dockerHostPort(out string) (string, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	host, port, err := net.SplitHostPort(strings.TrimSpace(line))
	if err != nil || port == "" {
		return "", fmt.Errorf("no published k3s port in %q", out)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

// pointKubeconfigAt sets every cluster's server to url: inside the
// container k3s says 127.0.0.1:6443, outside it is the published port.
func pointKubeconfigAt(raw []byte, url string) ([]byte, error) {
	k, err := clientcmd.Load(raw)
	if err != nil {
		return nil, err
	}
	for _, c := range k.Clusters {
		c.Server = url
	}
	return clientcmd.Write(*k)
}

// dockerMemoryNote warns when Docker has less memory than k3s and a few
// agents need. "" when it has enough.
func dockerMemoryNote(memBytes int64) string {
	if memBytes >= dockerMinMemory {
		return ""
	}
	return fmt.Sprintf("Note: Docker has %.1f GB of memory; agents need 6 GB or more. "+
		"Raise it in Docker's settings (Resources) if runs fail to start.", float64(memBytes)/(1<<30))
}

// pickWay chooses how to get k3s: in Docker when it runs, else the
// installer on Linux. --local forces one.
func pickWay(ctx context.Context, d k3sDeps) (k3sWay, error) {
	linux := d.goos == "linux"
	switch d.local {
	case "docker":
		if !d.dockerRunning(ctx) {
			return k3sWay{}, errors.New("--local docker: Docker is not running")
		}
		return d.docker, nil
	case "k3s":
		if !linux {
			return k3sWay{}, errors.New("--local k3s installs k3s on Linux only; use --local docker")
		}
		return d.native, nil
	case "":
	default:
		return k3sWay{}, fmt.Errorf("--local %q: want docker or k3s", d.local)
	}
	if d.dockerRunning(ctx) {
		return d.docker, nil
	}
	if linux {
		return d.native, nil
	}
	return k3sWay{}, errors.New("no Kubernetes cluster found, and up starts one (k3s) in Docker: " +
		"install and start Docker Desktop or OrbStack, then run boardingparty up again; or point kubectl at a cluster")
}

// ensureCluster makes sure there is a cluster to install on. With
// kubeconfig contexts it does nothing and returns "". With none it offers
// k3s (in Docker, or installed on Linux), starts it on yes, adds it to the
// kubeconfig and returns its context name.
func ensureCluster(ctx context.Context, d k3sDeps, contexts []string, yes, interactive bool, in io.Reader, out io.Writer) (string, error) {
	if len(contexts) > 0 {
		return "", nil
	}
	way, err := pickWay(ctx, d)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(out, "No Kubernetes cluster found. boardingparty runs on k3s, a one-machine cluster.\n"+
		"Starting it %s runs %s\n", way.where, way.shows)
	if !yes {
		if !interactive {
			return "", errors.New("not a terminal, so cannot ask before starting k3s: pass --yes")
		}
		fmt.Fprintf(out, "Start k3s %s? [Y/n] ", way.where)
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "" && a != "y" && a != "yes" {
			return "", errors.New("stopped: no cluster to install on")
		}
	}
	if err := way.install(ctx); err != nil {
		return "", fmt.Errorf("start k3s %s: %w", way.where, err)
	}
	raw, err := way.readKubeconfig(ctx)
	if err != nil {
		return "", fmt.Errorf("read k3s's kubeconfig: %w", err)
	}
	if err := mergeK3s(d.kubeconfigPath, raw); err != nil {
		return "", fmt.Errorf("add k3s to %s: %w", d.kubeconfigPath, err)
	}
	fmt.Fprintf(out, "✓ k3s started %s; kubeconfig context %q in %s\n  (remove it later: %s)\n",
		way.where, k3sContext, d.kubeconfigPath, way.remove)
	if err := d.waitReady(ctx, k3sContext); err != nil {
		return "", fmt.Errorf("%w (see `%s`)", err, way.logs)
	}
	fmt.Fprintln(out, "✓ k3s node ready")
	return k3sContext, nil
}

// mergeK3s adds k3s's kubeconfig (where everything is named "default") to
// the file at path under the name k3s, makes it current, and keeps the
// file private: it carries the cluster's admin key.
func mergeK3s(path string, raw []byte) error {
	k, err := clientcmd.Load(raw)
	if err != nil {
		return err
	}
	kc := k.Contexts[k.CurrentContext]
	if kc == nil || k.Clusters[kc.Cluster] == nil || k.AuthInfos[kc.AuthInfo] == nil {
		return errors.New("k3s kubeconfig has no usable current context")
	}
	cur := clientcmdapi.NewConfig()
	if _, err := os.Stat(path); err == nil {
		if cur, err = clientcmd.LoadFromFile(path); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cur.Clusters[k3sContext] = k.Clusters[kc.Cluster]
	cur.AuthInfos[k3sContext] = k.AuthInfos[kc.AuthInfo]
	cur.Contexts[k3sContext] = &clientcmdapi.Context{Cluster: k3sContext, AuthInfo: k3sContext}
	cur.CurrentContext = k3sContext
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := clientcmd.WriteToFile(*cur, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// waitNodeReady waits up to 3 minutes for a Ready node.
func waitNodeReady(ctx context.Context, kubeContext string) error {
	cfg, err := loadRestConfig(kubeContext)
	if err != nil {
		return err
	}
	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for {
		nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err == nil {
			for _, n := range nodes.Items {
				for _, c := range n.Status.Conditions {
					if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("k3s node not ready after 3 minutes")
		case <-time.After(2 * time.Second):
		}
	}
}

// isK3s reports whether a server version is k3s's (v1.33.5+k3s1).
func isK3s(gitVersion string) bool { return strings.Contains(gitVersion, "+k3s") }

// sshHint tells someone logged in over SSH how to reach the board, which
// listens on the server's 127.0.0.1, from their laptop. "" outside SSH.
func sshHint(getenv func(string) string, port int) string {
	f := strings.Fields(getenv("SSH_CONNECTION"))
	if len(f) < 3 {
		return ""
	}
	host := f[2]
	if u := getenv("USER"); u != "" {
		host = u + "@" + host
	}
	return fmt.Sprintf("You are on this server over SSH. On your laptop run:\n  ssh -N -L %d:127.0.0.1:%d %s\nthen open the link below there.", port, port, host)
}

func newUp(g *globals) *cobra.Command {
	var o boardOpts
	var yes bool
	var local string
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Install everything (k3s if there is no cluster, tiny, the director) and open the board",
		Long: "Uses a kubeconfig context if there is one (asking which on a terminal). With none,\n" +
			"offers to start k3s: in Docker when Docker runs (any OS), else installed on Linux.\n" +
			"Then installs or upgrades tiny's\n" +
			"runtime and the director in the namespace (default " + upNamespace + ") and opens the board.\n" +
			"Safe to re-run: each step leaves what is already there.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			out := cmd.OutOrStdout()
			var contexts []string
			if g.kubeContext == "" {
				contexts, _, _ = kubeContexts() // no kubeconfig at all is the same as none
			} else {
				contexts = []string{g.kubeContext}
			}
			made, err := ensureCluster(ctx, realK3s(local), contexts, yes, isTerminal(), cmd.InOrStdin(), out)
			if err != nil {
				return err
			}
			if made != "" {
				g.kubeContext = made
			}
			if g.namespace == "" && (made != "" || !isTerminal()) {
				g.namespace = upNamespace
			}
			if err := g.chooseTarget(ctx); err != nil {
				return err
			}
			cfg, ns, err := g.kubeConfig()
			if err != nil {
				return err
			}
			// The director starts runs only behind tiny's egress proxy. On
			// k3s a pod's policy lands a moment after it starts, so sessions
			// there wait for the fence before the agent runs.
			topts := tinyruntime.Options{EgressProxy: true}
			if kc, err := kubernetes.NewForConfig(cfg); err == nil {
				if v, err := kc.Discovery().ServerVersion(); err == nil {
					topts.FenceWait = isK3s(v.GitVersion)
				}
			}
			if err := tinyruntime.Init(ctx, cfg, ns, topts); err != nil {
				return fmt.Errorf("install tiny: %w", err)
			}
			fmt.Fprintln(out, "✓ tiny runtime, egress through its proxy")
			port := o.port
			if port == 0 {
				port = sshDefaultPort // the tunnel needs a port known in advance
			}
			if hint := sshHint(os.Getenv, port); hint != "" {
				o.port, o.noOpen = port, true
				fmt.Fprintln(out, hint)
			}
			return g.serveBoard(ctx, cfg, ns, o, out)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "start k3s without asking when there is no cluster")
	cmd.Flags().StringVar(&local, "local", "", "how to start k3s when there is no cluster: docker or k3s (Linux installer); default docker when it runs")
	cmd.Flags().IntVar(&o.port, "port", 0, "local port for the board (0 = any free port; 8790 over SSH)")
	cmd.Flags().BoolVar(&o.noOpen, "no-open", false, "don't open a browser")
	return cmd
}
