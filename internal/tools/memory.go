package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/memory"
)

type MemorySearchTool struct {
	ConfigPath string
}

type memorySearchArgs struct {
	Query      string   `json:"query"`
	MaxResults int      `json:"max_results"`
	MinScore   *float64 `json:"min_score"`
}

func (t *MemorySearchTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "memory_search",
			Description: "Search long-term durable memory across sessions (Markdown files under the memory root). Returns small snippets with path + line numbers. Safe: only reads within the memory directory.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query (required).",
					},
					"max_results": map[string]any{
						"type":        "integer",
						"description": "Maximum results to return (default: memory.max_results or 10).",
					},
					"min_score": map[string]any{
						"type":        "number",
						"description": "Minimum score threshold (may be ignored by some backends).",
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
	}
}

func (t *MemorySearchTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in memorySearchArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", errors.New("query is required")
	}

	cfg, err := memory.LoadConfig(t.ConfigPath)
	if err != nil {
		return "", err
	}
	cwd, _ := os.Getwd()
	paths, err := memory.ResolvePaths(cfg, cwd)
	if err != nil {
		return "", err
	}

	resp, err := memory.Search(ctx, cfg, paths.RootDir, in.Query, in.MaxResults)
	if err != nil {
		return "", err
	}
	if in.MinScore != nil && *in.MinScore > 0 && len(resp.Results) > 0 {
		minScore := *in.MinScore
		if minScore > 1 {
			minScore = 1
		}
		filtered := resp.Results[:0]
		for _, r := range resp.Results {
			if r.Score >= minScore {
				filtered = append(filtered, r)
			}
		}
		resp.Results = filtered
	}
	resp.ProjectKey = paths.ProjectKey
	out, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

type MemoryGetTool struct {
	ConfigPath string
}

type memoryGetArgs struct {
	Path  string `json:"path"`
	From  int    `json:"from"`
	Lines int    `json:"lines"`
}

func (t *MemoryGetTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "memory_get",
			Description: "Read a slice of a memory Markdown file (path is relative to the memory root). Safe: only reads within the memory directory and rejects symlinks.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Relative path like \"daily/2026-02-24.md\" or \"MEMORY.md\" (required).",
					},
					"from": map[string]any{
						"type":        "integer",
						"description": "Start line number (1-based, default: 1).",
					},
					"lines": map[string]any{
						"type":        "integer",
						"description": "Number of lines to read (default: 50; max: 200).",
					},
				},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		},
	}
}

func (t *MemoryGetTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in memoryGetArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Path) == "" {
		return "", errors.New("path is required")
	}

	cfg, err := memory.LoadConfig(t.ConfigPath)
	if err != nil {
		return "", err
	}
	cwd, _ := os.Getwd()
	paths, err := memory.ResolvePaths(cfg, cwd)
	if err != nil {
		return "", err
	}

	resp, err := memory.Get(ctx, paths.RootDir, in.Path, in.From, in.Lines)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

type MemoryAppendTool struct {
	ConfigPath string
}

type memoryAppendArgs struct {
	Kind   string   `json:"kind"`
	Text   string   `json:"text"`
	Tags   []string `json:"tags"`
	Source string   `json:"source"`
}

func (t *MemoryAppendTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "memory_append",
			Description: "Append a durable note to today's long-term memory daily file. Automatically redacts common secrets. Safe: only writes within the memory directory and rejects symlinks.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind": map[string]any{
						"type":        "string",
						"description": "Category (examples: pref, decision, todo, note). Default: note.",
					},
					"text": map[string]any{
						"type":        "string",
						"description": "The durable content to remember (required). Do NOT include secrets.",
					},
					"tags": map[string]any{
						"type":        "array",
						"description": "Optional tags like [\"prefs\",\"arch\"]. Will be written as #prefs #arch.",
						"items":       map[string]any{"type": "string"},
					},
					"source": map[string]any{
						"type":        "string",
						"description": "Optional source hint (e.g. run_id, issue id).",
					},
				},
				"required":             []string{"text"},
				"additionalProperties": false,
			},
		},
	}
}

func (t *MemoryAppendTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in memoryAppendArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Text) == "" {
		return "", errors.New("text is required")
	}

	cfg, err := memory.LoadConfig(t.ConfigPath)
	if err != nil {
		return "", err
	}
	cwd, _ := os.Getwd()
	paths, err := memory.ResolvePaths(cfg, cwd)
	if err != nil {
		return "", err
	}

	resp, err := memory.AppendDaily(ctx, cfg, paths.RootDir, in.Kind, in.Text, in.Tags, in.Source, time.Time{})
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

type MemoryFlushTool struct {
	ConfigPath string
}

type memoryFlushArgs struct {
	Text     string `json:"text"`
	Source   string `json:"source"`
	MaxItems int    `json:"max_items"`
	Auto     bool   `json:"auto"`
}

func (t *MemoryFlushTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "memory_flush",
			Description: "Extract durable notes (preferences/decisions/TODOs) from a summary/transcript and persist them to long-term memory (daily/YYYY-MM-DD.md). Automatically redacts common secrets. Safe: only writes within the memory directory and rejects symlinks.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "Summary/transcript text to extract durable notes from (required).",
					},
					"source": map[string]any{
						"type":        "string",
						"description": "Optional source hint (e.g. run_id).",
					},
					"max_items": map[string]any{
						"type":        "integer",
						"description": "Maximum extracted items to persist (default: 12; max: 40).",
					},
					"auto": map[string]any{
						"type":        "boolean",
						"description": "If true, respects config.memory.auto_flush_on_compaction (disabled => no-op).",
					},
				},
				"required":             []string{"text"},
				"additionalProperties": false,
			},
		},
	}
}

func (t *MemoryFlushTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in memoryFlushArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Text) == "" {
		return "", errors.New("text is required")
	}

	cfg, err := memory.LoadConfig(t.ConfigPath)
	if err != nil {
		return "", err
	}
	if in.Auto && cfg.AutoFlushOnCompaction != nil && !*cfg.AutoFlushOnCompaction {
		out, err := json.Marshal(memory.FlushResponse{Disabled: true, Backend: "disabled"})
		if err != nil {
			return "", nil
		}
		return string(out), nil
	}

	cwd, _ := os.Getwd()
	paths, err := memory.ResolvePaths(cfg, cwd)
	if err != nil {
		return "", err
	}

	resp, err := memory.FlushFromText(ctx, cfg, paths.RootDir, in.Text, in.MaxItems, in.Source, time.Time{})
	if err != nil {
		return "", err
	}
	resp.Root = paths.RootDir
	out, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
