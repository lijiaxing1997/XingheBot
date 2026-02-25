package agent

import (
	"strings"

	"test_skill_agent/internal/memory"
)

type memoryMDUpdateJob struct {
	RunID          string
	RunTitle       string
	UserRequest    string
	AssistantFinal string
	ToolRecords    []memory.ToolRecord
}

func (a *Agent) queueMemoryMDUpdate(job memoryMDUpdateJob) {
	if a == nil || a.Client == nil {
		return
	}
	a.memoryMDUpdateMu.Lock()
	a.memoryMDUpdateQueue = append(a.memoryMDUpdateQueue, memoryMDUpdateJob{
		RunID:          strings.TrimSpace(job.RunID),
		RunTitle:       strings.TrimSpace(job.RunTitle),
		UserRequest:    job.UserRequest,
		AssistantFinal: job.AssistantFinal,
		ToolRecords:    append([]memory.ToolRecord(nil), job.ToolRecords...),
	})
	if a.memoryMDUpdateRunning {
		a.memoryMDUpdateMu.Unlock()
		return
	}
	a.memoryMDUpdateRunning = true
	a.memoryMDUpdateMu.Unlock()

	go a.runMemoryMDUpdateLoop()
}

func (a *Agent) runMemoryMDUpdateLoop() {
	if a == nil || a.Client == nil {
		return
	}
	for {
		a.memoryMDUpdateMu.Lock()
		if len(a.memoryMDUpdateQueue) == 0 {
			a.memoryMDUpdateRunning = false
			a.memoryMDUpdateMu.Unlock()
			return
		}
		job := a.memoryMDUpdateQueue[0]
		a.memoryMDUpdateQueue = a.memoryMDUpdateQueue[1:]
		a.memoryMDUpdateMu.Unlock()

		_, _, _ = a.updateMemoryMDAfterTurn(job.RunID, job.RunTitle, job.UserRequest, strings.TrimSpace(job.AssistantFinal), job.ToolRecords)
	}
}
