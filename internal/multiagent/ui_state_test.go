package multiagent

import (
	"context"
	"testing"
	"time"
)

func TestSessionRunIDContext(t *testing.T) {
	t.Parallel()

	if _, ok := SessionRunID(context.Background()); ok {
		t.Fatalf("expected no session run id in background context")
	}

	ctx := WithSessionRunID(context.Background(), " run-123 ")
	if got, ok := SessionRunID(ctx); !ok || got != "run-123" {
		t.Fatalf("unexpected SessionRunID: ok=%v got=%q", ok, got)
	}
}

func TestRunUIStateHideAndShowAgents(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(t.TempDir())
	run, err := coord.CreateRun("demo-run", nil)
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	a1, _, err := coord.CreateAgent(run.ID, AgentSpec{ID: "a1", Task: "t1"})
	if err != nil {
		t.Fatalf("CreateAgent a1 failed: %v", err)
	}
	a2, _, err := coord.CreateAgent(run.ID, AgentSpec{ID: "a2", Task: "t2"})
	if err != nil {
		t.Fatalf("CreateAgent a2 failed: %v", err)
	}

	now1 := time.Date(2026, 2, 22, 12, 0, 0, 0, time.UTC)
	state, err := coord.SetAgentsHidden(run.ID, []string{a1.ID}, true, "noise", now1)
	if err != nil {
		t.Fatalf("SetAgentsHidden hide failed: %v", err)
	}
	if len(state.HiddenAgents) != 1 {
		t.Fatalf("expected 1 hidden agent, got %d", len(state.HiddenAgents))
	}
	if got, ok := state.HiddenAgents[a1.ID]; !ok || got.AgentID != a1.ID || got.HiddenAt.IsZero() || got.Reason != "noise" {
		t.Fatalf("unexpected hidden record: ok=%v rec=%+v", ok, got)
	}

	now2 := now1.Add(10 * time.Minute)
	state, err = coord.SetAgentsHidden(run.ID, []string{a2.ID}, true, "", now2)
	if err != nil {
		t.Fatalf("SetAgentsHidden hide a2 failed: %v", err)
	}
	if len(state.HiddenAgents) != 2 {
		t.Fatalf("expected 2 hidden agents, got %d", len(state.HiddenAgents))
	}

	loaded, err := coord.ReadRunUIState(run.ID)
	if err != nil {
		t.Fatalf("ReadRunUIState failed: %v", err)
	}
	if loaded.Version != runUIStateVersion {
		t.Fatalf("expected version %d, got %d", runUIStateVersion, loaded.Version)
	}
	if len(loaded.HiddenAgents) != 2 {
		t.Fatalf("expected 2 hidden agents after reload, got %d", len(loaded.HiddenAgents))
	}

	now3 := now2.Add(10 * time.Minute)
	state, err = coord.SetAgentsHidden(run.ID, []string{a1.ID}, false, "", now3)
	if err != nil {
		t.Fatalf("SetAgentsHidden show a1 failed: %v", err)
	}
	if len(state.HiddenAgents) != 1 {
		t.Fatalf("expected 1 hidden agent after show, got %d", len(state.HiddenAgents))
	}
	if _, ok := state.HiddenAgents[a1.ID]; ok {
		t.Fatalf("expected a1 to be visible after show")
	}
	if _, ok := state.HiddenAgents[a2.ID]; !ok {
		t.Fatalf("expected a2 to remain hidden")
	}
}
