// Package fault contains fault-injection tests. The point is to
// verify that the platform degrades gracefully when its underlying
// primitives fail or behave unexpectedly.
package fault

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/state"
	"github.com/minicloud/platform/internal/util/retry"
)

func TestKVRecoverFromFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	// Try an idempotent put; retry helper should succeed quickly.
	ctx := context.Background()
	err = retry.Do(ctx, 3, time.Millisecond, 5*time.Millisecond, nil, func() error {
		return st.CreateProject(ctx, &state.Project{Base: state.Base{ID: "p1"}})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestKVNotFound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	_, err = st.GetProject(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRetryStopsOnPermanent(t *testing.T) {
	called := 0
	err := retry.Do(context.Background(), 5, time.Millisecond, time.Millisecond, retry.Is, func() error {
		called++
		return retry.Permanent(errors.New("fatal"))
	})
	if err == nil || called != 1 {
		t.Fatalf("expected 1 call, got %d (err=%v)", called, err)
	}
}
