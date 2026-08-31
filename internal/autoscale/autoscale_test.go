package autoscale

import (
	"context"
	"path/filepath"
	"testing"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/primitives/obs"
	"github.com/minicloud/platform/internal/state"
)

func newSetup(t *testing.T) (*Controller, *state.Store, *obs.Metrics) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	m := obs.NewMetrics()
	c := New(st, m)
	return c, st, m
}

func TestPolicyRegisterAndRemove(t *testing.T) {
	c, _, _ := newSetup(t)
	c.SetPolicy("p", "w", Policy{Min: 1, Max: 5, TargetCPU: 50})
	if len(c.specs) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(c.specs))
	}
	c.RemovePolicy("p", "w")
	if len(c.specs) != 0 {
		t.Fatalf("expected 0 policies, got %d", len(c.specs))
	}
}

func TestScaleUp(t *testing.T) {
	c, st, m := newSetup(t)
	ctx := context.Background()
	// create workload with 1 replica
	st.CreateProject(ctx, &state.Project{Base: state.Base{ID: "p"}})
	w := &state.Workload{Base: state.Base{ID: "w", ProjectID: "p"}, Replicas: 1, CPUMillicores: 100, MemoryBytes: 100}
	st.CreateWorkload(ctx, w)
	// emit high CPU
	m.Set("workload_cpu_p_w", 95, map[string]string{"replica": "0"})
	c.SetPolicy("p", "w", Policy{Min: 1, Max: 5, TargetCPU: 50, ScaleUpBy: 2})
	c.evaluate(ctx)
	got, _ := st.GetWorkload(ctx, "p", "w")
	if got.Replicas != 3 {
		t.Fatalf("expected 3, got %d", got.Replicas)
	}
}

func TestScaleDown(t *testing.T) {
	c, st, m := newSetup(t)
	ctx := context.Background()
	st.CreateProject(ctx, &state.Project{Base: state.Base{ID: "p"}})
	w := &state.Workload{Base: state.Base{ID: "w", ProjectID: "p"}, Replicas: 5, CPUMillicores: 100, MemoryBytes: 100}
	st.CreateWorkload(ctx, w)
	m.Set("workload_cpu_p_w", 5, map[string]string{"replica": "0"})
	c.SetPolicy("p", "w", Policy{Min: 1, Max: 5, TargetCPU: 50, ScaleDownBy: 2})
	c.evaluate(ctx)
	got, _ := st.GetWorkload(ctx, "p", "w")
	if got.Replicas != 3 {
		t.Fatalf("expected 3, got %d", got.Replicas)
	}
}
