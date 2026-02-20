package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/util"
)

type ListFilesTool struct{}

type listFilesArgs struct {
	Path          string `json:"path"`
	Recursive     bool   `json:"recursive"`
	MaxEntries    int    `json:"max_entries"`
	IncludeHidden bool   `json:"include_hidden"`
}

func (t *ListFilesTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "list_files",
			Description: "List files under a path. Supports recursive listing and hidden files.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory to list (default: .)",
					},
					"recursive": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether to recursively list subdirectories",
					},
					"max_entries": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of entries to return (default: 2000)",
					},
					"include_hidden": map[string]interface{}{
						"type":        "boolean",
						"description": "Include hidden files and directories",
					},
				},
			},
		},
	}
}

func (t *ListFilesTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in listFilesArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}
	if in.Path == "" {
		in.Path = "."
	}
	if in.MaxEntries <= 0 {
		in.MaxEntries = 2000
	}

	results := make([]string, 0, 128)
	root := in.Path

	if in.Recursive {
		var stopErr = errors.New("max entries reached")
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == root {
				return nil
			}
			name := d.Name()
			if !in.IncludeHidden && strings.HasPrefix(name, ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if d.IsDir() {
				rel += string(os.PathSeparator)
			}
			results = append(results, rel)
			if len(results) >= in.MaxEntries {
				return stopErr
			}
			return nil
		})
		if err != nil && !errors.Is(err, stopErr) {
			return "", err
		}
	} else {
		entries, err := os.ReadDir(root)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !in.IncludeHidden && strings.HasPrefix(name, ".") {
				continue
			}
			if entry.IsDir() {
				name += string(os.PathSeparator)
			}
			results = append(results, name)
			if len(results) >= in.MaxEntries {
				break
			}
		}
	}

	if len(results) == 0 {
		return "(no entries)", nil
	}
	return strings.Join(results, "\n"), nil
}

type ReadFileTool struct{}

