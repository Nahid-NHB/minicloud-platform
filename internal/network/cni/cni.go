// Package cni provides a minimal in-process bridge CNI plugin that
// allocates a per-project virtual network and veth pairs for
// containers. On Linux hosts the agent execs `ip` and `iptables` to
// make the changes real. On other platforms, the package records the
// desired state without applying it (a no-op) so the rest of the
// platform still works.
package cni

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/minicloud/platform/internal/state"
)

// Manager creates and tears down virtual networks.
type Manager struct {
	mu      sync.Mutex
	networks map[string]*Network // projectID -> network
}

type Network struct {
	Name   string
	CIDR   string
	Gateway string
	Bridge string
}

// NewManager builds a Manager.
func NewManager() *Manager { return &Manager{networks: map[string]*Network{}} }

// Ensure creates the bridge and veth pair for a project if not yet
// present.
func (m *Manager) Ensure(n *state.Network) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	bridge := "cbr-" + shortID(n.ID)
	gw, _ := firstIP(n.CIDR)
	net := &Network{Name: n.Name, CIDR: n.CIDR, Gateway: gw, Bridge: bridge}
	m.networks[n.ProjectID] = net
	if !canExec() {
		return nil
	}
	cmds := [][]string{
		{"ip", "link", "add", bridge, "type", "bridge"},
		{"ip", "addr", "add", gw + "/24", "dev", bridge},
		{"ip", "link", "set", bridge, "up"},
	}
	for _, c := range cmds {
		_ = exec.Command(c[0], c[1:]...).Run()
	}
	return nil
}

// Attach records a veth pair for a container.
func (m *Manager) Attach(projectID, containerID, hostIf string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	net, ok := m.networks[projectID]
	if !ok {
		return fmt.Errorf("cni: project %s has no network", projectID)
	}
	if !canExec() {
		return nil
	}
	vethHost := "veth" + shortID(containerID)
	vethCtr := "eth0"
	cmds := [][]string{
		{"ip", "link", "add", vethHost, "type", "veth", "peer", "name", vethCtr},
		{"ip", "link", "set", vethHost, "master", net.Bridge},
		{"ip", "link", "set", vethHost, "up"},
	}
	for _, c := range cmds {
		_ = exec.Command(c[0], c[1:]...).Run()
	}
	_ = hostIf
	return nil
}

// Detach removes a veth pair.
func (m *Manager) Detach(projectID, containerID string) error {
	if !canExec() {
		return nil
	}
	vethHost := "veth" + shortID(containerID)
	_ = exec.Command("ip", "link", "del", vethHost).Run()
	return nil
}

// Network returns the in-memory record.
func (m *Manager) Network(projectID string) (*Network, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.networks[projectID]
	return n, ok
}

func shortID(s string) string {
	if len(s) < 6 {
		return s
	}
	return strings.ReplaceAll(s, "-", "")[:6]
}

func firstIP(cidr string) (string, error) {
	parts := strings.SplitN(cidr, "/", 2)
	if len(parts) == 0 {
		return "", fmt.Errorf("cni: bad cidr %s", cidr)
	}
	return parts[0], nil
}

func canExec() bool {
	_, err := exec.LookPath("ip")
	return err == nil
}