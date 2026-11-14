package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
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
	k3sInstall     = "curl -sfL https://get.k3s.io | sh -"
	k3sKubeconfig  = "/etc/rancher/k3s/k3s.yaml"
	k3sContext     = "k3s"
	upNamespace    = "boardingparty"
	sshDefaultPort = 8790
)

// k3sDeps is how up gets a cluster on a machine that has none; tests swap
// every part so nothing is installed.
type k3sDeps struct {
	goos           string
	kubeconfigPath string
	install        func(ctx context.Context) error
	readKubeconfig func(ctx context.Context) ([]byte, error)
	waitReady      func(ctx context.Context, kubeContext string) error
}

func realK3s() k3sDeps {
	return k3sDeps{
		goos:           runtime.GOOS,
		kubeconfigPath: clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename(),
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
		waitReady: waitNodeReady,
	}
}

// ensureCluster makes sure there is a cluster to install on. With
// kubeconfig contexts it does nothing and returns "". With none it offers
// k3s on this machine (Linux only), installs it on yes, adds it to the
// kubeconfig and returns its context name.
func ensureCluster(ctx context.Context, d k3sDeps, contexts []string, yes, interactive bool, in io.Reader, out io.Writer) (string, error) {
	if len(contexts) > 0 {
		return "", nil
	}
	if d.goos != "linux" {
		return "", errors.New("no Kubernetes cluster found, and up installs one (k3s) on Linux only: " +
			"run boardingparty up on a Linux server with 4 GB or more, or point kubectl at a cluster")
	}
	fmt.Fprintf(out, "No Kubernetes cluster found. boardingparty runs on k3s, a one-machine cluster.\n"+
		"Installing it runs k3s's official installer as root:\n  %s\n", k3sInstall)
	if !yes {
		if !interactive {
			return "", errors.New("not a terminal, so cannot ask before installing k3s: pass --yes")
		}
		fmt.Fprint(out, "Install k3s on this machine? [Y/n] ")
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "" && a != "y" && a != "yes" {
			return "", errors.New("stopped: no cluster to install on")
		}
	}
	if err := d.install(ctx); err != nil {
		return "", fmt.Errorf("install k3s: %w", err)
	}
	raw, err := d.readKubeconfig(ctx)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", k3sKubeconfig, err)
	}
	if err := mergeK3s(d.kubeconfigPath, raw); err != nil {
		return "", fmt.Errorf("add k3s to %s: %w", d.kubeconfigPath, err)
	}
	fmt.Fprintf(out, "✓ k3s installed; kubeconfig context %q in %s\n", k3sContext, d.kubeconfigPath)
	if err := d.waitReady(ctx, k3sContext); err != nil {
		return "", err
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
			return fmt.Errorf("k3s node not ready after 3 minutes (see `sudo journalctl -u k3s`)")
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
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Install everything (k3s if there is no cluster, tiny, the director) and open the board",
		Long: "Uses a kubeconfig context if there is one (asking which on a terminal). With none,\n" +
			"offers to install k3s on this machine (Linux). Then installs or upgrades tiny's\n" +
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
			made, err := ensureCluster(ctx, realK3s(), contexts, yes, isTerminal(), cmd.InOrStdin(), out)
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
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "install k3s without asking when there is no cluster")
	cmd.Flags().IntVar(&o.port, "port", 0, "local port for the board (0 = any free port; 8790 over SSH)")
	cmd.Flags().BoolVar(&o.noOpen, "no-open", false, "don't open a browser")
	return cmd
}
