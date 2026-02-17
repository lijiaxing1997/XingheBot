package tools

import (
    "context"
    "encoding/json"
    "fmt"

    "test_skill_agent/internal/llm"
)

type Tool interface {
    Definition() llm.ToolDefinition
    Call(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
    tools map[string]Tool
}

func NewRegistry() *Registry {
    return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
    if r.tools == nil {
        r.tools = make(map[string]Tool)
    }
    r.tools[t.Definition().Function.Name] = t
}

func (r *Registry) Definitions() []llm.ToolDefinition {
    if r == nil {
        return nil
    }
    defs := make([]llm.ToolDefinition, 0, len(r.tools))
    for _, t := range r.tools {
        defs = append(defs, t.Definition())
    }
    return defs
}

func (r *Registry) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
    if r == nil {
        return "", fmt.Errorf("tool registry is nil")
    }
    t, ok := r.tools[name]
    if !ok {
        return "", fmt.Errorf("unknown tool: %s", name)
    }
    return t.Call(ctx, args)
}
