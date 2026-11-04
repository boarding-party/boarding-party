package egress

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// proxyLabel selects tiny's egress proxy pods (tiny's addons.ProxyName).
const proxyLabel = "app=tiny-egress"

// maxLogBytes bounds how much proxy log one Denied call reads per pod.
const maxLogBytes = 4 << 20

// Denied returns the hosts the proxy refused between since and until, sorted
// and without duplicates. The proxy is shared by the namespace, so this is
// only one run's list while one run happens at a time.
func (f *Fence) Denied(ctx context.Context, since, until time.Time) ([]string, error) {
	if f.Pods == nil {
		return nil, nil
	}
	pods, err := f.Pods.Pods(f.Namespace).List(ctx, metav1.ListOptions{LabelSelector: proxyLabel})
	if err != nil {
		return nil, fmt.Errorf("list egress proxy pods: %w", err)
	}
	seen := map[string]bool{}
	st := metav1.NewTime(since)
	limit := int64(maxLogBytes)
	for _, p := range pods.Items {
		rc, err := f.Pods.Pods(f.Namespace).GetLogs(p.Name, &corev1.PodLogOptions{
			SinceTime: &st, Timestamps: true, LimitBytes: &limit}).Stream(ctx)
		if err != nil {
			return nil, fmt.Errorf("read egress proxy logs: %w", err)
		}
		hosts, err := parseDenied(rc, since, until)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read egress proxy logs: %w", err)
		}
		for _, h := range hosts {
			seen[h] = true
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	slices.Sort(out)
	return out, nil
}

// parseDenied reads timestamped proxy log lines ("<RFC3339> ... egress DENIED
// <method> <host>") and returns the hosts refused within [since, until].
func parseDenied(r io.Reader, since, until time.Time) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		stamp, rest, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || t.Before(since) || (!until.IsZero() && t.After(until)) {
			continue
		}
		_, after, found := strings.Cut(rest, "egress DENIED ")
		if !found {
			continue
		}
		fields := strings.Fields(after)
		if len(fields) < 2 {
			continue
		}
		if h := hostOnly(fields[1]); h != "" {
			out = append(out, h)
		}
	}
	return out, sc.Err()
}

// hostOnly lower-cases a proxy target and drops its port.
func hostOnly(target string) string {
	h := strings.ToLower(strings.TrimSpace(target))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.Trim(h, "[]")
}
