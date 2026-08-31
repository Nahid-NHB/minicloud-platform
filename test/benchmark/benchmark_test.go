// Package benchmark contains microbenchmarks for the platform's hot
// paths. Run with `go test -bench=. ./test/benchmark`.
package benchmark

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/state"
)

func benchStore(b *testing.B) *state.Store {
	b.Helper()
	dir, err := os.MkdirTemp("", "mc-bench-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { os.RemoveAll(dir) })
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n", DataDir: filepath.Join(dir, "kv"), Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		b.Fatal(err)
	}
	return state.NewStore(kv)
}

func BenchmarkWorkloadGet(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()
	if err := s.CreateProject(ctx, &state.Project{Base: state.Base{ID: "p"}}); err != nil {
		b.Fatal(err)
	}
	w := &state.Workload{Base: state.Base{ID: "w", ProjectID: "p"}, Replicas: 1, CPUMillicores: 100, MemoryBytes: 1 << 20}
	if err := s.CreateWorkload(ctx, w); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.GetWorkload(ctx, "p", "w"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListWorkloads(b *testing.B) {
	s := benchStore(b)
	ctx := context.Background()
	if err := s.CreateProject(ctx, &state.Project{Base: state.Base{ID: "p"}}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		w := &state.Workload{Base: state.Base{ID: "w", ProjectID: "p"}, Replicas: 1, CPUMillicores: 100, MemoryBytes: 1 << 20}
		_ = s.CreateWorkload(ctx, w)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ListWorkloads(ctx, "p"); err != nil {
			b.Fatal(err)
		}
	}
}
