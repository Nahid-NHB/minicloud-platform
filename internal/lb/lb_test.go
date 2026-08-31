package lb

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/state"
)

func TestLBPickFallsBack(t *testing.T) {
	b := &Balancer{cfg: Config{Strategy: StrategyRoundRobin}}
	b.mu.Lock()
	b.backends = []Backend{{Addr: "10.0.0.1:80", Healthy: true}}
	b.mu.Unlock()
	be, err := b.pick()
	if err != nil || be == nil {
		t.Fatalf("err=%v be=%v", err, be)
	}
}

func TestLBNoBackend(t *testing.T) {
	b := &Balancer{cfg: Config{Strategy: StrategyRoundRobin}}
	if _, err := b.pick(); err == nil {
		t.Fatal("expected error")
	}
}

// end-to-end smoke test: stand up an echo backend, an LB, dial the LB,
// and check that the byte round-trips.
func TestLBEndToEnd(t *testing.T) {
	// Echo backend.
	bl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer bl.Close()
	var hits int32
	go func() {
		for {
			c, err := bl.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&hits, 1)
			go func(c net.Conn) {
				io.Copy(c, c)
				c.Close()
			}(c)
		}
	}()
	addr := bl.Addr().String()
	b := &Balancer{cfg: Config{Strategy: StrategyRoundRobin}}
	b.mu.Lock()
	b.backends = []Backend{{Addr: addr, Healthy: true}}
	b.mu.Unlock()

	// LB listener.
	ll, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ll.Close()
	go b.Serve(context.Background(), ll)

	// Connect via LB.
	c, err := net.Dial("tcp", ll.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("got %q", buf)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("no hits")
	}
}

// sanity: refresh reads placements from the store.
func TestLBRefresh(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	p := &state.Project{Base: state.Base{ID: "p"}}
	st.CreateProject(context.Background(), p)
	w := &state.Workload{Base: state.Base{ID: "w1", ProjectID: "p"}, Replicas: 1, CPUMillicores: 1, MemoryBytes: 1}
	st.CreateWorkload(context.Background(), w)
	st.SetPlacements(context.Background(), "w1", []state.Placement{
		{Base: state.Base{ID: "x"}, WorkloadID: "w1", NodeID: "n1", ReplicaIdx: 0, Status: "Ready"},
	})
	svc := &state.Service{Base: state.Base{ID: "s1", ProjectID: "p"}, WorkloadID: "w1", Port: 80}
	st.CreateService(context.Background(), svc)

	b := NewBalancer(Config{Store: st, Service: "s1"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go b.Run(ctx)
	time.Sleep(2500 * time.Millisecond)
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.backends) == 0 {
		t.Fatalf("no backends; bk=%v", b)
	}
}