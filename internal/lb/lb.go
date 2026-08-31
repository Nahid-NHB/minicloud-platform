// Package lb implements a small Layer-4 load balancer for the platform.
//
// It listens on a virtual IP:port and forwards TCP connections to a
// pool of backend addresses using a round-robin or least-connections
// strategy. Backends are pulled from the platform state store (a
// Service resource resolves to its workload's running container IPs).
//
// Health checking is done by TCP connect probes every Interval.
package lb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/minicloud/platform/internal/state"
)

// Strategy is the backend selection algorithm.
type Strategy string

const (
	StrategyRoundRobin      Strategy = "round_robin"
	StrategyLeastConnections Strategy = "least_connections"
)

// Config configures an LB.
type Config struct {
	Store    *state.Store
	Service  string // service id
	Strategy Strategy
}

// Backend is a TCP backend target.
type Backend struct {
	Addr       string
	Weight     int
	Conns      int32
	Healthy    bool
	LastChange time.Time
}

// Balancer is an L4 LB for a single service.
type Balancer struct {
	cfg Config

	mu       sync.Mutex
	backends []Backend
	stop     chan struct{}

	closed atomic.Bool
}

// NewBalancer creates an LB from a service id.
func NewBalancer(cfg Config) *Balancer {
	if cfg.Strategy == "" {
		cfg.Strategy = StrategyRoundRobin
	}
	return &Balancer{cfg: cfg, stop: make(chan struct{})}
}

// Run starts the LB. Returns when ctx is canceled.
func (b *Balancer) Run(ctx context.Context) error {
	if b.cfg.Store == nil {
		return errors.New("lb: store required")
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			b.refresh(ctx)
			b.healthCheck(ctx)
		}
	}
}

// Serve accepts connections on listener and forwards each to a backend.
func (b *Balancer) Serve(ctx context.Context, ln net.Listener) error {
	if b.cfg.Store != nil {
		b.refresh(ctx)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			if b.closed.Load() {
				return nil
			}
			return err
		}
		go b.handle(ctx, c)
	}
}

func (b *Balancer) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	be, err := b.pick()
	if err != nil {
		return
	}
	atomic.AddInt32(&be.Conns, 1)
	defer atomic.AddInt32(&be.Conns, -1)
	d := net.Dialer{Timeout: 5 * time.Second}
	dst, err := d.DialContext(ctx, "tcp", be.Addr)
	if err != nil {
		be.Healthy = false
		return
	}
	defer dst.Close()
	go io.Copy(dst, c)
	io.Copy(c, dst)
}

func (b *Balancer) pick() (*Backend, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	healthy := make([]*Backend, 0, len(b.backends))
	for i := range b.backends {
		if b.backends[i].Healthy {
			healthy = append(healthy, &b.backends[i])
		}
	}
	if len(healthy) == 0 {
		return nil, errors.New("lb: no healthy backends")
	}
	if b.cfg.Strategy == StrategyLeastConnections {
		var best *Backend
		for _, be := range healthy {
			if best == nil || be.Conns < best.Conns {
				best = be
			}
		}
		return best, nil
	}
	// round-robin
	idx := int(time.Now().UnixNano()) % len(healthy)
	return healthy[idx], nil
}

func (b *Balancer) refresh(ctx context.Context) {
	projects, err := b.cfg.Store.ListProjects(ctx)
	if err != nil {
		return
	}
	var svc *state.Service
	for _, p := range projects {
		svcs, _ := b.cfg.Store.ListServices(ctx, p.ID)
		for _, s := range svcs {
			if s.ID == b.cfg.Service || s.Name == b.cfg.Service {
				svc = s
				break
			}
		}
	}
	if svc == nil {
		return
	}
	ps, _ := b.cfg.Store.GetPlacements(ctx, svc.WorkloadID)
	var addrs []string
	for _, p := range ps {
		if p.Status != "Ready" {
			continue
		}
		// For the MVP, use nodeID:port as the backend. Real impl would
		// resolve to the container's IP+port via the runtime.
		addrs = append(addrs, fmt.Sprintf("%s:%d", p.NodeID, svc.Port))
	}
	b.mu.Lock()
	b.backends = b.backends[:0]
	for _, a := range addrs {
		b.backends = append(b.backends, Backend{Addr: a, Healthy: true, LastChange: time.Now()})
	}
	b.mu.Unlock()
}

func (b *Balancer) healthCheck(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := net.Dialer{Timeout: time.Second}
	for i := range b.backends {
		c, err := d.DialContext(ctx, "tcp", b.backends[i].Addr)
		if err != nil {
			if b.backends[i].Healthy {
				b.backends[i].Healthy = false
				b.backends[i].LastChange = time.Now()
			}
			continue
		}
		c.Close()
		if !b.backends[i].Healthy {
			b.backends[i].Healthy = true
			b.backends[i].LastChange = time.Now()
		}
	}
}

// Close stops the balancer.
func (b *Balancer) Close() error {
	b.closed.Store(true)
	close(b.stop)
	return nil
}