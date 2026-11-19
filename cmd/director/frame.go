package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/boarding-party/boardingparty/internal/board"
	"github.com/boarding-party/boardingparty/pkg/install"
)

// frameTTL is how long a read of bp-project is reused.
const frameTTL = 30 * time.Second

// frameSource reads the settings the cloud keeps in ConfigMap bp-project
// (who may frame the board, the staging URL) at request time, cached
// briefly, so a value the cloud writes after the director started takes
// effect without a restart.
type frameSource struct {
	c   client.Client
	ns  string
	ttl time.Duration
	now func() time.Time

	mu   sync.Mutex
	data map[string]string
	at   time.Time
	ok   bool
}

// Get returns a CSP frame-ancestors source list; 'none' when unset or bad.
func (f *frameSource) Get(ctx context.Context) string {
	return board.FrameAncestors(f.read(ctx)["frameAncestors"])
}

// StagingURL returns the staging URL the cloud gave the project; "" when unset.
func (f *frameSource) StagingURL(ctx context.Context) string {
	return strings.TrimSpace(f.read(ctx)["stagingURL"])
}

// read returns bp-project's data, reusing a read younger than ttl. A failed
// read keeps the last good data.
func (f *frameSource) read(ctx context.Context) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ok && f.now().Sub(f.at) < f.ttl {
		return f.data
	}
	var cm corev1.ConfigMap
	err := f.c.Get(ctx, client.ObjectKey{Namespace: f.ns, Name: install.ProjectConfigMap}, &cm)
	switch {
	case err == nil:
		f.data = cm.Data
	case apierrors.IsNotFound(err):
		f.data = nil
	default:
		log.Printf("director: read %s: %v", install.ProjectConfigMap, err)
	}
	f.at, f.ok = f.now(), true
	return f.data
}
