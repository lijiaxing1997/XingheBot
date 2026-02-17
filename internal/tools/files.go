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
                    "path": map[string]interface{}{"type": "string"},
                    "start_line": map[string]interface{}{"type": "integer"},
                    "end_line": map[string]interface{}{"type": "integer"},
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
    Path       string `json:"path"`
    Content    string `json:"content"`
    CreateDirs bool   `json:"create_dirs"`
}

func (t *WriteFileTool) Definition() llm.ToolDefinition {
    return llm.ToolDefinition{
        Type: "function",
        Function: llm.ToolFunctionDef{
            Name:        "write_file",
            Description: "Write content to a file, overwriting if it exists.",
            Parameters: map[string]interface{}{
                "type": "object",
                "properties": map[string]interface{}{
                    "path": map[string]interface{}{"type": "string"},
                    "content": map[string]interface{}{"type": "string"},
                    "create_dirs": map[string]interface{}{"type": "boolean"},
                },
                "required": []string{"path", "content"},
            },
        },
    }
}

func (t *WriteFileTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
    var in writeFileArgs
    if err := json.Unmarshal(args, &in); err != nil {
        return "", err
    }
    if in.Path == "" {
        return "", errors.New("path is required")
    }
    if in.CreateDirs {
        if err := util.EnsureParentDir(in.Path); err != nil {
            return "", err
        }
    }
    if err := os.WriteFile(in.Path, []byte(in.Content), 0o644); err != nil {
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
                                "old_text": map[string]interface{}{"type": "string"},
                                "new_text": map[string]interface{}{"type": "string"},
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
                    "src": map[string]interface{}{"type": "string"},
                    "dest": map[string]interface{}{"type": "string"},
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
                    "src": map[string]interface{}{"type": "string"},
                    "dest": map[string]interface{}{"type": "string"},
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
                    "path": map[string]interface{}{"type": "string"},
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
