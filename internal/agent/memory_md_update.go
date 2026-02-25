package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"test_skill_agent/internal/memory"
)

const memoryMDUpdateTimeout = 2 * time.Minute

func (a *Agent) updateMemoryMDAfterTurn(runID string, runTitle string, userRequest string, assistantFinal string, toolRecords []memory.ToolRecord) (memory.MemoryMDUpdateResponse, string, error) {
	if a == nil || a.Client == nil {
		return memory.MemoryMDUpdateResponse{}, "", errors.New("agent client is nil")
	}

	cfg, err := memory.LoadConfig(a.ConfigPath)
	if err != nil {
		cfg = memory.DefaultConfig().WithDefaults()
	}
	cwd, _ := os.Getwd()
	paths, err := memory.ResolvePaths(cfg, cwd)
	if err != nil {
		return memory.MemoryMDUpdateResponse{}, "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), memoryMDUpdateTimeout)
	defer cancel()

	resp, err := memory.UpdateMemoryMDFromTurn(ctx, a.Client, cfg, paths.RootDir, memory.MemoryMDUpdateInput{
		RunID:          strings.TrimSpace(runID),
		RunTitle:       strings.TrimSpace(runTitle),
		UserRequest:    userRequest,
		AssistantFinal: assistantFinal,
		ToolRecords:    toolRecords,
		Now:            time.Now().UTC(),
	})
	if err != nil {
		errText := strings.TrimSpace(err.Error())
		errText, _ = memory.RedactText(cfg, errText)
		appendMemoryMDUpdateLog(paths.RootDir, fmt.Sprintf("%s run_id=%s status=error step=update_memory_md error=%s\n", time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(runID), errText))
		return resp, paths.RootDir, fmt.Errorf("MEMORY.md update failed: %s", errText)
	}

	return resp, paths.RootDir, nil
}

func appendMemoryMDUpdateLog(root string, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	logPath := ""
	if strings.TrimSpace(root) != "" {
		logPath = filepath.Join(root, "index", "memory_md_update.log")
	} else {
		logPath = "memory_md_update.log"
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}
