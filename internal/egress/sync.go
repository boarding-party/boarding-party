package egress

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The allow-list lines between these markers belong to boardingparty and are
// rewritten on every sync; everything else in the list is left alone.
const (
	blockBegin = "# boardingparty: the project's site and extra hosts. Rewritten when the project is saved; edit them in boardingparty."
	blockEnd   = "# boardingparty: end"
)

// Sync puts hosts on the allow-list as one managed block, replacing the
// previous block. Lines outside it (the model API, anything an operator
// added) stay as they are.
func (f *Fence) Sync(ctx context.Context, hosts []string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm := &corev1.ConfigMap{}
		if err := f.Client.Get(ctx, client.ObjectKey{Namespace: f.Namespace, Name: egressAllowCM}, cm); err != nil {
			return fmt.Errorf("read allow-list %s: %w", egressAllowCM, err)
		}
		next := withBlock(cm.Data[egressAllowKey], hosts)
		if next == cm.Data[egressAllowKey] {
			return nil
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[egressAllowKey] = next
		if err := f.Client.Update(ctx, cm); err != nil {
			return fmt.Errorf("update allow-list %s: %w", egressAllowCM, err)
		}
		return nil
	})
}

// withBlock returns list with its managed block replaced by one listing hosts
// (or removed when hosts is empty). An unterminated block runs to the end.
func withBlock(list string, hosts []string) string {
	var kept []string
	in := false
	for _, line := range strings.Split(strings.TrimRight(list, "\n"), "\n") {
		switch {
		case strings.TrimSpace(line) == blockBegin:
			in = true
		case in && strings.TrimSpace(line) == blockEnd:
			in = false
		case !in:
			kept = append(kept, line)
		}
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	if len(hosts) > 0 {
		if len(kept) > 0 {
			kept = append(kept, "")
		}
		kept = append(kept, blockBegin)
		kept = append(kept, hosts...)
		kept = append(kept, blockEnd)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n"
}
