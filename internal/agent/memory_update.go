package agent

import (
	"context"
	"os"
	"strings"
	"time"

	"test_skill_agent/internal/memory"
)

type memoryMDUpdateJob struct {
	RunID          string
	UserRequest    string
	AssistantFinal string
	ToolRecords    []memory.ToolRecord
}

func (a *Agent) queueMemoryMDUpdate(job memoryMDUpdateJob) {
	if a == nil || a.Client == nil {
		return
	}
	a.memoryUpdateMu.Lock()
	a.memoryUpdatePending = &job
	if a.memoryUpdateRunning {
		a.memoryUpdateMu.Unlock()
		return
	}
	a.memoryUpdateRunning = true
	a.memoryUpdateMu.Unlock()

	go a.runMemoryMDUpdateLoop()
}

func (a *Agent) runMemoryMDUpdateLoop() {
	if a == nil || a.Client == nil {
		return
	}
	for {
		a.memoryUpdateMu.Lock()
		job := a.memoryUpdatePending
		a.memoryUpdatePending = nil
		a.memoryUpdateMu.Unlock()

		if job == nil {
			a.memoryUpdateMu.Lock()
			a.memoryUpdateRunning = false
			a.memoryUpdateMu.Unlock()
			return
		}

		cfg, err := memory.LoadConfig(a.ConfigPath)
		if err != nil {
			continue
		}
		cwd, _ := os.Getwd()
		paths, err := memory.ResolvePaths(cfg, cwd)
		if err != nil {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		_, _ = memory.UpdateMemoryMDFromTurn(ctx, a.Client, cfg, paths.RootDir, memory.MemoryMDUpdateInput{
			RunID:          strings.TrimSpace(job.RunID),
			UserRequest:    job.UserRequest,
			AssistantFinal: job.AssistantFinal,
			ToolRecords:    job.ToolRecords,
			Now:            time.Now().UTC(),
		})
		cancel()
	}
}
