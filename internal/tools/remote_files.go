package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"test_skill_agent/internal/cluster"
	"test_skill_agent/internal/llm"
)

func normalizeClusterFilesRelPath(raw string) string {
	p := strings.TrimSpace(raw)
	p = strings.Trim(p, `"'`)
	if p == "" {
		return ""
	}

	// Normalize separators for cross-platform inputs (Windows paths, etc.).
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")

	// Users/models sometimes include the root dir prefix or an absolute path.
	// remote_file_get expects a path relative to cluster.files.root_dir, so strip the common ".cluster/files" prefix.
	if idx := strings.LastIndex(p, "/.cluster/files/"); idx >= 0 {
		return strings.TrimPrefix(p[idx+len("/.cluster/files/"):], "/")
	}
	if idx := strings.LastIndex(p, ".cluster/files/"); idx >= 0 {
		return strings.TrimPrefix(p[idx+len(".cluster/files/"):], "/")
	}
	if strings.HasPrefix(p, ".cluster/files/") {
		return strings.TrimPrefix(p, ".cluster/files/")
	}
	return p
}

type RemoteFilePutTool struct {
	Gateway *cluster.MasterGateway
}

type remoteFilePutArgs struct {
	Slave          string `json:"slave"`
	LocalPath      string `json:"local_path"`
	RemoteName     string `json:"remote_name"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (t *RemoteFilePutTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "remote_file_put",
			Description: "Send a local file from the master to a slave via WebSocket chunked transfer.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"slave":           map[string]any{"type": "string"},
					"local_path":      map[string]any{"type": "string"},
					"remote_name":     map[string]any{"type": "string"},
					"timeout_seconds": map[string]any{"type": "integer"},
				},
				"required": []string{"slave", "local_path"},
			},
		},
	}
}

func (t *RemoteFilePutTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Gateway == nil {
		return "", errors.New("master gateway is not configured")
	}
	var in remoteFilePutArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	slaveID := strings.TrimSpace(in.Slave)
	localPath := strings.TrimSpace(in.LocalPath)
	if slaveID == "" || localPath == "" {
		return "", errors.New("slave and local_path are required")
	}
	timeout := 15 * time.Minute
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}

	res, err := t.Gateway.SendFilePut(ctx, slaveID, localPath, strings.TrimSpace(in.RemoteName), timeout)
	if err != nil {
		status := "failed"
		if strings.Contains(strings.ToLower(err.Error()), "offline") {
			status = "offline"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = "timeout"
		}
		return prettyJSON(map[string]any{
			"slave_id":    slaveID,
			"transfer_id": strings.TrimSpace(res.TransferID),
			"status":      status,
			"error":       err.Error(),
			"remote_path": strings.TrimSpace(res.RemotePath),
			"ack":         res.Ack,
		})
	}

	out := map[string]any{
		"slave_id":    slaveID,
		"transfer_id": res.TransferID,
		"status":      strings.TrimSpace(res.Ack.Status),
		"size_bytes":  res.SizeBytes,
		"sha256":      strings.TrimSpace(res.SHA256),
		"remote_path": strings.TrimSpace(res.RemotePath),
		"ack":         res.Ack,
	}
	return prettyJSON(out)
}

type RemoteFileGetTool struct {
	Gateway *cluster.MasterGateway
}

type remoteFileGetArgs struct {
	Slave          string `json:"slave"`
	RemotePath     string `json:"remote_path"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (t *RemoteFileGetTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "remote_file_get",
			Description: "Fetch a file from a slave. remote_path MUST be relative to the slave's cluster.files.root_dir (default: .cluster/files). Use paths like outbox/report.txt (NOT .cluster/files/outbox/report.txt or an absolute path). The file is saved under the master's cluster.files.root_dir inbox.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"slave":           map[string]any{"type": "string"},
					"remote_path":     map[string]any{"type": "string"},
					"timeout_seconds": map[string]any{"type": "integer"},
				},
				"required": []string{"slave", "remote_path"},
			},
		},
	}
}

func (t *RemoteFileGetTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Gateway == nil {
		return "", errors.New("master gateway is not configured")
	}
	var in remoteFileGetArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	slaveID := strings.TrimSpace(in.Slave)
	remotePath := normalizeClusterFilesRelPath(in.RemotePath)
	if slaveID == "" || remotePath == "" {
		return "", errors.New("slave and remote_path are required")
	}
	timeout := 15 * time.Minute
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}

	res, err := t.Gateway.SendFileGet(ctx, slaveID, remotePath, timeout)
	if err != nil {
		status := "failed"
		if strings.Contains(strings.ToLower(err.Error()), "offline") {
			status = "offline"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			status = "timeout"
		}
		return prettyJSON(map[string]any{
			"slave_id":    slaveID,
			"transfer_id": strings.TrimSpace(res.TransferID),
			"status":      status,
			"error":       err.Error(),
			"saved_path":  strings.TrimSpace(res.Ack.SavedPath),
			"ack":         res.Ack,
		})
	}

	out := map[string]any{
		"slave_id":    slaveID,
		"transfer_id": res.TransferID,
		"status":      strings.TrimSpace(res.Ack.Status),
		"size_bytes":  res.SizeBytes,
		"sha256":      strings.TrimSpace(res.SHA256),
		"local_path":  strings.TrimSpace(res.LocalPath),
		"saved_path":  strings.TrimSpace(res.Ack.SavedPath),
		"ack":         res.Ack,
	}
	return prettyJSON(out)
}
