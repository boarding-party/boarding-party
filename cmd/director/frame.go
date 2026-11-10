package main

import (
	"context"
	"log"
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

// frameSource reads who may frame the board from ConfigMap bp-project at
// request time (cached briefly), so a value the cloud writes after the
// director started takes effect without a restart.
type frameSource struct {
	c   client.Client
	ns  string
	ttl time.Duration
	now func() time.Time

	mu  sync.Mutex
	val string
	at  time.Time
	ok  bool
}

// Get returns a CSP frame-ancestors source list; 'none' when unset or bad.
func (f *frameSource) Get(ctx context.Context) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ok && f.now().Sub(f.at) < f.ttl {
		return f.val
	}
	var cm corev1.ConfigMap
	err := f.c.Get(ctx, client.ObjectKey{Namespace: f.ns, Name: install.ProjectConfigMap}, &cm)
	switch {
	case err == nil:
		f.val = board.FrameAncestors(cm.Data["frameAncestors"])
	case apierrors.IsNotFound(err):
		f.val = "'none'"
	default:
		log.Printf("director: read %s: %v", install.ProjectConfigMap, err)
		if !f.ok {
			f.val = "'none'"
		}
	}
	f.at, f.ok = f.now(), true
	return f.val
}
