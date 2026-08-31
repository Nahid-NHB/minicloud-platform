// Package runtime is a thin abstraction over a container runtime.
//
// It defines a minimal CRI-like surface: Pull, Create, Start, Stop,
// Remove, Exec, Logs, List. Two implementations are provided:
//
//   - process: spawns the container image as a child process (works for
//     Linux hosts where the operator runs the workload's command
//     directly). Useful for the single-node MVP and CI.
//   - fake:    an in-memory implementation used by tests and for nodes
//     that have no real container runtime installed.
//
// A production deployment can swap in a runc or containerd client by
// implementing the Runtime interface.
package runtime

import (
	"context"
	"errors"
	"io"
	"time"
)

// Spec is the container spec the runtime needs to launch a workload.
type Spec struct {
	ID         string
	Name       string
	Image      string
	Command    []string
	Args       []string
	Env        []string
	WorkingDir string
	Labels     map[string]string
	CPUQuota   int64 // millicores
	Memory     int64 // bytes
	Mounts     []Mount
	Ports      []Port
	Network    string
}

// Mount describes a host-to-container bind or volume.
type Mount struct {
	HostPath  string
	MountPath string
	ReadOnly  bool
}

// Port describes a port mapping.
type Port struct {
	ContainerPort int32
	HostPort      int32
	Protocol      string
}

// Status represents the runtime state of a container.
type Status string

const (
	StatusCreating Status = "creating"
	StatusRunning  Status = "running"
	StatusExited   Status = "exited"
	StatusFailed   Status = "failed"
	StatusUnknown  Status = "unknown"
)

// Container is a runtime-side container handle.
type Container struct {
	ID      string
	Name    string
	Status  Status
	Image   string
	Created time.Time
	Started time.Time
	Exited  time.Time
	Exit    int
	// Annotations are runtime-specific data (PID, OCI spec, etc).
	PID int
}

// Runtime is the platform-facing interface.
type Runtime interface {
	// Pull fetches an image into the runtime's local store.
	Pull(ctx context.Context, image string) error
	// Create provisions a container.
	Create(ctx context.Context, s Spec) (*Container, error)
	// Start launches a container.
	Start(ctx context.Context, id string) error
	// Stop terminates a container with the given grace period.
	Stop(ctx context.Context, id string, grace time.Duration) error
	// Remove deletes a container's state.
	Remove(ctx context.Context, id string, force bool) error
	// List returns all containers (running and stopped).
	List(ctx context.Context, all bool) ([]*Container, error)
	// Get returns a single container.
	Get(ctx context.Context, id string) (*Container, error)
	// Logs streams logs. If follow is true the call blocks until the
	// container exits or the context is canceled.
	Logs(ctx context.Context, id string, follow bool, tail int) (io.ReadCloser, error)
	// Exec runs a command inside the container.
	Exec(ctx context.Context, id string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
	// Stats returns CPU and memory usage snapshots.
	Stats(ctx context.Context, id string) (Stats, error)
}

// Stats is a runtime resource usage snapshot.
type Stats struct {
	CPUUsageMilli int64
	MemUsageBytes int64
	NetBytes      int64
}

// ErrNotFound is returned when a container doesn't exist.
var ErrNotFound = errors.New("runtime: container not found")
