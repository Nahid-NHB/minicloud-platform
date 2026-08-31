// Package config resolves workload environment variables from
// ConfigMaps and Secrets. The runtime container can call ResolveEnv
// with a workload and a master key, and receive a fully-substituted
// map[string]string that is safe to pass to a container process.
//
// This package is intentionally small and dependency-free so it can
// be reused from any binary (CLI, dashboard, node agent).
package config

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/minicloud/platform/internal/secret"
	"github.com/minicloud/platform/internal/state"
)

// Resolver resolves env vars from Secret + ConfigMap references.
type Resolver struct {
	store  *state.Store
	cipher *secret.Cipher
}

// New builds a Resolver.
func New(store *state.Store, c *secret.Cipher) *Resolver {
	return &Resolver{store: store, cipher: c}
}

// ResolveEnv returns the effective environment variables for the
// given workload: its declared Env, expanded by any ConfigMap data
// and decrypted from any Secret references.
func (r *Resolver) ResolveEnv(ctx context.Context, w *state.Workload) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range w.Env {
		out[k] = v
	}
	for _, ref := range w.ConfigMapRefs {
		cm, err := r.store.GetConfigMap(ctx, w.ProjectID, ref.Name)
		if err != nil {
			return nil, fmt.Errorf("config: configmap %s: %w", ref.Name, err)
		}
		for k, v := range cm.Data {
			out[k] = v
		}
	}
	for _, ref := range w.SecretRefs {
		sec, err := r.store.GetSecret(ctx, w.ProjectID, ref.Name)
		if err != nil {
			return nil, fmt.Errorf("config: secret %s: %w", ref.Name, err)
		}
		pt, err := r.cipher.Decrypt(sec.Ciphertext, sec.Nonce)
		if err != nil {
			return nil, fmt.Errorf("config: decrypt %s: %w", ref.Name, err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(pt, &m); err != nil {
			return nil, fmt.Errorf("config: decode %s: %w", ref.Name, err)
		}
		for k, v := range m {
			out[k] = v
		}
	}
	return out, nil
}
