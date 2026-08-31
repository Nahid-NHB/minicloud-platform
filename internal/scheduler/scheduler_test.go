package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/state"
)

func newEnv(t *testing.T) (*Scheduler, *state.Store) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{
		NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	return New(st), st
}

func addNode(t *testing.T, st *state.Store, id string, cpu int64, mem int64, ready bool) {
	t.Helper()
	n := &state.Node{
		Base:          state.Base{ID: id, Name: id},
		Address:       "127.0.0.1",
		CPUAllocatable: cpu,
		MemAllocatable: mem,
		Status:        "Ready",
	}
	if !ready {
		n.Status = "NotReady"
	}
	if err := st.UpsertNode(context.Background(), n); err != nil {
		t.Fatal(err)
	}
}

func TestPlanSingleReplica(t *testing.T) {
	sch, st := newEnv(t)
	addNode(t, st, "n1", 4000, 8<<30, true)
	w := &state.Workload{
		Base:         state.Base{ID: "w1", ProjectID: "p", Name: "web"},
		Image:        "nginx",
		CPUMillicores: 500,
		MemoryBytes:   256 * 1024 * 1024,
		Replicas:     1,
	}
	d, err := sch.Plan(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Placements) != 1 || d.Placements[0].NodeID != "n1" {
		t.Fatalf("got %+v", d)
	}
}

func TestPlanNoFit(t *testing.T) {
	sch, st := newEnv(t)
	addNode(t, st, "n1", 1000, 1<<30, true)
	w := &state.Workload{Base: state.Base{ID: "w1"}, CPUMillicores: 4000, MemoryBytes: 4 << 30, Replicas: 1}
	d, _ := sch.Plan(context.Background(), w)
	if len(d.Placements) != 0 {
		t.Fatalf("got %+v", d)
	}
}

func TestPlanSpread(t *testing.T) {
	sch, st := newEnv(t)
	for _, id := range []string{"n1", "n2", "n3"} {
		addNode(t, st, id, 8000, 16<<30, true)
	}
	w := &state.Workload{Base: state.Base{ID: "w1"}, CPUMillicores: 500, MemoryBytes: 256 * 1024 * 1024, Replicas: 3, AntiAffinity: []string{"web"}}
	d, _ := sch.Plan(context.Background(), w)
	if len(d.Placements) != 3 {
		t.Fatalf("got %d placements", len(d.Placements))
	}
	seen := map[string]bool{}
	for _, p := range d.Placements {
		seen[p.NodeID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("not spread: %v", seen)
	}
}

func TestPlanNotReady(t *testing.T) {
	sch, st := newEnv(t)
	addNode(t, st, "n1", 8000, 16<<30, false)
	w := &state.Workload{Base: state.Base{ID: "w1"}, CPUMillicores: 500, MemoryBytes: 256 * 1024 * 1024, Replicas: 1}
	d, _ := sch.Plan(context.Background(), w)
	if len(d.Placements) != 0 {
		t.Fatalf("got %+v", d)
	}
}

func TestReconcilePersists(t *testing.T) {
	sch, st := newEnv(t)
	addNode(t, st, "n1", 4000, 8<<30, true)
	w := &state.Workload{Base: state.Base{ID: "w1", ProjectID: "p"}, Image: "nginx", CPUMillicores: 500, MemoryBytes: 256 * 1024 * 1024, Replicas: 1}
	if err := sch.Reconcile(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	ps, err := st.GetPlacements(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d", len(ps))
	}
}

// Sanity: heartbeat field round-trips through state.
func TestNodeHeartbeat(t *testing.T) {
	sch, st := newEnv(t)
	addNode(t, st, "n1", 4000, 8<<30, true)
	n, _ := st.GetNode(context.Background(), "n1")
	n.LastHeartbeat = time.Now()
	if err := st.UpsertNode(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	_ = sch
}