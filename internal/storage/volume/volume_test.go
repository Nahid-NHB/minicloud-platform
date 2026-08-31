package volume

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/state"
)

func newMgr(t *testing.T) (*Manager, *state.Store) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	root := filepath.Join(t.TempDir(), "vol")
	m, err := New(Config{Store: st, RootDir: root})
	if err != nil {
		t.Fatal(err)
	}
	return m, st
}

func TestCreateAndRead(t *testing.T) {
	m, _ := newMgr(t)
	ctx := context.Background()
	if err := m.Create(ctx, "p", "v1", 1024, "n1"); err != nil {
		t.Fatal(err)
	}
	r, err := m.Read(ctx, "p", "v1")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	buf := &bytes.Buffer{}
	io.Copy(buf, r)
	// newly created volume is sparse; just verify file exists.
}

func TestAttachDetach(t *testing.T) {
	m, st := newMgr(t)
	ctx := context.Background()
	if err := m.Create(ctx, "p", "v2", 1024, "n1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Attach(ctx, "p", "v2", "n2"); err != nil {
		t.Fatal(err)
	}
	v, _ := st.GetVolume(ctx, "p", "v2")
	if v.NodeID != "n2" || v.Status != "InUse" {
		t.Fatalf("got %+v", v)
	}
	if err := m.Detach(ctx, "p", "v2"); err != nil {
		t.Fatal(err)
	}
	v, _ = st.GetVolume(ctx, "p", "v2")
	if v.Status != "Available" {
		t.Fatalf("got %+v", v)
	}
}

func TestDelete(t *testing.T) {
	m, _ := newMgr(t)
	ctx := context.Background()
	if err := m.Create(ctx, "p", "v3", 512, "n1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "p", "v3"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Read(ctx, "p", "v3"); err == nil {
		t.Fatal("expected error after delete")
	}
}
