package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// role states.
type role int

const (
	roleFollower role = iota
	roleCandidate
	roleLeader
)

// logEntry is a journaled operation.
type logEntry struct {
	Index   uint64 `json:"i"`
	Term    uint64 `json:"t"`
	Op      string `json:"o"` // put | del
	Key     string `json:"k"`
	Value   []byte `json:"v,omitempty"`
	Version uint64 `json:"vr"`
}

// raftSnapshot is the on-disk snapshot format.
type raftSnapshot struct {
	Term  uint64            `json:"term"`
	Index uint64            `json:"index"`
	State map[string]*Entry `json:"state"`
}

// store combines an inmem map with a write-ahead log + snapshot file.
type store struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	version uint64
}

func (s *store) get(key string) *Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.entries[key]; ok {
		cp := *e
		return &cp
	}
	return nil
}

func (s *store) put(key string, e *Entry) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.entries[key]; ok {
		e.Version = cur.Version + 1
	} else {
		e.Version = 1
	}
	s.entries[key] = e
	s.version++
	return e.Version
}

func (s *store) del(key string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := uint64(0)
	if cur, ok := s.entries[key]; ok {
		v = cur.Version + 1
	}
	delete(s.entries, key)
	s.version++
	return v
}

// raft is a concrete KV implementation.
type raft struct {
	cfg Config

	mu         sync.RWMutex
	role       role
	currentTerm uint64
	votedFor   string
	log        []logEntry
	commitIndex uint64
	lastApplied uint64

	peers      []Peer
	nextIndex  map[string]uint64
	matchIndex map[string]uint64

	st *store

	journal interface {
		io.WriteCloser
		Sync() error
	}

	electionTimer  *time.Timer
	applyCh        chan struct{}
	stopCh         chan struct{}
	applyQueueMu   sync.Mutex
	applyQueue     []*Entry

	watchMu  sync.RWMutex
	watchers map[string][]*watcher

	mets struct {
		puts    atomic.Uint64
		dels    atomic.Uint64
		applied atomic.Uint64
	}
}

type watcher struct {
	prefix string
	ch     chan *Entry
}

func openRaft(ctx context.Context, cfg Config) (KV, ClusterMembership, error) {
	if cfg.NodeID == "" {
		return nil, nil, errors.New("db: NodeID required")
	}
	if cfg.DataDir == "" {
		return nil, nil, errors.New("db: DataDir required")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, nil, err
	}
	r := &raft{
		cfg:        cfg,
		peers:      append([]Peer{}, cfg.Peers...),
		nextIndex:  map[string]uint64{},
		matchIndex: map[string]uint64{},
		st:         &store{entries: map[string]*Entry{}},
		applyCh:    make(chan struct{}, 1),
		stopCh:     make(chan struct{}),
		watchers:   map[string][]*watcher{},
	}
	self := Peer{ID: cfg.NodeID, Address: cfg.Listen}
	if !r.containsPeer(self) {
		r.peers = append(r.peers, self)
	}
	for _, p := range r.peers {
		r.nextIndex[p.ID] = 1
		r.matchIndex[p.ID] = 0
	}
	if err := r.recover(); err != nil {
		return nil, nil, fmt.Errorf("recover: %w", err)
	}
	if cfg.Bootstrap && r.currentTerm == 0 {
		r.mu.Lock()
		r.currentTerm = 1
		r.votedFor = cfg.NodeID
		r.mu.Unlock()
	}
	go r.run(ctx)
	return r, r, nil
}

func (r *raft) containsPeer(p Peer) bool {
	for _, x := range r.peers {
		if x.ID == p.ID {
			return true
		}
	}
	return false
}

// ---------- persistence ----------

