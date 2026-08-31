// Package controller drives the actual cluster toward the desired
// state recorded in the KV store.
//
// One controller instance runs as the leader at any time (elected via
// the leader package). Other instances sit idle and only take over if
// the leader's key expires.
//
// The controller maintains a set of watch streams — one per resource
// kind — and reconciles each change. Reconcile loops are idempotent
// and use the CAS primitive to avoid lost updates.
package controller

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/minicloud/platform/internal/scheduler"
	"github.com/minicloud/platform/internal/state"
	"github.com/minicloud/platform/internal/util/leader"
)

// Notifier is the interface the controller uses to publish events to
// node agents (e.g. "create container", "delete container").
type Notifier interface {
	ApplyWorkload(ctx context.Context, w *state.Workload, p state.Placement) error
	DeleteWorkload(ctx context.Context, w *state.Workload, p state.Placement) error
}

// Controller reconciles desired state with actual state.
type Controller struct {
	store    *state.Store
	sched    *scheduler.Scheduler
	notifier Notifier
	election *leader.Election

	mu      sync.Mutex
	running bool
}

// New builds a controller.
func New(store *state.Store, sched *scheduler.Scheduler, n Notifier, e *leader.Election) *Controller {
	return &Controller{store: store, sched: sched, notifier: n, election: e}
}

// Run blocks until ctx is canceled. The controller reconciles in a
// loop while it is the leader; when leadership is lost it stops until
// it is re-elected.
func (c *Controller) Run(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if c.election == nil || c.election.IsLeader() {
				if err := c.reconcileAll(ctx); err != nil {
					// Best-effort; next tick retries.
				}
			}
		}
	}
}

func (c *Controller) reconcileAll(ctx context.Context) error {
	// Walk every project and reconcile its workloads.
	projects, err := c.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, p := range projects {
		ws, err := c.store.ListWorkloads(ctx, p.ID)
		if err != nil {
			continue
		}
		for _, w := range ws {
			if err := c.reconcileWorkload(ctx, w); err != nil {
				continue
			}
		}
	}
	return nil
}

func (c *Controller) reconcileWorkload(ctx context.Context, w *state.Workload) error {
	// Plan the placements.
	d, err := c.sched.Plan(ctx, w)
	if err != nil {
		return err
	}
	if d.Unscheduled != "" && len(d.Placements) == 0 {
		w.Status.Available = false
		w.Status.Message = d.Unscheduled
		_ = c.store.UpdateWorkload(ctx, w)
		return nil
	}
	// Persist placements.
	placements := make([]state.Placement, 0, len(d.Placements))
	for _, p := range d.Placements {
		placements = append(placements, state.Placement{
			Base:       state.Base{ID: w.ID + "-" + p.NodeID, ProjectID: w.ProjectID, Name: w.Name + "-" + p.NodeID},
			WorkloadID: w.ID,
			NodeID:     p.NodeID,
			ReplicaIdx: p.ReplicaIdx,
			Status:     "Pending",
		})
	}
	if err := c.store.SetPlacements(ctx, w.ID, placements); err != nil {
		return err
	}
	// Notify node agents.
	ready := 0
	for _, p := range placements {
		if c.notifier == nil {
			continue
		}
		if err := c.notifier.ApplyWorkload(ctx, w, p); err == nil {
			ready++
		}
	}
	w.Status.ReadyReplicas = int32(ready)
	w.Status.DesiredReplicas = w.Replicas
	w.Status.Available = int32(ready) >= w.Replicas
	if err := c.store.UpdateWorkload(ctx, w); err != nil {
		return err
	}
	return nil
}

// ReconcileOne is a public entry point that the API server can call
// synchronously (e.g. after a create) to skip waiting for the next
// controller tick.
func (c *Controller) ReconcileOne(ctx context.Context, workloadID string) error {
	// Search every project for the workload.
	projects, err := c.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, p := range projects {
		w, err := c.store.GetWorkload(ctx, p.ID, workloadID)
		if err == nil {
			return c.reconcileWorkload(ctx, w)
		}
	}
	return errors.New("controller: workload not found")
}

// ReconcileAll forces an immediate reconcile pass.
func (c *Controller) ReconcileAll(ctx context.Context) error {
	return c.reconcileAll(ctx)
}
