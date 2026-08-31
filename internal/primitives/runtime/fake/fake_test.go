package fake

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	rt "github.com/minicloud/platform/internal/primitives/runtime"
)

func TestLifecycle(t *testing.T) {
	r := New()
	ctx := context.Background()
	if err := r.Pull(ctx, "nginx:alpine"); err != nil {
		t.Fatal(err)
	}
	c, err := r.Create(ctx, rt.Spec{ID: "c1", Name: "web", Image: "nginx:alpine", Command: []string{"nginx"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != rt.StatusCreating {
		t.Fatalf("status=%s", c.Status)
	}
	if err := r.Start(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	r.AppendLog("c1", "started nginx")
	logs, err := r.Logs(ctx, "c1", false, 100)
	if err != nil {
		t.Fatal(err)
	}
	buf := &bytes.Buffer{}
	_, _ = buf.ReadFrom(logs)
	if !strings.Contains(buf.String(), "started nginx") {
		t.Fatalf("logs: %q", buf.String())
	}
	if err := r.Stop(ctx, "c1", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(ctx, "c1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, "c1"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestStats(t *testing.T) {
	r := New()
	ctx := context.Background()
	r.Create(ctx, rt.Spec{ID: "c", Image: "x"})
	r.Start(ctx, "c")
	time.Sleep(250 * time.Millisecond)
	s, err := r.Stats(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUUsageMilli == 0 && s.MemUsageBytes == 0 {
		t.Fatalf("expected non-zero stats, got %+v", s)
	}
}
