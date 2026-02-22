package multiagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunAutoCleanupOnceArchivesCompletedRuns(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runRoot := filepath.Join(root, "runs")
	archiveDir := filepath.Join(root, "archive")

	coord := NewCoordinator(runRoot)

	now := time.Now().UTC()

	runOld, err := coord.CreateRun("old_ok", nil)
	if err != nil {
		t.Fatalf("CreateRun old_ok failed: %v", err)
	}
	oldSpec, _, err := coord.CreateAgent(runOld.ID, AgentSpec{Task: "old"})
	if err != nil {
		t.Fatalf("CreateAgent old_ok failed: %v", err)
	}
	markAgentFinished(t, coord, runOld.ID, oldSpec.ID, StatusCompleted, now.Add(-2*time.Hour))

	runNew, err := coord.CreateRun("new_ok", nil)
	if err != nil {
		t.Fatalf("CreateRun new_ok failed: %v", err)
	}
	newSpec, _, err := coord.CreateAgent(runNew.ID, AgentSpec{Task: "new"})
	if err != nil {
		t.Fatalf("CreateAgent new_ok failed: %v", err)
	}
	markAgentFinished(t, coord, runNew.ID, newSpec.ID, StatusCompleted, now.Add(-1*time.Hour))

	cfg := ResolvedAutoCleanupConfig{
		Enabled:       true,
		Interval:      time.Hour,
		Mode:          "archive",
		ArchiveDir:    archiveDir,
		KeepLast:      1,
		ArchiveAfter:  0,
		IncludeFailed: true,
		DryRun:        false,
	}

	report, err := RunAutoCleanupOnce(context.Background(), coord, cfg)
	if err != nil {
		t.Fatalf("RunAutoCleanupOnce failed: %v", err)
	}
	if report.Applied != 1 {
		t.Fatalf("expected applied=1, got %d", report.Applied)
	}

	if _, err := os.Stat(coord.RunDir(runOld.ID)); !os.IsNotExist(err) {
		t.Fatalf("expected old run moved out of run root, stat err=%v", err)
	}
	if _, err := os.Stat(coord.RunDir(runNew.ID)); err != nil {
		t.Fatalf("expected new run kept in run root, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, runOld.ID)); err != nil {
		t.Fatalf("expected old run archived, stat err=%v", err)
	}
}

func markAgentFinished(t *testing.T, coord *Coordinator, runID string, agentID string, status string, finishedAt time.Time) {
	t.Helper()

	st, err := coord.ReadAgentState(runID, agentID)
	if err != nil {
		t.Fatalf("ReadAgentState failed: %v", err)
	}
	st.Status = status
	st.FinishedAt = finishedAt
	if err := coord.UpdateAgentState(runID, st); err != nil {
		t.Fatalf("UpdateAgentState failed: %v", err)
	}
}
