package llm

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Registry tracks registered models and their backing engines.
type Registry struct {
	mu      sync.RWMutex
	models  map[string]*Model
	engines map[string]Engine
}

type Model struct {
	Name      string
	Revision  string
	Backend   string
	CreatedAt time.Time
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		models:  map[string]*Model{},
		engines: map[string]Engine{},
	}
}

// Register adds a model to the registry.
func (r *Registry) Register(m Model, eng Engine) error {
	if m.Name == "" {
		return errors.New("llm: model name required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m.CreatedAt = time.Now().UTC()
	r.models[m.Name] = &m
	if eng != nil {
		r.engines[m.Name] = eng
	}
	return nil
}

// Resolve returns the engine for the given model. If none registered,
// it falls back to the tiny engine so OpenAI-compat queries always work.
func (r *Registry) Resolve(name string) Engine {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.engines[name]; ok {
		return e
	}
	return &TinyEngine{}
}

// List returns registered models.
func (r *Registry) List() []Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Model, 0, len(r.models))
	for _, m := range r.models {
		out = append(out, *m)
	}
	return out
}

// Get returns one model.
func (r *Registry) Get(name string) (Model, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[name]
	if !ok {
		return Model{}, false
	}
	return *m, true
}

// Chat is a convenience pass-through.
func (r *Registry) Chat(ctx context.Context, req ChatRequest) (*Response, error) {
	return r.Resolve(req.Model).Chat(ctx, req)
}
