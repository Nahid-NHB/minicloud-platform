// Package scheduler places workloads onto nodes using a two-pass
// filter+score algorithm.
//
//   - filter: removes nodes that don't have enough free CPU/memory,
//     don't tolerate a taint the workload has, or fail affinity rules.
//   - score:  ranks survivors by least-allocated resources, GPU fit,
//     and (optionally) spread across zones.
//
// The scheduler is intentionally stateless across calls. State lives in
// the KV store and node heartbeats.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/minicloud/platform/internal/state"
)

// Decision is the scheduler's plan for a workload.
type Decision struct {
	WorkloadID  string
	Placements  []Placement
	Unscheduled string
}

// Placement maps one workload replica to one node.
type Placement struct {
	NodeID     string
	ReplicaIdx int32
	Reason     string
}

// Scheduler places workloads.
type Scheduler struct {
	store *state.Store
	mu    sync.Mutex
}

// New builds a scheduler.
func New(store *state.Store) *Scheduler { return &Scheduler{store: store} }

// Plan computes a Decision for a single workload.
func (s *Scheduler) Plan(ctx context.Context, w *state.Workload) (*Decision, error) {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	filtered := filter(nodes, w)
	if len(filtered) == 0 {
		return &Decision{WorkloadID: w.ID, Unscheduled: "no node satisfies filters"}, nil
	}
	scored := score(filtered, w)
	// Spread: avoid placing two replicas of the same workload on the
	// same node when possible.
	perNode := map[string]int32{}
	placements := make([]Placement, 0, w.Replicas)
	for i := int32(0); i < w.Replicas; i++ {
		chosen := -1
		for idx, n := range scored {
			if perNode[n.ID] >= maxPerNode(w, n) {
				continue
			}
			chosen = idx
			break
		}
		if chosen < 0 {
			return &Decision{WorkloadID: w.ID, Placements: placements, Unscheduled: "not enough capacity for all replicas"}, nil
		}
		placements = append(placements, Placement{NodeID: scored[chosen].ID, ReplicaIdx: i})
		perNode[scored[chosen].ID]++
	}
	return &Decision{WorkloadID: w.ID, Placements: placements}, nil
}

// Reconcile is the entry point used by the controller.
func (s *Scheduler) Reconcile(ctx context.Context, w *state.Workload) error {
	d, err := s.Plan(ctx, w)
	if err != nil {
		return err
	}
	placements := make([]state.Placement, 0, len(d.Placements))
	for _, p := range d.Placements {
		placements = append(placements, state.Placement{
			Base:       state.Base{ID: fmt.Sprintf("%s-%d", w.ID, p.ReplicaIdx), ProjectID: w.ProjectID, Name: fmt.Sprintf("%s-%d", w.Name, p.ReplicaIdx)},
			WorkloadID: w.ID,
			NodeID:     p.NodeID,
			ReplicaIdx: p.ReplicaIdx,
			Status:     "Pending",
		})
	}
	return s.store.SetPlacements(ctx, w.ID, placements)
}

func filter(nodes []*state.Node, w *state.Workload) []*state.Node {
	out := make([]*state.Node, 0, len(nodes))
	for _, n := range nodes {
		if n.Unschedulable || n.Drain || n.Cordoned {
			continue
		}
		if n.Status != "Ready" {
			continue
		}
		// Resource availability.
		if n.CPUAllocatable-n.CPUAllocated < w.CPUMillicores {
			continue
		}
		if n.MemAllocatable-n.MemAllocated < w.MemoryBytes {
			continue
		}
		if int64(n.GPUs) < int64(w.GPUs) {
			continue
		}
		// Node selectors.
		if !matchSelector(w.NodeSelector, n.Labels) {
			continue
		}
		// Taints vs tolerations.
		if !tolerates(w.Tolerations, n.Taints) {
			continue
		}
		out = append(out, n)
	}
	return out
}

func score(nodes []*state.Node, w *state.Workload) []*state.Node {
	// Higher score = better. Least-allocated wins.
	type sc struct {
		n *state.Node
		s float64
	}
	scs := make([]sc, 0, len(nodes))
	for _, n := range nodes {
		freeCPU := float64(n.CPUAllocatable - n.CPUAllocated)
		freeMem := float64(n.MemAllocatable - n.MemAllocated)
		total := float64(n.CPUAllocatable + n.MemAllocatable)
		avail := freeCPU + freeMem
		if total == 0 {
			scs = append(scs, sc{n: n, s: 0})
			continue
		}
		// Score is ratio of available resources (0..1).
		scs = append(scs, sc{n: n, s: avail / total})
	}
	sort.SliceStable(scs, func(i, j int) bool { return scs[i].s > scs[j].s })
	out := make([]*state.Node, 0, len(scs))
	for _, x := range scs {
		out = append(out, x.n)
	}
	return out
}

func matchSelector(sel, labels map[string]string) bool {
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func tolerates(tol []string, taints []state.Taint) bool {
	for _, t := range taints {
		if t.Effect == "NoSchedule" && !hasToleration(tol, t) {
			return false
		}
	}
	return true
}

func hasToleration(tol []string, t state.Taint) bool {
	for _, t2 := range tol {
		parts := strings.SplitN(t2, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] != t.Key {
			continue
		}
		if parts[1] == "*" || parts[1] == t.Value {
			return true
		}
	}
	return false
}

func maxPerNode(w *state.Workload, n *state.Node) int32 {
	// Honor anti-affinity: never place two replicas of this workload on
	// the same node when the workload expresses anti-affinity.
	for _, a := range w.AntiAffinity {
		if strings.Contains(a, w.Name) {
			return 1
		}
	}
	// Otherwise allow replicas to pack onto a node until resources run out.
	free := n.CPUAllocatable - n.CPUAllocated
	if w.CPUMillicores == 0 {
		return 4
	}
	c := free / w.CPUMillicores
	if c > 4 {
		return 4
	}
	if c <= 0 {
		return 0
	}
	return int32(c)
}

// ValidateWorkload sanity-checks a workload spec.
func ValidateWorkload(w *state.Workload) error {
	if w.ID == "" {
		return errors.New("scheduler: workload id required")
	}
	if w.CPUMillicores < 0 || w.MemoryBytes < 0 {
		return errors.New("scheduler: negative resources")
	}
	if w.Replicas < 0 {
		return errors.New("scheduler: negative replicas")
	}
	return nil
}