package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"test_skill_agent/internal/multiagent"
)

func TestAgentRunListAndInspectTools(t *testing.T) {
	t.Parallel()

	coord := multiagent.NewCoordinator(t.TempDir())
	run, err := coord.CreateRun("demo-run", map[string]any{"suite": "test"})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	spec, _, err := coord.CreateAgent(run.ID, multiagent.AgentSpec{
		ID:   "worker-1",
		Task: "do something useful",
	})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	state, err := coord.ReadAgentState(run.ID, spec.ID)
	if err != nil {
		t.Fatalf("ReadAgentState failed: %v", err)
	}
	state.Status = multiagent.StatusRunning
	state.PID = 12345
	state.StartedAt = time.Now().UTC()
	if err := coord.UpdateAgentState(run.ID, state); err != nil {
		t.Fatalf("UpdateAgentState failed: %v", err)
	}

	_, _ = coord.AppendEvent(run.ID, spec.ID, multiagent.AgentEvent{
		Type:      "test_event",
		Message:   "hello",
		CreatedAt: time.Now().UTC(),
	})
	_, _ = coord.AppendCommand(run.ID, spec.ID, multiagent.AgentCommand{
		Type:      multiagent.CommandMessage,
		Payload:   map[string]any{"text": "guidance"},
		CreatedAt: time.Now().UTC(),
	})

	agentDir := coord.AgentDir(run.ID, spec.ID)
	if err := os.WriteFile(filepath.Join(agentDir, "stdout.log"), []byte("out1\nout2\n"), 0o644); err != nil {
		t.Fatalf("write stdout.log failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "stderr.log"), []byte("err1\n"), 0o644); err != nil {
		t.Fatalf("write stderr.log failed: %v", err)
	}

	listTool := &AgentRunListTool{Coordinator: coord}
	listOut, err := listTool.Call(context.Background(), json.RawMessage(`{"active_only":true,"include_agents":true,"include_tasks":true}`))
	if err != nil {
		t.Fatalf("AgentRunListTool.Call failed: %v", err)
	}
	var listObj map[string]any
	if err := json.Unmarshal([]byte(listOut), &listObj); err != nil {
		t.Fatalf("unmarshal list output failed: %v", err)
	}
	runsAny, ok := listObj["runs"].([]any)
	if !ok || len(runsAny) == 0 {
		t.Fatalf("expected runs array in output, got: %v", listObj["runs"])
	}

	inspectTool := &AgentInspectTool{Coordinator: coord}
	inspectOut, err := inspectTool.Call(context.Background(), json.RawMessage(`{"run_id":"`+run.ID+`","agent_id":"`+spec.ID+`","events_limit":10,"commands_limit":10,"stdout_tail_lines":10,"stderr_tail_lines":10}`))
	if err != nil {
		t.Fatalf("AgentInspectTool.Call failed: %v", err)
	}
	var inspectObj map[string]any
	if err := json.Unmarshal([]byte(inspectOut), &inspectObj); err != nil {
		t.Fatalf("unmarshal inspect output failed: %v", err)
	}
	if got := inspectObj["run_id"]; got != run.ID {
		t.Fatalf("unexpected run_id: %v", got)
	}
	if got := inspectObj["agent_id"]; got != spec.ID {
		t.Fatalf("unexpected agent_id: %v", got)
	}
	if tail, _ := inspectObj["stdout_tail"].(string); !strings.Contains(tail, "out2") {
		t.Fatalf("unexpected stdout_tail: %q", tail)
	}
}
