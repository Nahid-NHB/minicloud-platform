package dns

import (
	"bytes"
	"context"
	"encoding/binary"
	"path/filepath"
	"testing"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/state"
)

func newServer(t *testing.T) (*Server, *state.Store, context.Context) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{
		NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	s := NewServer(Config{Store: st, Listen: "127.0.0.1:0", Zone: "cluster.local"})
	return s, st, context.Background()
}

func TestDNSParsing(t *testing.T) {
	// Build a fake A query for "web.p.svc.cluster.local" and ensure the
	// parser returns the right name.
	q := buildQuery("web.p.svc.cluster.local", 1)
	name, _, err := readName(q, 12)
	if err != nil {
		t.Fatal(err)
	}
	if name != "web.p.svc.cluster.local" {
		t.Fatalf("got %q", name)
	}
}

func TestDNSHandle(t *testing.T) {
	s, st, ctx := newServer(t)
	p := &state.Project{Base: state.Base{ID: "p"}}
	if err := st.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRecord(ctx, "p", "web.p.svc.cluster.local", "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshZone(ctx); err != nil {
		t.Fatal(err)
	}
	resp, err := s.handleQuery(buildQuery("web.p.svc.cluster.local", 1))
	if err != nil {
		t.Fatal(err)
	}
	anCount := binary.BigEndian.Uint16(resp[6:8])
	if anCount != 1 {
		t.Fatalf("anCount=%d", anCount)
	}
}

func buildQuery(name string, qtype uint16) []byte {
	var b bytes.Buffer
	b.Write(make([]byte, 12))
	for _, label := range splitDots(name) {
		b.WriteByte(byte(len(label)))
		b.Write([]byte(label))
	}
	b.WriteByte(0)
	binary.Write(&b, binary.BigEndian, qtype)
	binary.Write(&b, binary.BigEndian, uint16(1)) // IN
	return b.Bytes()
}

func splitDots(s string) []string {
	out := []string{}
	last := 0
	for i, c := range s {
		if c == '.' {
			out = append(out, s[last:i])
			last = i + 1
		}
	}
	out = append(out, s[last:])
	return out
}
