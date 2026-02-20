package multiagent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCoordinatorRunAndAgentLifecycle(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(t.TempDir())
	run, err := coord.CreateRun("demo-run", map[string]any{"suite": "test"})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	if run.ID == "" {
		t.Fatalf("expected run id")
	}

	spec, state, err := coord.CreateAgent(run.ID, AgentSpec{
		ID:       "planner",
		Task:     "build a test plan",
		Metadata: map[string]any{"role": "planner"},
	})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}
	if spec.ID == "" {
		t.Fatalf("expected agent id")
	}
	if state.Status != StatusPending {
		t.Fatalf("expected pending status, got %s", state.Status)
	}

	loadedSpec, err := coord.ReadAgentSpec(run.ID, spec.ID)
	if err != nil {
		t.Fatalf("ReadAgentSpec failed: %v", err)
	}
	if loadedSpec.Task != "build a test plan" {
		t.Fatalf("unexpected task: %s", loadedSpec.Task)
	}

	loadedState, err := coord.ReadAgentState(run.ID, spec.ID)
	if err != nil {
		t.Fatalf("ReadAgentState failed: %v", err)
	}
	if loadedState.AgentID != spec.ID {
		t.Fatalf("expected same agent id, got %s", loadedState.AgentID)
	}
}

func TestCoordinatorCommandsEventsAndSignals(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(t.TempDir())
	run, err := coord.CreateRun("", nil)
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	spec, _, err := coord.CreateAgent(run.ID, AgentSpec{
		Task: "dummy",
	})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	cmd1, err := coord.AppendCommand(run.ID, spec.ID, AgentCommand{Type: CommandPause})
	if err != nil {
		t.Fatalf("AppendCommand pause failed: %v", err)
	}
	cmd2, err := coord.AppendCommand(run.ID, spec.ID, AgentCommand{Type: CommandResume})
	if err != nil {
		t.Fatalf("AppendCommand resume failed: %v", err)
	}
	if cmd2.Seq <= cmd1.Seq {
		t.Fatalf("expected increasing sequence, got %d then %d", cmd1.Seq, cmd2.Seq)
	}

	commands, err := coord.ReadCommandsAfter(run.ID, spec.ID, 0, 10)
	if err != nil {
		t.Fatalf("ReadCommandsAfter failed: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(commands))
	}

	_, err = coord.AppendEvent(run.ID, spec.ID, AgentEvent{Type: "test_event"})
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}
	events, err := coord.ReadEvents(run.ID, spec.ID, 0, 10)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	sig, err := coord.AppendSignal(run.ID, "barrier", Signal{
		FromAgentID: spec.ID,
		Payload:     map[string]any{"ok": true},
	})
	if err != nil {
		t.Fatalf("AppendSignal failed: %v", err)
	}
	if sig.Seq == 0 {
		t.Fatalf("expected non-zero signal sequence")
	}

	gotSig, err := coord.WaitForSignal(context.Background(), run.ID, "barrier", 0, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForSignal failed: %v", err)
	}
	if gotSig.Seq != sig.Seq {
		t.Fatalf("expected seq %d, got %d", sig.Seq, gotSig.Seq)
	}
}

func TestWorkerControllerPauseResumeCancel(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(t.TempDir())
	run, err := coord.CreateRun("run", nil)
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	spec, _, err := coord.CreateAgent(run.ID, AgentSpec{Task: "task"})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}
	ctl := NewWorkerController(coord, run.ID, spec.ID)
	if err := ctl.Start(12345); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if _, err := coord.AppendCommand(run.ID, spec.ID, AgentCommand{Type: CommandPause}); err != nil {
		t.Fatalf("append pause failed: %v", err)
	}
	go func() {
		time.Sleep(120 * time.Millisecond)
		_, _ = coord.AppendCommand(run.ID, spec.ID, AgentCommand{Type: CommandResume})
	}()
	start := time.Now()
	if err := ctl.Checkpoint(context.Background(), "test"); err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatalf("expected pause/resume wait, got too short wait")
	}

	if _, err := coord.AppendCommand(run.ID, spec.ID, AgentCommand{
		Type:    CommandMessage,
		Payload: map[string]any{"text": "hello worker"},
	}); err != nil {
		t.Fatalf("append message failed: %v", err)
	}
	if err := ctl.Checkpoint(context.Background(), "message"); err != nil {
		t.Fatalf("Checkpoint message failed: %v", err)
	}
	msgs := ctl.DrainMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if got, _ := msgs[0].Payload["text"].(string); got != "hello worker" {
		t.Fatalf("unexpected message payload: %v", msgs[0].Payload)
	}

	if _, err := coord.AppendCommand(run.ID, spec.ID, AgentCommand{Type: CommandCancel}); err != nil {
		t.Fatalf("append cancel failed: %v", err)
	}
	err = ctl.Checkpoint(context.Background(), "cancel")
	if !errors.Is(err, ErrAgentCanceled) {
		t.Fatalf("expected ErrAgentCanceled, got %v", err)
	}
}
