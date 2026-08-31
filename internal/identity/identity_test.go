package identity

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/state"
)

func newManager(t *testing.T) (*Manager, *state.Store, context.Context) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{
		NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(kv)
	m := New(store, []byte("secret"))
	ctx := context.Background()
	if _, err := m.BootstrapAdmin(ctx, "admin@example.com", "admin", "Admin"); err != nil {
		t.Fatal(err)
	}
	return m, store, ctx
}

func TestLoginAndToken(t *testing.T) {
	m, _, ctx := newManager(t)
	tok, exp, u, err := m.Login(ctx, "admin@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if exp.Before(time.Now()) || u == nil {
		t.Fatalf("bad login: exp=%v u=%v", exp, u)
	}
	u2, err := m.VerifyToken(ctx, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if u2.ID != u.ID {
		t.Fatalf("id mismatch")
	}
}

func TestBadPassword(t *testing.T) {
	m, _, ctx := newManager(t)
	_, _, _, err := m.Login(ctx, "admin@example.com", "wrong")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAPIKeyIssueAndAuth(t *testing.T) {
	m, store, ctx := newManager(t)
	p := &state.Project{Base: state.Base{ID: "p1", Name: "p"}, QuotaCPU: 1000, QuotaMem: 1 << 30}
	if err := store.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, secret, err := m.IssueAPIKey(ctx, "p1", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	k, err := m.AuthAPIKey(ctx, secret)
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if k.ProjectID != "p1" {
		t.Fatalf("got %s", k.ProjectID)
	}
	if _, err := m.AuthAPIKey(ctx, "ctlk_bogus"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRBAC(t *testing.T) {
	if !HasPermission([]string{"*:*"}, "create", "workloads") {
		t.Fatal("admin should match")
	}
	if !HasPermission([]string{"get:*"}, "get", "workloads") {
		t.Fatal("viewer should read")
	}
	if HasPermission([]string{"get:*"}, "create", "workloads") {
		t.Fatal("viewer should not create")
	}
	if !HasPermission([]string{"*:workloads"}, "delete", "workloads") {
		t.Fatal("editor should delete workloads")
	}
	if !HasPermission([]string{"get:nodes"}, "get", "nodes") {
		t.Fatal("operator read nodes")
	}
	if HasPermission([]string{"get:nodes"}, "create", "nodes") {
		t.Fatal("operator shouldn't create")
	}
}
