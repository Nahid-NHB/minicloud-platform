package leader

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	db "github.com/minicloud/platform/internal/primitives/db"
)

func newKV(t *testing.T) db.KV {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{
		NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return kv
}

func TestElectionSingleLeader(t *testing.T) {
	kv := newKV(t)
	e := NewElection(Config{NodeID: "n1", Group: "ctrl", KV: kv, TTL: 5 * time.Second, Heartbeat: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go e.Run(ctx)
	time.Sleep(500 * time.Millisecond)
	if !e.IsLeader() {
		t.Fatal("expected leader")
	}
	if e.Term() == 0 {
		t.Fatal("expected nonzero term")
	}
	e.Stop()
}

func TestElectionLeaderChange(t *testing.T) {
	kv := newKV(t)
	e1 := NewElection(Config{NodeID: "n1", Group: "g", KV: kv, TTL: 200 * time.Millisecond, Heartbeat: 50 * time.Millisecond})
	e2 := NewElection(Config{NodeID: "n2", Group: "g", KV: kv, TTL: 200 * time.Millisecond, Heartbeat: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); e1.Run(ctx) }()
	go func() { defer wg.Done(); e2.Run(ctx) }()
	time.Sleep(500 * time.Millisecond)
	lr, err := GetLeader(context.Background(), kv, "g")
	if err != nil {
		t.Fatal(err)
	}
	if lr.NodeID != "n1" && lr.NodeID != "n2" {
		t.Fatalf("unexpected leader: %v", lr)
	}
	e1.Stop()
	e2.Stop()
}