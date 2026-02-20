package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"test_skill_agent/internal/llm"
)

type Tool interface {
	Definition() llm.ToolDefinition
	Call(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	name := t.Definition().Function.Name
	r.mu.Lock()
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	r.tools[name] = t
	r.mu.Unlock()
}

func (r *Registry) Unregister(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.tools, name)
	r.mu.Unlock()
}

func (r *Registry) UnregisterMany(names []string) {
	if r == nil || len(names) == 0 {
		return
	}
	r.mu.Lock()
	for _, name := range names {
		delete(r.tools, name)
	}
	r.mu.Unlock()
}

func (r *Registry) Definitions() []llm.ToolDefinition {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	defs := make([]llm.ToolDefinition, 0, len(names))
	for _, name := range names {
		t := r.tools[name]
		defs = append(defs, t.Definition())
	}
	r.mu.RUnlock()
	return defs
}

func (r *Registry) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if r == nil {
		return "", fmt.Errorf("tool registry is nil")
	}
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.Call(ctx, args)
}
