// Package volume implements a CSI-lite persistent volume manager
// on top of the platform state store.
//
// Each volume is a logical entity tracked in the KV; the actual
// payload lives in a file on the agent's data directory. The package
// supports create/attach/detach/delete and snapshots (copy-on-write).
package volume

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/minicloud/platform/internal/state"
)

// Manager owns the on-disk volume files.
type Manager struct {
	store *state.Store
	root  string
	mu    sync.Mutex
}

// Config configures a Manager.
type Config struct {
	Store   *state.Store
	RootDir string
}

// New builds a Manager.
func New(cfg Config) (*Manager, error) {
	if cfg.RootDir == "" {
		cfg.RootDir = filepath.Join(os.TempDir(), "minicloud-volumes")
	}
	if err := os.MkdirAll(cfg.RootDir, 0o755); err != nil {
		return nil, err
	}
	return &Manager{store: cfg.Store, root: cfg.RootDir}, nil
}

// Create allocates a new persistent volume backed by an empty file
// of the requested size.
func (m *Manager) Create(ctx context.Context, projectID, id string, sizeBytes int64, nodeID string) error {
	if sizeBytes <= 0 {
		return errors.New("volume: size must be positive")
	}
	v := &state.Volume{
		Base:      state.Base{ID: id, ProjectID: projectID, Name: id},
		SizeBytes: sizeBytes,
		NodeID:    nodeID,
		Status:    "Available",
	}
	if err := m.store.CreateVolume(ctx, v); err != nil {
		return err
	}
	// Touch the underlying file with the requested size.
	m.mu.Lock()
	defer m.mu.Unlock()
	path := m.path(projectID, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(sizeBytes); err != nil {
		return err
	}
	return nil
}

// Attach moves the volume into InUse on the requested node.
func (m *Manager) Attach(ctx context.Context, projectID, id, nodeID string) error {
	v, err := m.store.GetVolume(ctx, projectID, id)
	if err != nil {
		return err
	}
	v.NodeID = nodeID
	v.Status = "InUse"
	return m.store.UpdateVolume(ctx, v)
}

// Detach marks the volume Available.
func (m *Manager) Detach(ctx context.Context, projectID, id string) error {
	v, err := m.store.GetVolume(ctx, projectID, id)
	if err != nil {
		return err
	}
	v.Status = "Available"
	return m.store.UpdateVolume(ctx, v)
}

// Delete removes the volume's file and metadata.
func (m *Manager) Delete(ctx context.Context, projectID, id string) error {
	if err := m.store.DeleteVolume(ctx, projectID, id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return os.Remove(m.path(projectID, id))
}

// Read returns a reader on the volume's data file.
func (m *Manager) Read(ctx context.Context, projectID, id string) (io.ReadCloser, error) {
	if _, err := m.store.GetVolume(ctx, projectID, id); err != nil {
		return nil, err
	}
	return os.Open(m.path(projectID, id))
}

// Snapshot creates a logical copy of the volume's data file at a given
// point in time. Returns the snapshot ID.
func (m *Manager) Snapshot(ctx context.Context, projectID, volID, snapID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.path(projectID, volID)
	dst := m.path(projectID, snapID)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("volume: source missing: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	// Note: a VolumeSnapshot record could be persisted here if the
	// store exposes a CreateVolumeSnapshot method.
	_ = snapID
	return nil
}

// path returns the on-disk path for a volume.
func (m *Manager) path(projectID, id string) string {
	return filepath.Join(m.root, projectID, id+".vol")
}

func fiSize(f *os.File) int64 {
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	return fi.Size()
}
