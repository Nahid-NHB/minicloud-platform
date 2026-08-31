// Package db provides a strongly-consistent distributed key-value store
// used as the platform's source of truth for desired cluster state.
//
// The implementation is a single-node Raft-compatible log with a swappable
// transport so a multi-node cluster can be formed by adding peers via
// AddPeer. Every write is durably journaled before being applied.
//
// The exposed surface is intentionally narrow: Get/Put/Delete/Watch plus
// CompareAndSwap. This is enough to build controllers, schedulers and
// service registries without coupling to a particular consensus engine.
package db

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("kv: key not found")

// ErrCASMismatch is returned when a CompareAndSwap observes a different
// version than the caller expected.
var ErrCASMismatch = errors.New("kv: cas version mismatch")

// Entry is a watched-change record.
type Entry struct {
	Key       string
	Value     []byte
	Version   uint64 // monotonically increasing per-key revision
	Deleted   bool
	CreatedAt time.Time
}

// WatchOptions controls a watch session.
type WatchOptions struct {
	Prefix  string        // empty = all keys
	Since   uint64        // revision to start from; 0 = current
	Timeout time.Duration // 0 = forever
}

// KV is the public interface of the primitive.
type KV interface {
	// Get returns the current value and version for a key.
	Get(ctx context.Context, key string) (*Entry, error)

	// Put writes a new value. The returned Entry has Version incremented
	// atomically with the write.
	Put(ctx context.Context, key string, value []byte) (*Entry, error)

	// Delete removes a key; idempotent.
	Delete(ctx context.Context, key string) error

	// CompareAndSwap atomically replaces the value if its current
	// version equals expectVersion.
	CompareAndSwap(ctx context.Context, key string, value []byte, expectVersion uint64) (*Entry, error)

	// List returns all entries whose keys start with prefix.
	List(ctx context.Context, prefix string) ([]*Entry, error)

	// Watch streams changes from Since. The first entry delivered is the
	// current snapshot at Since, followed by deltas.
	Watch(ctx context.Context, opts WatchOptions) (<-chan *Entry, error)

	// Snapshot returns a point-in-time view of all entries with prefix.
	Snapshot(ctx context.Context, prefix string) (*Snapshot, error)

	// Close stops the underlying goroutines and releases resources.
	Close() error
}

// Snapshot is a point-in-time copy of state under a prefix.
type Snapshot struct {
	Revision uint64
	Entries  []*Entry
}

// Peer represents a remote node in the Raft group.
type Peer struct {
	ID      string
	Address string
}

// ClusterMembership controls the peer set at runtime.
type ClusterMembership interface {
	AddPeer(ctx context.Context, peer Peer) error
	RemovePeer(ctx context.Context, id string) error
	Peers(ctx context.Context) ([]Peer, error)
}

// Config holds all options for opening a KV.
type Config struct {
	NodeID    string
	DataDir   string
	Listen    string // raft transport listen address
	Bootstrap bool   // true for the first node of a new cluster
	Peers     []Peer
}

// Open returns a KV backed by an embedded Raft log.
//
// For single-node operation pass Bootstrap:true. For multi-node, only the
// initial node sets Bootstrap:true; later nodes are added via AddPeer.
func Open(ctx context.Context, cfg Config) (KV, ClusterMembership, error) {
	return openRaft(ctx, cfg)
}
