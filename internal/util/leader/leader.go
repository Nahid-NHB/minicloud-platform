// Package leader implements a simple Raft-like leader election loop
// on top of the KV primitive. A single key per group holds the current
// leader's identity and term. When the key disappears or its term
// expires, candidates race to claim leadership with CAS.
package leader

import (
	"context"
	"errors"
	"sync"
	"time"

	db "github.com/minicloud/platform/internal/primitives/db"
)

// State is the current leader state observed by a candidate.
type State int

const (
	Follower State = iota
	Candidate
	Leader
)

// Config configures a single candidate.
type Config struct {
	NodeID    string
	Group     string // leader group name
	TTL       time.Duration
	Heartbeat time.Duration
	KV        db.KV
}

// Election is the leader-election state machine for one node.
type Election struct {
	cfg Config

	mu       sync.Mutex
	state    State
	term     uint64
	lastSeen time.Time

	stopCh chan struct{}
}

// NewElection creates an election loop.
func NewElection(cfg Config) *Election {
	if cfg.TTL == 0 {
		cfg.TTL = 5 * time.Second
	}
	if cfg.Heartbeat == 0 {
		cfg.Heartbeat = 1500 * time.Millisecond
	}
	return &Election{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// Run starts the election loop. It returns when ctx is canceled.
func (e *Election) Run(ctx context.Context) {
	t := time.NewTicker(e.cfg.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-t.C:
			e.tick(ctx)
		}
	}
}

// Stop stops the loop.
func (e *Election) Stop() { close(e.stopCh) }

// IsLeader reports the current state.
func (e *Election) IsLeader() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state == Leader
}

// Term returns the current term.
func (e *Election) Term() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.term
}

func leaderKey(group string) string {
	return "_leader/" + group
}

func (e *Election) tick(ctx context.Context) {
	e.mu.Lock()
	e.state = Candidate
	e.mu.Unlock()
	now := time.Now()
	key := leaderKey(e.cfg.Group)
	e.mu.Lock()
	e.term++
	cur := LeaderRecord{
		NodeID: e.cfg.NodeID,
		Term:   e.term,
		Expiry: now.Add(e.cfg.TTL),
	}
	e.mu.Unlock()
	b := mustMarshal(cur)
	ent, err := e.cfg.KV.Get(ctx, key)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return
	}
	if ent != nil {
		var prev LeaderRecord
		mustUnmarshal(ent.Value, &prev)
		if prev.Expiry.After(now) && prev.Term >= cur.Term {
			// Someone else is leader.
			e.mu.Lock()
			e.state = Follower
			e.term = prev.Term
			e.lastSeen = now
			e.mu.Unlock()
			return
		}
	}
	// Try to claim leadership.
	if ent == nil {
		// Initial claim — use Put.
		if _, err := e.cfg.KV.Put(ctx, key, b); err == nil {
			e.mu.Lock()
			e.state = Leader
			e.lastSeen = now
			e.mu.Unlock()
		}
		return
	}
	if _, err := e.cfg.KV.CompareAndSwap(ctx, key, b, ent.Version); err == nil {
		e.mu.Lock()
		e.state = Leader
		e.lastSeen = now
		e.mu.Unlock()
	}
}

// Version returns 0 if ent is nil.
func versionOrZero(ent *db.Entry) uint64 {
	if ent == nil {
		return 0
	}
	return ent.Version
}

// LeaderRecord is the persisted leader record.
type LeaderRecord struct {
	NodeID string    `json:"node_id"`
	Term   uint64    `json:"term"`
	Expiry time.Time `json:"expiry"`
}

// GetLeader returns the current leader record, if any.
func GetLeader(ctx context.Context, kv db.KV, group string) (*LeaderRecord, error) {
	ent, err := kv.Get(ctx, leaderKey(group))
	if err != nil {
		return nil, err
	}
	var lr LeaderRecord
	mustUnmarshal(ent.Value, &lr)
	return &lr, nil
}