package object

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/state"
)

func newStore(t *testing.T) (*Store, *state.Store) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	root := filepath.Join(t.TempDir(), "obj")
	s, err := New(Config{Store: st, RootDir: root})
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func TestBucketAndObject(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.CreateBucket(ctx, "b1"); err != nil {
		t.Fatal(err)
	}
	digest, err := s.PutObject(ctx, "b1", "k1", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" {
		t.Fatal("empty digest")
	}
	objs, _ := s.ListObjects(ctx, "b1", "")
	if len(objs) != 1 || objs[0].Key != "k1" {
		t.Fatalf("got %+v", objs)
	}
	r, o, err := s.GetObject(ctx, "b1", "k1")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	if string(b) != "hello" || o.SHA256 != digest {
		t.Fatalf("got %s digest=%s", b, o.SHA256)
	}
}

func TestDeleteObject(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.CreateBucket(ctx, "b2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutObject(ctx, "b2", "k1", bytes.NewReader([]byte("hi"))); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteObject(ctx, "b2", "k1"); err != nil {
		t.Fatal(err)
	}
	objs, _ := s.ListObjects(ctx, "b2", "")
	if len(objs) != 0 {
		t.Fatalf("expected empty, got %d", len(objs))
	}
}

func TestListWithPrefix(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.CreateBucket(ctx, "b3"); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a/1", "a/2", "b/1"} {
		if _, err := s.PutObject(ctx, "b3", k, bytes.NewReader([]byte("x"))); err != nil {
			t.Fatal(err)
		}
	}
	objs, _ := s.ListObjects(ctx, "b3", "a/")
	if len(objs) != 2 {
		t.Fatalf("expected 2, got %d", len(objs))
	}
}

func TestPresign(t *testing.T) {
	s, _ := newStore(t)
	u := s.Presign("b", "k", 60)
	if u == "" {
		t.Fatal("empty url")
	}
	parts := strings.SplitN(u, "?", 2)
	if len(parts) != 2 {
		t.Fatalf("bad presign url: %s", u)
	}
	v, err := url.ParseQuery(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	bb, kk, err := s.VerifyPresign(v)
	if err != nil || bb != "b" || kk != "k" {
		t.Fatalf("err=%v bb=%s kk=%s", err, bb, kk)
	}
}

func TestVerifyPresignBadSignature(t *testing.T) {
	s, _ := newStore(t)
	v := url.Values{}
	v.Set("bucket", "b")
	v.Set("key", "k")
	v.Set("exp", "123")
	v.Set("sig", "bogus")
	if _, _, err := s.VerifyPresign(v); err == nil {
		t.Fatal("expected error on bad signature")
	}
}