func (r *raft) recover() error {
	jf, err := os.OpenFile(filepath.Join(r.cfg.DataDir, "journal.jsonl"),
		os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	r.journal = jf
	dec := json.NewDecoder(jf)
	for {
		var e logEntry
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		r.mu.Lock()
		r.log = append(r.log, e)
		if e.Term > r.currentTerm {
			r.currentTerm = e.Term
		}
		if e.Index > r.commitIndex && len(r.peers) == 1 {
			r.commitIndex = e.Index
		}
		r.mu.Unlock()
		r.applyLocked(e)
	}
	snapPath := filepath.Join(r.cfg.DataDir, "snap.json")
	if data, err := os.ReadFile(snapPath); err == nil && len(data) > 0 {
		var snap raftSnapshot
		if err := json.Unmarshal(data, &snap); err == nil {
			r.st.mu.Lock()
			r.st.entries = snap.State
			r.st.version = snap.Index
			r.st.mu.Unlock()
			r.mu.Lock()
			r.commitIndex = snap.Index
			r.lastApplied = snap.Index
			r.mu.Unlock()
		}
	}
	return nil
}

func (r *raft) append(e logEntry) (logEntry, error) {
	r.mu.Lock()
	e.Index = uint64(len(r.log)) + 1
	if r.currentTerm == 0 {
		r.currentTerm = 1
	}
	e.Term = r.currentTerm
	r.log = append(r.log, e)
	r.mu.Unlock()
	data, _ := json.Marshal(e)
	if _, err := r.journal.Write(append(data, '\n')); err != nil {
		return e, err
	}
	return e, r.journal.Sync()
}

func (r *raft) snapshot() error {
	r.st.mu.RLock()
	state := make(map[string]*Entry, len(r.st.entries))
	for k, v := range r.st.entries {
		cp := *v
		state[k] = &cp
	}
	rev := r.st.version
	r.st.mu.RUnlock()
	r.mu.RLock()
	snap := raftSnapshot{Term: r.currentTerm, Index: rev, State: state}
	r.mu.RUnlock()
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tmp := filepath.Join(r.cfg.DataDir, "snap.json.tmp")
	final := filepath.Join(r.cfg.DataDir, "snap.json")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := r.journal.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	jf, err := os.OpenFile(filepath.Join(r.cfg.DataDir, "journal.jsonl"),
		os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	r.journal = jf
	return nil
}

// ---------- state machine ----------

func (r *raft) applyLocked(e logEntry) {
	r.mu.Lock()
	if e.Index <= r.lastApplied {
		r.mu.Unlock()
		return
	}
	r.lastApplied = e.Index
	r.mu.Unlock()
	r.st.mu.Lock()
	switch e.Op {
	case "put":
		e2 := &Entry{
			Key: e.Key, Value: append([]byte{}, e.Value...), Version: e.Version,
			CreatedAt: time.Now().UTC(),
		}
		r.st.entries[e.Key] = e2
		r.st.version++
	case "del":
		delete(r.st.entries, e.Key)
		r.st.version++
	}
	r.st.mu.Unlock()
	ev := r.st.get(e.Key)
	if e.Op == "del" {
		ev = &Entry{Key: e.Key, Deleted: true, Version: e.Version, CreatedAt: time.Now().UTC()}
	}
	r.applyQueueMu.Lock()
	r.applyQueue = append(r.applyQueue, ev)
	r.applyQueueMu.Unlock()
	r.mets.applied.Add(1)
	select {
	case r.applyCh <- struct{}{}:
	default:
	}
}

func (r *raft) applyFromQueue() {
	r.applyQueueMu.Lock()
	evs := r.applyQueue
	r.applyQueue = nil
	r.applyQueueMu.Unlock()
	for _, ev := range evs {
		r.fanout(ev)
	}
}

func (r *raft) fanout(ev *Entry) {
	r.watchMu.RLock()
	defer r.watchMu.RUnlock()
	for _, subs := range r.watchers {
		for _, w := range subs {
			if !hasPrefix(ev.Key, w.prefix) {
				continue
			}
			cp := *ev
			select {
			case w.ch <- &cp:
			default:
			}
		}
	}
}

func hasPrefix(s, p string) bool {
	if p == "" {
		return true
	}
	return len(s) >= len(p) && s[:len(p)] == p
}

// ---------- KV interface ----------

func (r *raft) Close() error {
	close(r.stopCh)
	if r.journal != nil {
		return r.journal.Close()
	}
	return nil
}

func (r *raft) Get(ctx context.Context, key string) (*Entry, error) {
	e := r.st.get(key)
	if e == nil {
		return nil, ErrNotFound
	}
	return e, nil
}

func (r *raft) Put(ctx context.Context, key string, value []byte) (*Entry, error) {
	r.mu.Lock()
	v := uint64(1)
	r.st.mu.RLock()
	if cur, ok := r.st.entries[key]; ok {
		v = cur.Version + 1
	}
	r.st.mu.RUnlock()
	r.mu.Unlock()
	e := logEntry{Op: "put", Key: key, Value: append([]byte{}, value...), Version: v}
	var err error
	e, err = r.append(e)
	if err != nil {
		return nil, err
	}
	if err := r.replicate(e); err != nil {
		return nil, err
	}
	r.mets.puts.Add(1)
	return r.st.get(key), nil
}

func (r *raft) Delete(ctx context.Context, key string) error {
	e := logEntry{Op: "del", Key: key}
	var err error
	e, err = r.append(e)
	if err != nil {
		return err
	}
	if err := r.replicate(e); err != nil {
		return err
	}
	r.mets.dels.Add(1)
	return nil
}

func (r *raft) CompareAndSwap(ctx context.Context, key string, value []byte, expect uint64) (*Entry, error) {
	r.st.mu.RLock()
	cur, ok := r.st.entries[key]
	r.st.mu.RUnlock()
	if ok && cur.Version != expect {
		return nil, ErrCASMismatch
	}
	if !ok && expect != 0 {
		return nil, ErrCASMismatch
	}
	v := expect + 1
	if !ok {
		v = 1
	}
	e := logEntry{Op: "put", Key: key, Value: append([]byte{}, value...), Version: v}
	var err error
	e, err = r.append(e)
	if err != nil {
		return nil, err
	}
	if err := r.replicate(e); err != nil {
		return nil, err
	}
	return r.st.get(key), nil
}

func (r *raft) List(ctx context.Context, prefix string) ([]*Entry, error) {
	r.st.mu.RLock()
	defer r.st.mu.RUnlock()
	out := make([]*Entry, 0, len(r.st.entries))
	for k, v := range r.st.entries {
		if !hasPrefix(k, prefix) {
			continue
		}
		cp := *v
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (r *raft) Snapshot(ctx context.Context, prefix string) (*Snapshot, error) {
	es, err := r.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	r.st.mu.RLock()
	rev := r.st.version
	r.st.mu.RUnlock()
	return &Snapshot{Revision: rev, Entries: es}, nil
}

func (r *raft) Watch(ctx context.Context, opts WatchOptions) (<-chan *Entry, error) {
	ch := make(chan *Entry, 256)
	w := &watcher{prefix: opts.Prefix, ch: ch}
	r.watchMu.Lock()
	r.watchers[opts.Prefix] = append(r.watchers[opts.Prefix], w)
	r.watchMu.Unlock()
	// Replay snapshot to caller.
	es, _ := r.List(ctx, opts.Prefix)
	go func() {
		for _, e := range es {
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	if opts.Timeout > 0 {
		go func() {
			t := time.NewTimer(opts.Timeout)
			defer t.Stop()
			select {
			case <-t.C:
				close(ch)
			case <-ctx.Done():
				close(ch)
			}
		}()
	}
	return ch, nil
}

// ---------- ClusterMembership ----------

func (r *raft) AddPeer(ctx context.Context, p Peer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.containsPeer(p) {
		return nil
	}
	r.peers = append(r.peers, p)
	r.nextIndex[p.ID] = uint64(len(r.log)) + 1
	r.matchIndex[p.ID] = 0
	return nil
}

func (r *raft) RemovePeer(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.peers[:0]
	for _, p := range r.peers {
		if p.ID != id {
			out = append(out, p)
		}
	}
	r.peers = out
	delete(r.nextIndex, id)
	delete(r.matchIndex, id)
	return nil
}

func (r *raft) Peers(ctx context.Context) ([]Peer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := append([]Peer{}, r.peers...)
	return out, nil
}

// ---------- replication & loop ----------

func (r *raft) replicate(e logEntry) error {
	r.mu.Lock()
	if len(r.peers) == 1 {
		r.commitIndex = e.Index
		r.mu.Unlock()
		r.applyLocked(e)
		r.applyFromQueue()
		return nil
	}
	r.mu.Unlock()
	ackCh := make(chan struct{}, len(r.peers))
	r.mu.RLock()
	for _, p := range r.peers {
		if p.ID == r.cfg.NodeID {
			ackCh <- struct{}{}
			continue
		}
		go r.replicateTo(p, ackCh)
	}
	r.mu.RUnlock()
	need := (len(r.peers) / 2) + 1
	acks := 0
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for acks < need {
		select {
		case <-ackCh:
			acks++
		case <-timer.C:
			return errors.New("db: replication timeout")
		}
	}
	r.mu.Lock()
	if e.Index > r.commitIndex {
		r.commitIndex = e.Index
	}
	r.mu.Unlock()
	r.applyLocked(e)
	r.applyFromQueue()
	return nil
}

func (r *raft) replicateTo(p Peer, ackCh chan<- struct{}) {
	d := net.Dialer{Timeout: 200 * time.Millisecond}
	conn, err := d.Dial("tcp", p.Address)
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{'R'}); err != nil {
		return
	}
	ackCh <- struct{}{}
}

func (r *raft) run(ctx context.Context) {
	r.electionTimer = time.NewTimer(electionTimeout())
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-r.electionTimer.C:
			r.mu.Lock()
			r.role = roleCandidate
			r.currentTerm++
			r.votedFor = r.cfg.NodeID
			r.mu.Unlock()
			if len(r.peers) == 1 {
				r.mu.Lock()
				r.role = roleLeader
				r.mu.Unlock()
			}
			r.electionTimer.Reset(electionTimeout())
		case <-r.applyCh:
			r.applyFromQueue()
		}
		if r.mets.applied.Load()%256 == 0 && r.mets.applied.Load() > 0 {
			_ = r.snapshot()
		}
	}
}

func electionTimeout() time.Duration { return 1500 * time.Millisecond }
