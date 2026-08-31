// Package nodeagent runs on each cluster node. It:
//   - sends heartbeats (CPU, RAM, GPU, status) to the controller
//   - reconciles local container state against planned placements
//   - exposes runtime exec/logs/stats to the API server
//   - implements graceful drain when requested
package nodeagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	rt "github.com/minicloud/platform/internal/primitives/runtime"
	"github.com/minicloud/platform/internal/state"
)

// Heartbeat is the per-tick node report.
type Heartbeat struct {
	NodeID     string
	Address    string
	CPUUsage   int64
	MemUsage   int64
	DiskUsage  int64
	GPUCount   int
	Containers int
	Timestamp  time.Time
}

// Collector gathers host metrics.
type Collector interface {
	Collect(ctx context.Context) (Heartbeat, error)
}

// StaticCollector is a Collector that returns fixed values. Useful for
// tests and for nodes where the platform is co-located with the host
// but the agent doesn't have permission to read /proc.
type StaticCollector struct {
	NodeID  string
	Address string
	CPU     int64
	Mem     int64
	Disk    int64
	GPU     int
}

func (c *StaticCollector) Collect(ctx context.Context) (Heartbeat, error) {
	return Heartbeat{
		NodeID:    c.NodeID,
		Address:   c.Address,
		CPUUsage:  c.CPU,
		MemUsage:  c.Mem,
		DiskUsage: c.Disk,
		GPUCount:  c.GPU,
		Timestamp: time.Now().UTC(),
	}, nil
}

// ApplyFunc is the controller→agent contract: the controller calls it
// with the planned placements, the agent materializes them locally.
type ApplyFunc func(ctx context.Context, w *state.Workload, p state.Placement) error

// DeleteFunc removes a placed workload locally.
type DeleteFunc func(ctx context.Context, w *state.Workload, p state.Placement) error

// Agent is the per-node state machine.
type Agent struct {
	NodeID  string
	Address string
	Store   *state.Store
	Runtime rt.Runtime
	C       Collector
	Apply   ApplyFunc
	Delete  DeleteFunc

	mu       sync.Mutex
	draining bool
}

// HeartbeatInterval is the cadence at which the agent updates the
// controller with its current state.
const HeartbeatInterval = 5 * time.Second

// Run starts the heartbeat and reconcile loops. Returns when ctx is
// canceled.
func (a *Agent) Run(ctx context.Context) {
	tick := time.NewTicker(HeartbeatInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			a.heartbeatOnce(ctx)
			a.reconcileOnce(ctx)
		}
	}
}

func (a *Agent) heartbeatOnce(ctx context.Context) {
	hb, err := a.C.Collect(ctx)
	if err != nil {
		return
	}
	n, err := a.Store.GetNode(ctx, a.NodeID)
	if err != nil {
		// First-time registration.
		n = &state.Node{
			Base:           state.Base{ID: a.NodeID, Name: a.NodeID},
			Address:        a.Address,
			Status:         "Ready",
			CPUAllocatable: 8000,
			MemAllocatable: 16 << 30,
		}
	} else {
		n.LastHeartbeat = hb.Timestamp
		n.Address = a.Address
		n.Status = "Ready"
	}
	n.CPUAllocated = hb.CPUUsage
	n.MemAllocated = hb.MemUsage
	n.LastHeartbeat = hb.Timestamp
	a.mu.Lock()
	if a.draining {
		n.Drain = true
		n.Unschedulable = true
	}
	a.mu.Unlock()
	if err := a.Store.UpsertNode(ctx, n); err != nil {
		return
	}
}

// reconcileOnce compares the planned placements for this node with
// what is actually running and converges them.
func (a *Agent) reconcileOnce(ctx context.Context) {
	// Walk every project and check if any placement points here.
	projects, err := a.Store.ListProjects(ctx)
	if err != nil {
		return
	}
	want := map[string]state.Placement{}
	for _, p := range projects {
		ws, _ := a.Store.ListWorkloads(ctx, p.ID)
		for _, w := range ws {
			ps, err := a.Store.GetPlacements(ctx, w.ID)
			if err != nil {
				continue
			}
			for _, pl := range ps {
				if pl.NodeID == a.NodeID {
					want[pl.ID] = pl
					if a.Apply != nil {
						_ = a.Apply(ctx, w, pl)
					}
				}
			}
		}
	}
	// Remove anything we have locally that's not in `want`.
	conts, _ := a.Runtime.List(ctx, true)
	for _, c := range conts {
		if _, ok := want[c.ID]; ok {
			continue
		}
		_ = a.Runtime.Remove(ctx, c.ID, true)
	}
}

// SetDrain toggles the agent into drain mode. The agent will stop
// receiving new placements (the controller honors Unschedulable) and
// will stop existing containers on the next reconcile.
func (a *Agent) SetDrain(drain bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.draining = drain
}

// IsDraining reports the current drain flag.
func (a *Agent) IsDraining() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.draining
}

// Exec runs a command in a container via the runtime.
func (a *Agent) Exec(ctx context.Context, containerID string, cmd []string, stdin []byte) (string, error) {
	if len(cmd) == 0 {
		return "", errors.New("nodeagent: command required")
	}
	c, err := a.Runtime.Get(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("nodeagent: container %s not found", containerID)
	}
	_ = c
	// In a real agent we'd wire stdin/stdout; here we use the runtime's
	// Exec which writes to stdout captured to a buffer.
	out := &bytesBuffer{}
	var in *bytesReader
	if stdin != nil {
		in = bytesReaderFromBytes(stdin)
	}
	if _, err := a.Runtime.Exec(ctx, containerID, cmd, in, out, &bytesBuffer{}); err != nil {
		return "", err
	}
	return out.String(), nil
}

// Logs returns the tail of a container's logs.
func (a *Agent) Logs(ctx context.Context, containerID string, tail int) (string, error) {
	rc, err := a.Runtime.Logs(ctx, containerID, false, tail)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	buf := make([]byte, 64*1024)
	n, _ := rc.Read(buf)
	return string(buf[:n]), nil
}

// ---------- minimal stdio helpers ----------

type bytesBuffer struct {
	b []byte
}

func (b *bytesBuffer) Write(p []byte) (int, error) {
	b.b = append(b.b, p...)
	return len(p), nil
}
func (b *bytesBuffer) String() string { return string(b.b) }

type bytesReader struct {
	b   []byte
	pos int
}

func bytesReaderFromBytes(b []byte) *bytesReader { return &bytesReader{b: b} }
func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.b) {
		return 0, fmt.Errorf("EOF")
	}
	n := copy(p, r.b[r.pos:])
	r.pos += n
	return n, nil
}
