package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func tmpDataDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func newSingleNode(t *testing.T) (KV, ClusterMembership, string) {
	t.Helper()
	dir := filepath.Join(tmpDataDir(t), "node")
	kv, cm, err := Open(context.Background(), Config{
		NodeID: "n1", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return kv, cm, dir
}

func TestPutGetDelete(t *testing.T) {
	kv, _, _ := newSingleNode(t)
	defer kv.Close()

	if _, err := kv.Put(context.Background(), "hello", []byte("world")); err != nil {
		t.Fatalf("put: %v", err)
	}
	e, err := kv.Get(context.Background(), "hello")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(e.Value) != "world" {
		t.Fatalf("value = %q want %q", e.Value, "world")
	}
	if e.Version != 1 {
		t.Fatalf("version = %d want 1", e.Version)
	}
	if _, err := kv.Put(context.Background(), "hello", []byte("again")); err != nil {
		t.Fatalf("put2: %v", err)
	}
	e2, _ := kv.Get(context.Background(), "hello")
	if e2.Version != 2 {
		t.Fatalf("version = %d want 2", e2.Version)
	}
	if err := kv.Delete(context.Background(), "hello"); err != nil {
		t.Fatalf("del: %v", err)
	}
	if _, err := kv.Get(context.Background(), "hello"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound got %v", err)
	}
}

func TestListPrefix(t *testing.T) {
	kv, _, _ := newSingleNode(t)
	defer kv.Close()
	for _, k := range []string{"a/1", "a/2", "b/1"} {
		if _, err := kv.Put(context.Background(), k, []byte(k)); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	es, err := kv.List(context.Background(), "a/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(es) != 2 {
		t.Fatalf("got %d entries want 2", len(es))
	}
}

func TestCAS(t *testing.T) {
	kv, _, _ := newSingleNode(t)
	defer kv.Close()
	if _, err := kv.Put(context.Background(), "k", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := kv.CompareAndSwap(context.Background(), "k", []byte("v2"), 1); err != nil {
		t.Fatalf("cas expected ok: %v", err)
	}
	if _, err := kv.CompareAndSwap(context.Background(), "k", []byte("v3"), 1); !errors.Is(err, ErrCASMismatch) {
		t.Fatalf("expected ErrCASMismatch, got %v", err)
	}
}

func TestWatch(t *testing.T) {
	kv, _, _ := newSingleNode(t)
	defer kv.Close()
	ch, err := kv.Watch(context.Background(), WatchOptions{Prefix: ""})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_, _ = kv.Put(context.Background(), "x", []byte("y"))
	}()
	select {
	case e := <-ch:
		if e.Key != "x" || string(e.Value) != "y" {
			t.Fatalf("got %v want x=y", e)
		}
	case <-ctx.Done():
		t.Fatal("watch timeout")
	}
}

func TestRecoverFromDisk(t *testing.T) {
	dir := filepath.Join(tmpDataDir(t), "n")
	kv, _, err := Open(context.Background(), Config{NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(context.Background(), "p", []byte("q")); err != nil {
		t.Fatal(err)
	}
	kv.Close()

	kv2, _, err := Open(context.Background(), Config{NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	defer kv2.Close()
	e, err := kv2.Get(context.Background(), "p")
	if err != nil {
		t.Fatalf("recovered key missing: %v", err)
	}
	if string(e.Value) != "q" {
		t.Fatalf("got %q want q", e.Value)
	}
}

func TestMembership(t *testing.T) {
	kv, cm, _ := newSingleNode(t)
	defer kv.Close()
	if err := cm.AddPeer(context.Background(), Peer{ID: "n2", Address: "127.0.0.1:2222"}); err != nil {
		t.Fatal(err)
	}
	ps, _ := cm.Peers(context.Background())
	if len(ps) != 2 {
		t.Fatalf("got %d peers want 2", len(ps))
	}
	if err := cm.RemovePeer(context.Background(), "n2"); err != nil {
		t.Fatal(err)
	}
	ps, _ = cm.Peers(context.Background())
	if len(ps) != 1 {
		t.Fatalf("got %d peers want 1", len(ps))
	}
}
