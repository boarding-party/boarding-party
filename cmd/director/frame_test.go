package main

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestProjectFrameFollowsConfigMap(t *testing.T) {
	sc := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(sc); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(sc).Build()
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	f := &frameSource{c: c, ns: "ns", ttl: 30 * time.Second, now: func() time.Time { return now }}
	ctx := context.Background()
	if got := f.Get(ctx); got != "'none'" {
		t.Fatalf("no ConfigMap: %q", got)
	}
	// The cloud writes bp-project after the director started.
	if err := c.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "bp-project", Namespace: "ns"},
		Data: map[string]string{"frameAncestors": "https://cloud.test"}}); err != nil {
		t.Fatal(err)
	}
	if got := f.Get(ctx); got != "'none'" {
		t.Fatalf("within the cache: %q", got)
	}
	now = now.Add(31 * time.Second)
	if got := f.Get(ctx); got != "https://cloud.test" {
		t.Fatalf("after the cache: %q", got)
	}
}

func TestProjectStagingURLFromConfigMap(t *testing.T) {
	sc := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(sc); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(sc).Build()
	f := &frameSource{c: c, ns: "ns", ttl: 0, now: time.Now}
	ctx := context.Background()
	if got := f.StagingURL(ctx); got != "" {
		t.Fatalf("no ConfigMap: %q", got)
	}
	if err := c.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "bp-project", Namespace: "ns"},
		Data: map[string]string{"frameAncestors": "https://cloud.test", "stagingURL": " https://staging.example.com "}}); err != nil {
		t.Fatal(err)
	}
	if got := f.StagingURL(ctx); got != "https://staging.example.com" {
		t.Fatalf("staging URL %q", got)
	}
	if got := f.Get(ctx); got != "https://cloud.test" {
		t.Fatalf("frame %q", got)
	}
}
