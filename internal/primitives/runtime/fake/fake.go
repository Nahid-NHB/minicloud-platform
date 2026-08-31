// Package fake provides an in-memory Runtime used by tests and as a
// last-resort fallback when no real OCI runtime is present on the host.
package fake

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

	rt "github.com/minicloud/platform/internal/primitives/runtime"
)

// Fake is an in-memory Runtime.
type Fake struct {
	mu     sync.RWMutex
	images map[string]bool
	conts  map[string]*fakeContainer
	logs   map[string]*bytes.Buffer
	stats  map[string]rt.Stats
}

type fakeContainer struct {
	cont rt.Container
	stop chan struct{}
	done chan struct{}
}

// New creates an empty fake runtime.
func New() *Fake {
	return &Fake{
		images: map[string]bool{},
		conts:  map[string]*fakeContainer{},
		logs:   map[string]*bytes.Buffer{},
		stats:  map[string]rt.Stats{},
	}
}

func (f *Fake) Pull(ctx context.Context, image string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images[image] = true
	return nil
}

func (f *Fake) Create(ctx context.Context, s rt.Spec) (*rt.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.images[s.Image] {
		// Auto-pull so callers don't need to.
		f.images[s.Image] = true
	}
	c := &rt.Container{
		ID:      s.ID,
		Name:    s.Name,
		Status:  rt.StatusCreating,
		Image:   s.Image,
		Created: time.Now().UTC(),
	}
	f.conts[s.ID] = &fakeContainer{cont: *c, stop: make(chan struct{}), done: make(chan struct{})}
	f.logs[s.ID] = &bytes.Buffer{}
	f.stats[s.ID] = rt.Stats{}
	return c, nil
}

func (f *Fake) Start(ctx context.Context, id string) error {
	f.mu.Lock()
	c, ok := f.conts[id]
	if !ok {
		f.mu.Unlock()
		return rt.ErrNotFound
	}
	c.cont.Status = rt.StatusRunning
	c.cont.Started = time.Now().UTC()
	c.cont.PID = len(f.conts)*100 + 1
	f.mu.Unlock()
	go func() {
		close(c.done)
		<-c.stop
		f.mu.Lock()
		c.cont.Status = rt.StatusExited
		c.cont.Exited = time.Now().UTC()
		c.cont.Exit = 0
		f.mu.Unlock()
	}()
	// Simulate some resource usage.
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-t.C:
				f.mu.Lock()
				cur := f.stats[id]
				cur.CPUUsageMilli += 50
				cur.MemUsageBytes += 1024 * 1024
				f.stats[id] = cur
				f.mu.Unlock()
			}
		}
	}()
	return nil
}

func (f *Fake) Stop(ctx context.Context, id string, grace time.Duration) error {
	f.mu.Lock()
	c, ok := f.conts[id]
	if !ok {
		f.mu.Unlock()
		return rt.ErrNotFound
	}
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	f.mu.Unlock()
	return nil
}

func (f *Fake) Remove(ctx context.Context, id string, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.conts[id]
	if !ok {
		return rt.ErrNotFound
	}
	if c.cont.Status == rt.StatusRunning && !force {
		return errors.New("runtime: container still running")
	}
	delete(f.conts, id)
	delete(f.logs, id)
	delete(f.stats, id)
	return nil
}

func (f *Fake) List(ctx context.Context, all bool) ([]*rt.Container, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*rt.Container, 0, len(f.conts))
	for _, c := range f.conts {
		if !all && c.cont.Status != rt.StatusRunning {
			continue
		}
		cp := c.cont
		out = append(out, &cp)
	}
	return out, nil
}

func (f *Fake) Get(ctx context.Context, id string) (*rt.Container, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	c, ok := f.conts[id]
	if !ok {
		return nil, rt.ErrNotFound
	}
	cp := c.cont
	return &cp, nil
}

func (f *Fake) Logs(ctx context.Context, id string, follow bool, tail int) (io.ReadCloser, error) {
	f.mu.RLock()
	buf, ok := f.logs[id]
	f.mu.RUnlock()
	if !ok {
		return nil, rt.ErrNotFound
	}
	out := io.NopCloser(bytes.NewReader(buf.Bytes()))
	if follow {
		// For simplicity return the buffered content only.
		_ = tail
	}
	return out, nil
}

func (f *Fake) Exec(ctx context.Context, id string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	f.mu.RLock()
	_, ok := f.conts[id]
	f.mu.RUnlock()
	if !ok {
		return 1, rt.ErrNotFound
	}
	// "exec" simply echoes the command into the stdout writer and the
	// logs buffer so consumers can verify it ran.
	if stdout != nil {
		stdout.Write([]byte("$ " + joinCmd(cmd) + "\n"))
	}
	f.mu.Lock()
	if buf, ok := f.logs[id]; ok {
		buf.WriteString("$ " + joinCmd(cmd) + "\n")
	}
	f.mu.Unlock()
	return 0, nil
}

func (f *Fake) Stats(ctx context.Context, id string) (rt.Stats, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s, ok := f.stats[id]
	if !ok {
		return rt.Stats{}, rt.ErrNotFound
	}
	return s, nil
}

// AppendLog allows tests to inject log content.
func (f *Fake) AppendLog(id, line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if buf, ok := f.logs[id]; ok {
		buf.WriteString(line)
		if len(line) > 0 && line[len(line)-1] != '\n' {
			buf.WriteString("\n")
		}
	}
}

func joinCmd(cmd []string) string {
	out := ""
	for i, s := range cmd {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}

// Compile-time guard.
var _ rt.Runtime = (*Fake)(nil)
