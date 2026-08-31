package controller

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/scheduler"
	"github.com/minicloud/platform/internal/state"
)

type fakeNotifier struct {
	mu    sync.Mutex
	apply []state.Placement
}

func (f *fakeNotifier) ApplyWorkload(_ context.Context, _ *state.Workload, p state.Placement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.apply = append(f.apply, p)
	return nil
}

func (f *fakeNotifier) DeleteWorkload(_ context.Context, _ *state.Workload, p state.Placement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return nil
}

func newEnv(t *testing.T) (*state.Store, *fakeNotifier) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{
		NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	return st, &fakeNotifier{}
}

func TestReconcileAllNotifiesAgents(t *testing.T) {
	st, n := newEnv(t)
	p := &state.Project{Base: state.Base{ID: "p", Name: "p"}}
	if err := st.CreateProject(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	// Add node.
	st.UpsertNode(context.Background(), &state.Node{
		Base: state.Base{ID: "n1"}, Address: "127.0.0.1",
		CPUAllocatable: 4000, MemAllocatable: 8 << 30, Status: "Ready",
	})
	w := &state.Workload{
		Base: state.Base{ID: "w1", ProjectID: "p", Name: "web"},
		Image: "nginx",
		CPUMillicores: 500, MemoryBytes: 256 * 1024 * 1024, Replicas: 1,
	}
	if err := st.CreateWorkload(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	ctrl := New(st, scheduler.New(st), n, nil)
	if err := ctrl.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.apply) != 1 {
		t.Fatalf("notifier applied=%d", len(n.apply))
	}
	w2, _ := st.GetWorkload(context.Background(), "p", "w1")
	if !w2.Status.Available {
		t.Fatalf("status=%+v", w2.Status)
	}
}
