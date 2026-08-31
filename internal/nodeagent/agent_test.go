package nodeagent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	rt "github.com/minicloud/platform/internal/primitives/runtime"
	rtfake "github.com/minicloud/platform/internal/primitives/runtime/fake"
	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/state"
)

func newAgent(t *testing.T) (*Agent, *state.Store, *rtfake.Fake) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{
		NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	r := rtfake.New()
	a := &Agent{
		NodeID:  "n1",
		Address: "127.0.0.1:9000",
		Store:   st,
		Runtime: r,
		C:       &StaticCollector{NodeID: "n1", Address: "127.0.0.1:9000"},
	}
	return a, st, r
}

func TestHeartbeatRegistersNode(t *testing.T) {
	a, st, _ := newAgent(t)
	a.heartbeatOnce(context.Background())
	n, err := st.GetNode(context.Background(), "n1")
	if err != nil {
		t.Fatal(err)
	}
	if n.Status != "Ready" {
		t.Fatalf("status=%s", n.Status)
	}
	if n.LastHeartbeat.IsZero() {
		t.Fatal("expected heartbeat timestamp")
	}
}

func TestDrainFlagPropagates(t *testing.T) {
	a, st, _ := newAgent(t)
	a.SetDrain(true)
	a.heartbeatOnce(context.Background())
	n, _ := st.GetNode(context.Background(), "n1")
	if !n.Drain || !n.Unschedulable {
		t.Fatalf("drain not propagated: %+v", n)
	}
}

func TestReconcileRemovesStale(t *testing.T) {
	a, st, r := newAgent(t)
	// Pre-create an orphan container on the runtime.
	_, _ = r.Create(context.Background(), rt.Spec{ID: "orphan", Image: "x"})
	_ = r.Start(context.Background(), "orphan")
	// Add a project so reconcile iterates.
	if err := st.CreateProject(context.Background(), &state.Project{Base: state.Base{ID: "p"}}); err != nil {
		t.Fatal(err)
	}
	a.reconcileOnce(context.Background())
	if _, err := r.Get(context.Background(), "orphan"); err == nil {
		t.Fatal("orphan should have been removed")
	}
	// Avoid an unused import warning.
	_ = time.Second
}