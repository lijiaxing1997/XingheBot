package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"test_skill_agent/internal/multiagent"
)

func TestSubagentsTool_ListSteerKill(t *testing.T) {
	t.Parallel()

	coord := multiagent.NewCoordinator(t.TempDir())
	run, err := coord.CreateRun("demo-run", nil)
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	spec1, _, err := coord.CreateAgent(run.ID, multiagent.AgentSpec{ID: "worker-1", Task: "do something useful"})
	if err != nil {
		t.Fatalf("CreateAgent worker-1 failed: %v", err)
	}
	state1, err := coord.ReadAgentState(run.ID, spec1.ID)
	if err != nil {
		t.Fatalf("ReadAgentState worker-1 failed: %v", err)
	}
	now := time.Now().UTC()
	state1.Status = multiagent.StatusRunning
	state1.PID = 12345
	state1.StartedAt = now.Add(-90 * time.Second)
	if err := coord.UpdateAgentState(run.ID, state1); err != nil {
		t.Fatalf("UpdateAgentState worker-1 failed: %v", err)
	}
	_, _ = coord.AppendEvent(run.ID, spec1.ID, multiagent.AgentEvent{
		Type:      "tool_call_started",
		Message:   "list_files",
		CreatedAt: now.Add(-30 * time.Second),
	})

	spec2, _, err := coord.CreateAgent(run.ID, multiagent.AgentSpec{ID: "worker-2", Task: "finish quickly"})
	if err != nil {
		t.Fatalf("CreateAgent worker-2 failed: %v", err)
	}
	state2, err := coord.ReadAgentState(run.ID, spec2.ID)
	if err != nil {
		t.Fatalf("ReadAgentState worker-2 failed: %v", err)
	}
	state2.Status = multiagent.StatusCompleted
	state2.PID = 54321
	state2.StartedAt = now.Add(-3 * time.Minute)
	state2.FinishedAt = now.Add(-10 * time.Second)
	if err := coord.UpdateAgentState(run.ID, state2); err != nil {
		t.Fatalf("UpdateAgentState worker-2 failed: %v", err)
	}

	tool := &SubagentsTool{Coordinator: coord}

	listOut, err := tool.Call(context.Background(), json.RawMessage(`{"action":"list","run_id":"`+run.ID+`","recent_minutes":30}`))
	if err != nil {
		t.Fatalf("subagents list failed: %v", err)
	}
	if !strings.Contains(listOut, "active subagents:") {
		t.Fatalf("expected list output to include text block, got:\n%s", listOut)
	}
	if !strings.Contains(listOut, "worker-1") {
		t.Fatalf("expected list output to include worker-1, got:\n%s", listOut)
	}

	_, err = tool.Call(context.Background(), json.RawMessage(`{"action":"steer","run_id":"`+run.ID+`","target":"last","message":"hello","role":"user"}`))
	if err != nil {
		t.Fatalf("subagents steer failed: %v", err)
	}
	cmds, err := coord.ReadCommandsAfter(run.ID, spec1.ID, 0, 10)
	if err != nil {
		t.Fatalf("ReadCommandsAfter failed: %v", err)
	}
	foundMessage := false
	for _, cmd := range cmds {
		if cmd.Type == multiagent.CommandMessage {
			foundMessage = true
			break
		}
	}
	if !foundMessage {
		t.Fatalf("expected message command for worker-1, got: %#v", cmds)
	}

	_, err = tool.Call(context.Background(), json.RawMessage(`{"action":"kill","run_id":"`+run.ID+`","target":"worker-1","force":false}`))
	if err != nil {
		t.Fatalf("subagents kill failed: %v", err)
	}
	cmds, err = coord.ReadCommandsAfter(run.ID, spec1.ID, 0, 20)
	if err != nil {
		t.Fatalf("ReadCommandsAfter failed: %v", err)
	}
	foundCancel := false
	for _, cmd := range cmds {
		if cmd.Type == multiagent.CommandCancel {
			foundCancel = true
			break
		}
	}
	if !foundCancel {
		t.Fatalf("expected cancel command for worker-1, got: %#v", cmds)
	}
}
