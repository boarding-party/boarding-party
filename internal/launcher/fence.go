package launcher

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/net/publicsuffix"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	tinySettings   = "tiny-settings"
	egressAllowCM  = "tiny-egress-allow"
	egressAllowKey = "hosts"
)

// CheckFence reports whether runs in the namespace are fenced: tiny's egress
// proxy is on and its allow-list admits no whole public suffix (".com",
// ".co.uk"), which would let agents reach any site under it.
func (t *Tiny) CheckFence(ctx context.Context) error {
	cm := &corev1.ConfigMap{}
	err := t.Client.Get(ctx, client.ObjectKey{Namespace: t.Namespace, Name: tinySettings}, cm)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("read configmap %s: %w", tinySettings, err)
	}
	if err != nil || cm.Data["egressProxy"] != "true" {
		return fmt.Errorf("tiny's egress proxy is off, so agents could reach any host (set egressProxy: \"true\" in configmap %s)", tinySettings)
	}
	al := &corev1.ConfigMap{}
	if err := t.Client.Get(ctx, client.ObjectKey{Namespace: t.Namespace, Name: egressAllowCM}, al); err != nil {
		return fmt.Errorf("can't read allow-list %s: %w", egressAllowCM, err)
	}
	if bad := wideEntries(al.Data[egressAllowKey]); len(bad) > 0 {
		return fmt.Errorf("allow-list %s admits whole domains: %s", egressAllowCM, strings.Join(bad, ", "))
	}
	return nil
}

// wideEntries returns the allow-list lines that match every host under a
// public suffix. Parsing follows tiny's egress proxy: one host per line,
// anything after # ignored, a leading dot admits all subdomains.
func wideEntries(list string) []string {
	var bad []string
	for _, raw := range strings.Split(list, "\n") {
		if i := strings.IndexByte(raw, '#'); i >= 0 {
			raw = raw[:i]
		}
		h := strings.ToLower(strings.TrimSpace(raw))
		if h == "" {
			continue
		}
		if h == "*" || h == "." {
			bad = append(bad, h)
			continue
		}
		if !strings.HasPrefix(h, ".") {
			continue
		}
		if suffix, _ := publicsuffix.PublicSuffix(h[1:]); suffix == h[1:] {
			bad = append(bad, h)
		}
	}
	return bad
}
