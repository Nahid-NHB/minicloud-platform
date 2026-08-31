// Package autoscale implements a horizontal-pod-autoscaler style
// controller that scales a Workload's replica count up or down based
// on observed CPU utilization reported by the metrics primitive.
//
// The controller is stateless: each tick it reads the latest metric
// samples, evaluates per-workload policies, and patches the workload's
// desired replica count in the state store. Workload placement is then
// done by the regular scheduler/controller loop.
package autoscale

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/minicloud/platform/internal/primitives/obs"
	"github.com/minicloud/platform/internal/state"
)

// Policy describes a single autoscaling rule attached to a workload.
type Policy struct {
	Min         int
	Max         int
	TargetCPU   float64 // target average CPU utilization percentage
	ScaleUpBy   int
	ScaleDownBy int
	Cooldown    time.Duration
}

// Spec binds a Policy to a workload.
type Spec struct {
	ProjectID  string
	WorkloadID string
	Policy     Policy
	UpdatedAt  time.Time
}

// Controller runs the autoscaler loop.
type Controller struct {
	store   *state.Store
	metrics *obs.Metrics

	mu       sync.Mutex
	specs    map[string]*Spec
	lastAton map[string]time.Time
}

// New constructs a Controller.
func New(store *state.Store, metrics *obs.Metrics) *Controller {
	return &Controller{
		store:    store,
		metrics:  metrics,
		specs:    map[string]*Spec{},
		lastAton: map[string]time.Time{},
	}
}

// SetPolicy registers or replaces an autoscaling policy.
func (c *Controller) SetPolicy(projectID, workloadID string, p Policy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.specs[projectID+"/"+workloadID] = &Spec{
		ProjectID:  projectID,
		WorkloadID: workloadID,
		Policy:     p,
		UpdatedAt:  time.Now(),
	}
}

// RemovePolicy deletes a previously registered policy.
func (c *Controller) RemovePolicy(projectID, workloadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.specs, projectID+"/"+workloadID)
}

// Run loops until ctx is canceled, evaluating all known policies on
// every Tick.
func (c *Controller) Run(ctx context.Context, tick time.Duration) {
	if tick == 0 {
		tick = 15 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.evaluate(ctx)
		}
	}
}

// evaluate runs one autoscaler pass.
func (c *Controller) evaluate(ctx context.Context) {
	c.mu.Lock()
	specs := make([]*Spec, 0, len(c.specs))
	for _, s := range c.specs {
		specs = append(specs, s)
	}
	c.mu.Unlock()
	for _, s := range specs {
		if err := c.evaluateOne(ctx, s); err != nil {
			fmt.Printf("autoscale: %s: %v\n", s.WorkloadID, err)
		}
	}
}

func (c *Controller) evaluateOne(ctx context.Context, s *Spec) error {
	w, err := c.store.GetWorkload(ctx, s.ProjectID, s.WorkloadID)
	if err != nil {
		return nil
	}
	avg := c.metrics.AvgCPU(workloadMetricKey(w.ProjectID, w.ID))
	want := int(w.Replicas)
	if avg > s.Policy.TargetCPU && int(w.Replicas) < s.Policy.Max {
		want += s.Policy.ScaleUpBy
		if want > s.Policy.Max {
			want = s.Policy.Max
		}
	}
	if avg < s.Policy.TargetCPU/2 && int(w.Replicas) > s.Policy.Min {
		want -= s.Policy.ScaleDownBy
		if want < s.Policy.Min {
			want = s.Policy.Min
		}
	}
	c.mu.Lock()
	last := c.lastAton[w.ProjectID+"/"+w.ID]
	c.mu.Unlock()
	if time.Since(last) < s.Policy.Cooldown {
		return nil
	}
	if want == int(w.Replicas) {
		return nil
	}
	w.Replicas = int32(want)
	if err := c.store.UpdateWorkload(ctx, w); err != nil {
		return err
	}
	c.mu.Lock()
	c.lastAton[w.ProjectID+"/"+w.ID] = time.Now()
	c.mu.Unlock()
	return nil
}

// workloadMetricKey returns the metrics key for a workload.
func workloadMetricKey(projectID, workloadID string) string {
	return "workload_cpu_" + projectID + "_" + workloadID
}
