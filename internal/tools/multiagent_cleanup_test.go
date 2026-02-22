package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"test_skill_agent/internal/multiagent"
)

func TestAgentRunPruneToolDeleteSkipsActiveAndFailed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runRoot := filepath.Join(root, "runs")
	coord := multiagent.NewCoordinator(runRoot)
	tool := &AgentRunPruneTool{Coordinator: coord}

	now := time.Now().UTC()

	runOldOK, err := coord.CreateRun("old_ok", nil)
	if err != nil {
		t.Fatalf("CreateRun old_ok failed: %v", err)
	}
	setRunCreatedAt(t, coord, runOldOK.ID, now.Add(-48*time.Hour))
	specOld, _, err := coord.CreateAgent(runOldOK.ID, multiagent.AgentSpec{Task: "old ok"})
	if err != nil {
		t.Fatalf("CreateAgent old_ok failed: %v", err)
	}
	markAgentStatus(t, coord, runOldOK.ID, specOld.ID, multiagent.StatusCompleted)

	runNewOK, err := coord.CreateRun("new_ok", nil)
	if err != nil {
		t.Fatalf("CreateRun new_ok failed: %v", err)
	}
	setRunCreatedAt(t, coord, runNewOK.ID, now.Add(-24*time.Hour))
	specNew, _, err := coord.CreateAgent(runNewOK.ID, multiagent.AgentSpec{Task: "new ok"})
	if err != nil {
		t.Fatalf("CreateAgent new_ok failed: %v", err)
	}
	markAgentStatus(t, coord, runNewOK.ID, specNew.ID, multiagent.StatusCompleted)

	runFailed, err := coord.CreateRun("old_failed", nil)
	if err != nil {
		t.Fatalf("CreateRun old_failed failed: %v", err)
	}
	setRunCreatedAt(t, coord, runFailed.ID, now.Add(-72*time.Hour))
	specFailed, _, err := coord.CreateAgent(runFailed.ID, multiagent.AgentSpec{Task: "failed"})
	if err != nil {
		t.Fatalf("CreateAgent old_failed failed: %v", err)
	}
	markAgentStatus(t, coord, runFailed.ID, specFailed.ID, multiagent.StatusFailed)

	runActive, err := coord.CreateRun("active_running", nil)
	if err != nil {
		t.Fatalf("CreateRun active_running failed: %v", err)
	}
	setRunCreatedAt(t, coord, runActive.ID, now.Add(-96*time.Hour))
	specActive, _, err := coord.CreateAgent(runActive.ID, multiagent.AgentSpec{Task: "active"})
	if err != nil {
		t.Fatalf("CreateAgent active_running failed: %v", err)
	}
	markAgentStatus(t, coord, runActive.ID, specActive.ID, multiagent.StatusRunning)

	out, err := tool.Call(context.Background(), json.RawMessage(`{"mode":"delete","keep_last":1,"dry_run":false}`))
	if err != nil {
		t.Fatalf("AgentRunPruneTool.Call failed: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("unmarshal output failed: %v", err)
	}
	if got, _ := obj["applied"].(float64); got != 1 {
		t.Fatalf("expected applied=1, got %v", obj["applied"])
	}

	if _, err := os.Stat(coord.RunDir(runOldOK.ID)); !os.IsNotExist(err) {
		t.Fatalf("expected old_ok run dir removed, stat err=%v", err)
	}
	if _, err := os.Stat(coord.RunDir(runNewOK.ID)); err != nil {
		t.Fatalf("expected new_ok run dir kept, stat err=%v", err)
	}
	if _, err := os.Stat(coord.RunDir(runFailed.ID)); err != nil {
		t.Fatalf("expected old_failed run dir kept, stat err=%v", err)
	}
	if _, err := os.Stat(coord.RunDir(runActive.ID)); err != nil {
		t.Fatalf("expected active_running run dir kept, stat err=%v", err)
	}
}

func TestAgentRunPruneToolArchiveMovesRuns(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runRoot := filepath.Join(root, "runs")
	coord := multiagent.NewCoordinator(runRoot)
	tool := &AgentRunPruneTool{Coordinator: coord}

	now := time.Now().UTC()

	runOldOK, err := coord.CreateRun("old_ok", nil)
	if err != nil {
		t.Fatalf("CreateRun old_ok failed: %v", err)
	}
	setRunCreatedAt(t, coord, runOldOK.ID, now.Add(-48*time.Hour))
	specOld, _, err := coord.CreateAgent(runOldOK.ID, multiagent.AgentSpec{Task: "old ok"})
	if err != nil {
		t.Fatalf("CreateAgent old_ok failed: %v", err)
	}
	markAgentStatus(t, coord, runOldOK.ID, specOld.ID, multiagent.StatusCompleted)

	runNewOK, err := coord.CreateRun("new_ok", nil)
	if err != nil {
		t.Fatalf("CreateRun new_ok failed: %v", err)
	}
	setRunCreatedAt(t, coord, runNewOK.ID, now.Add(-24*time.Hour))
	specNew, _, err := coord.CreateAgent(runNewOK.ID, multiagent.AgentSpec{Task: "new ok"})
	if err != nil {
		t.Fatalf("CreateAgent new_ok failed: %v", err)
	}
	markAgentStatus(t, coord, runNewOK.ID, specNew.ID, multiagent.StatusCompleted)

	out, err := tool.Call(context.Background(), json.RawMessage(`{"mode":"archive","keep_last":1,"dry_run":false}`))
	if err != nil {
		t.Fatalf("AgentRunPruneTool.Call failed: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("unmarshal output failed: %v", err)
	}
	if got, _ := obj["applied"].(float64); got != 1 {
		t.Fatalf("expected applied=1, got %v", obj["applied"])
	}

	archiveDir, _ := obj["archive_dir"].(string)
	if archiveDir == "" {
		t.Fatalf("expected archive_dir in output")
	}

	if _, err := os.Stat(coord.RunDir(runOldOK.ID)); !os.IsNotExist(err) {
		t.Fatalf("expected old_ok run dir moved, stat err=%v", err)
	}
	if _, err := os.Stat(coord.RunDir(runNewOK.ID)); err != nil {
		t.Fatalf("expected new_ok run dir kept, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, runOldOK.ID)); err != nil {
		t.Fatalf("expected archived old_ok run dir present, stat err=%v", err)
	}
}

func setRunCreatedAt(t *testing.T, coord *multiagent.Coordinator, runID string, createdAt time.Time) {
	t.Helper()
	run, err := coord.ReadRun(runID)
	if err != nil {
		t.Fatalf("ReadRun failed: %v", err)
	}
	run.CreatedAt = createdAt
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Fatalf("marshal run manifest failed: %v", err)
	}
	if err := os.WriteFile(coord.RunManifestPath(runID), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write run manifest failed: %v", err)
	}
}

func markAgentStatus(t *testing.T, coord *multiagent.Coordinator, runID string, agentID string, status string) {
	t.Helper()
	st, err := coord.ReadAgentState(runID, agentID)
	if err != nil {
		t.Fatalf("ReadAgentState failed: %v", err)
	}
	st.Status = status
	if err := coord.UpdateAgentState(runID, st); err != nil {
		t.Fatalf("UpdateAgentState failed: %v", err)
	}
}
