package multiagent

import (
	"context"
	"testing"
)

func TestWorkerControllerPersistsLastCommandSeq(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(t.TempDir())
	run, err := coord.CreateRun("", nil)
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	spec, _, err := coord.CreateAgent(run.ID, AgentSpec{Task: "task"})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	ctl1 := NewWorkerController(coord, run.ID, spec.ID)
	if err := ctl1.Start(111); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	cmd1, err := coord.AppendCommand(run.ID, spec.ID, AgentCommand{
		Type:    CommandMessage,
		Payload: map[string]any{"text": "hello"},
	})
	if err != nil {
		t.Fatalf("AppendCommand failed: %v", err)
	}
	if err := ctl1.Checkpoint(context.Background(), "first"); err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}
	msgs := ctl1.DrainMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Seq != cmd1.Seq {
		t.Fatalf("expected message seq %d, got %d", cmd1.Seq, msgs[0].Seq)
	}

	state, err := coord.ReadAgentState(run.ID, spec.ID)
	if err != nil {
		t.Fatalf("ReadAgentState failed: %v", err)
	}
	if state.LastCommandSeq != cmd1.Seq {
		t.Fatalf("expected last_command_seq %d, got %d", cmd1.Seq, state.LastCommandSeq)
	}

	ctl2 := NewWorkerController(coord, run.ID, spec.ID)
	if err := ctl2.Start(222); err != nil {
		t.Fatalf("Start(2) failed: %v", err)
	}
	if err := ctl2.Checkpoint(context.Background(), "noop"); err != nil {
		t.Fatalf("Checkpoint(2) failed: %v", err)
	}
	if msgs := ctl2.DrainMessages(); len(msgs) != 0 {
		t.Fatalf("expected 0 messages after restart, got %d", len(msgs))
	}

	cmd2, err := coord.AppendCommand(run.ID, spec.ID, AgentCommand{
		Type:    CommandMessage,
		Payload: map[string]any{"text": "second"},
	})
	if err != nil {
		t.Fatalf("AppendCommand(2) failed: %v", err)
	}
	if err := ctl2.Checkpoint(context.Background(), "second"); err != nil {
		t.Fatalf("Checkpoint(3) failed: %v", err)
	}
	msgs = ctl2.DrainMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after restart, got %d", len(msgs))
	}
	if msgs[0].Seq != cmd2.Seq {
		t.Fatalf("expected message seq %d, got %d", cmd2.Seq, msgs[0].Seq)
	}
}

