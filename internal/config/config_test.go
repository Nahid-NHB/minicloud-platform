package config

import (
	"context"
	"path/filepath"
	"testing"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/secret"
	"github.com/minicloud/platform/internal/state"
)

func newSetup(t *testing.T) (*Resolver, *state.Store) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	c, err := secret.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return New(st, c), st
}

func TestResolveEnv(t *testing.T) {
	r, st := newSetup(t)
	ctx := context.Background()
	st.CreateProject(ctx, &state.Project{Base: state.Base{ID: "p"}})
	w := &state.Workload{
		Base:        state.Base{ID: "w", ProjectID: "p"},
		Env:         map[string]string{"BASE": "x"},
		ConfigMapRefs: []state.ConfigRef{{Name: "cm"}},
		SecretRefs:    []state.SecretRef{{Name: "sec"}},
	}
	// Create a configmap and a secret manually.
	cm := &state.ConfigMap{Base: state.Base{ID: "cm", ProjectID: "p"}, Data: map[string]string{"FROM_CM": "v1"}}
	st.CreateConfigMap(ctx, cm)
	ct, nn, err := r.cipher.Encrypt([]byte(`{"FROM_SECRET":"v2"}`))
	if err != nil {
		t.Fatal(err)
	}
	sec := &state.Secret{Base: state.Base{ID: "sec", ProjectID: "p"}, Ciphertext: ct, Nonce: nn}
	st.CreateSecret(ctx, sec)
	out, err := r.ResolveEnv(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	if out["BASE"] != "x" || out["FROM_CM"] != "v1" || out["FROM_SECRET"] != "v2" {
		t.Fatalf("got %+v", out)
	}
}