type readFileArgs struct {
	Path            string `json:"path"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	WithLineNumbers bool   `json:"with_line_numbers"`
}

func (t *ReadFileTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "read_file",
			Description: "Read a file. Supports line ranges and optional line numbers.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":              map[string]interface{}{"type": "string"},
					"start_line":        map[string]interface{}{"type": "integer"},
					"end_line":          map[string]interface{}{"type": "integer"},
					"with_line_numbers": map[string]interface{}{"type": "boolean"},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (t *ReadFileTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in readFileArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.Path == "" {
		return "", errors.New("path is required")
	}
	data, err := os.ReadFile(in.Path)
	if err != nil {
		return "", err
	}
	content := string(data)
	lines := splitLines(content)
	if len(lines) == 0 {
		return "", nil
	}

	start := in.StartLine
	end := in.EndLine
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end || start > len(lines) {
		return "", fmt.Errorf("invalid line range: %d-%d", start, end)
	}

	var b strings.Builder
	for i := start; i <= end; i++ {
		line := lines[i-1]
		if in.WithLineNumbers {
			fmt.Fprintf(&b, "%d: %s", i, line)
		} else {
			b.WriteString(line)
		}
		if i != end {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

type WriteFileTool struct{}

type writeFileArgs struct {
	Path         string   `json:"path"`
	Content      *string  `json:"content"`
	ContentLines []string `json:"content_lines"`
	CreateDirs   bool     `json:"create_dirs"`
	Append       bool     `json:"append"`
}

func (t *WriteFileTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "write_file",
			Description: "Write content to a file.\n\nIMPORTANT: tool arguments MUST be valid JSON. Do NOT output raw file content outside the JSON object.\n\nIf you see errors like:\n- \"unexpected end of JSON input\": your tool arguments were truncated; reduce chunk size and/or increase max_tokens.\n- \"invalid character '#'\": you likely sent raw code instead of a JSON object; wrap args in {\"path\":...,\"content\":...}.\n\nFor large files: write in multiple calls (first append=false, then append=true) to avoid truncation.\n\n大文件建议：分片多次调用（首次 append=false，后续 append=true），避免输出被截断导致 JSON 不完整。",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Raw text content as a JSON string (must be JSON-escaped: use \\n, \\t, \\\" etc). Provide exactly one of content or content_lines. For large content, split into multiple write_file calls with append=true after the first chunk. Example: {\"path\":\"foo.py\",\"content\":\"line1\\nline2\",\"append\":false}.",
					},
					"content_lines": map[string]interface{}{
						"type":        "array",
						"description": "Array of lines joined with \"\\n\". Helps avoid JSON escaping issues.",
						"items":       map[string]interface{}{"type": "string"},
					},
					"create_dirs": map[string]interface{}{"type": "boolean"},
					"append": map[string]interface{}{
						"type":        "boolean",
						"description": "Append instead of overwrite. Use append=true for chunks after the first one.",
					},
				},
				"additionalProperties": false,
				"required":             []string{"path"},
			},
		},
	}
}

func (t *WriteFileTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in writeFileArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("write_file: invalid JSON arguments (possibly truncated or missing JSON wrapper). Args MUST be a single JSON object like {\"path\":\"...\",\"content\":\"...\"}. For large content, split into multiple calls. Example: first {\"path\":\"...\",\"content\":\"...\",\"append\":false}, then {\"path\":\"...\",\"content\":\"...\",\"append\":true}. Parse error: %w", err)
	}
	if in.Path == "" {
		return "", errors.New("path is required")
	}
	contentSources := 0
	if in.Content != nil {
		contentSources++
	}
	if in.ContentLines != nil {
		contentSources++
	}
	if contentSources == 0 {
		return "", errors.New("content is required: provide content or content_lines")
	}
	if contentSources > 1 {
		return "", errors.New("provide only one of content or content_lines")
	}
	if in.CreateDirs {
		if err := util.EnsureParentDir(in.Path); err != nil {
			return "", err
		}
	}
	var data []byte
	switch {
	case in.ContentLines != nil:
		data = []byte(strings.Join(in.ContentLines, "\n"))
	case in.Content != nil:
		data = []byte(*in.Content)
	}

	if in.Append {
		file, err := os.OpenFile(in.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return "", err
		}
		defer file.Close()

		written := 0
		for written < len(data) {
			n, err := file.Write(data[written:])
			written += n
			if err != nil {
				return "", err
			}
			if n == 0 {
				return "", errors.New("short write")
			}
		}
		return "ok", nil
	}

	// Atomic-ish overwrite: write a temp file in the same directory, then rename.
	dir := filepath.Dir(in.Path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".write_file_*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return "", err
	}

	written := 0
	for written < len(data) {
		n, err := tmp.Write(data[written:])
		written += n
		if err != nil {
			_ = tmp.Close()
			return "", err
		}
		if n == 0 {
			_ = tmp.Close()
			return "", errors.New("short write")
		}
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, in.Path); err != nil {
		return "", err
	}
	return "ok", nil
}

type EditFileTool struct{}

type editFileArgs struct {
	Path  string       `json:"path"`
	Edits []editChange `json:"edits"`
}

type editChange struct {
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *EditFileTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "edit_file",
			Description: "Edit a file by replacing text. Applies edits in order.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
					"edits": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"old_text":    map[string]interface{}{"type": "string"},
								"new_text":    map[string]interface{}{"type": "string"},
								"replace_all": map[string]interface{}{"type": "boolean"},
							},
							"required": []string{"old_text", "new_text"},
						},
					},
				},
				"required": []string{"path", "edits"},
			},
		},
	}
}

func (t *EditFileTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in editFileArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.Path == "" {
		return "", errors.New("path is required")
	}
	if len(in.Edits) == 0 {
		return "", errors.New("edits are required")
	}
	data, err := os.ReadFile(in.Path)
	if err != nil {
		return "", err
	}
	content := string(data)
	for _, edit := range in.Edits {
		if edit.ReplaceAll {
			content = strings.ReplaceAll(content, edit.OldText, edit.NewText)
			continue
		}
		idx := strings.Index(content, edit.OldText)
		if idx < 0 {
			return "", fmt.Errorf("old_text not found")
		}
		content = content[:idx] + edit.NewText + content[idx+len(edit.OldText):]
	}
	if err := os.WriteFile(in.Path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return "ok", nil
}

type MoveFileTool struct{}

type moveFileArgs struct {
	Src       string `json:"src"`
	Dest      string `json:"dest"`
	Overwrite bool   `json:"overwrite"`
}

func (t *MoveFileTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "move_file",
			Description: "Move or rename a file or directory.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"src":       map[string]interface{}{"type": "string"},
					"dest":      map[string]interface{}{"type": "string"},
					"overwrite": map[string]interface{}{"type": "boolean"},
				},
				"required": []string{"src", "dest"},
			},
		},
	}
}

func (t *MoveFileTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in moveFileArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.Src == "" || in.Dest == "" {
		return "", errors.New("src and dest are required")
	}
	if err := util.Move(in.Src, in.Dest, in.Overwrite); err != nil {
		return "", err
	}
	return "ok", nil
}

type CopyFileTool struct{}

type copyFileArgs struct {
	Src       string `json:"src"`
	Dest      string `json:"dest"`
	Overwrite bool   `json:"overwrite"`
}

func (t *CopyFileTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "copy_file",
			Description: "Copy a file or directory.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"src":       map[string]interface{}{"type": "string"},
					"dest":      map[string]interface{}{"type": "string"},
					"overwrite": map[string]interface{}{"type": "boolean"},
				},
				"required": []string{"src", "dest"},
			},
		},
	}
}

func (t *CopyFileTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in copyFileArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.Src == "" || in.Dest == "" {
		return "", errors.New("src and dest are required")
	}
	info, err := os.Stat(in.Src)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		if err := util.CopyDir(in.Src, in.Dest, in.Overwrite); err != nil {
			return "", err
		}
		return "ok", nil
	}
	if err := util.CopyFile(in.Src, in.Dest, in.Overwrite); err != nil {
		return "", err
	}
	return "ok", nil
}

type DeleteFileTool struct{}

type deleteFileArgs struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

func (t *DeleteFileTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "delete_file",
			Description: "Delete a file or directory.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":      map[string]interface{}{"type": "string"},
					"recursive": map[string]interface{}{"type": "boolean"},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (t *DeleteFileTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in deleteFileArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.Path == "" {
		return "", errors.New("path is required")
	}
	if in.Recursive {
		if err := os.RemoveAll(in.Path); err != nil {
			return "", err
		}
		return "ok", nil
	}
	if err := os.Remove(in.Path); err != nil {
		return "", err
	}
	return "ok", nil
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	lines := make([]string, 0, 128)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) == 0 && content != "" {
		return []string{content}
	}
	return lines
}
