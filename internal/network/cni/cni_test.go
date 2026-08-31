package cni

import (
	"testing"

	"github.com/minicloud/platform/internal/state"
)

func TestEnsureNetwork(t *testing.T) {
	m := NewManager()
	n := &state.Network{Base: state.Base{ID: "p1", ProjectID: "p1"}, CIDR: "10.0.0.0/24"}
	if err := m.Ensure(n); err != nil {
		t.Fatal(err)
	}
	got, ok := m.Network("p1")
	if !ok || got.CIDR != "10.0.0.0/24" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	// Attach without ip binary should not error.
	if err := m.Attach("p1", "c1", "veth1"); err != nil {
		t.Fatalf("attach: %v", err)
	}
}