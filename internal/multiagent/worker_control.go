package multiagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrAgentCanceled = errors.New("agent canceled by command")

type WorkerController struct {
	Coord          *Coordinator
	RunID          string
	AgentID        string
	LastCommandSeq int64
	paused         bool
	pendingMessages []AgentCommand
}

func NewWorkerController(coord *Coordinator, runID string, agentID string) *WorkerController {
	return &WorkerController{
		Coord:   coord,
		RunID:   SanitizeID(runID, GenerateRunID()),
		AgentID: SanitizeID(agentID, GenerateAgentID()),
	}
}

func (w *WorkerController) Start(pid int) error {
	if w.Coord == nil {
		return errors.New("worker controller has nil coordinator")
	}
	state, err := w.Coord.ReadAgentState(w.RunID, w.AgentID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	state.Status = StatusRunning
	state.PID = pid
	if state.StartedAt.IsZero() {
		state.StartedAt = now
	}
	state.UpdatedAt = now
	if err := w.Coord.UpdateAgentState(w.RunID, state); err != nil {
		return err
	}
	_, err = w.Coord.AppendEvent(w.RunID, w.AgentID, AgentEvent{
		Type:      "worker_started",
		Message:   "worker process started",
		CreatedAt: now,
		Payload: map[string]any{
			"pid": pid,
		},
	})
	return err
}

func (w *WorkerController) Checkpoint(ctx context.Context, stage string) error {
	if w.Coord == nil {
		return errors.New("worker controller has nil coordinator")
	}
	if err := w.pollCommands(ctx); err != nil {
		return err
	}
	if w.paused {
		return w.waitUntilResumed(ctx, stage)
	}
	return nil
}

func (w *WorkerController) BeforeTool(ctx context.Context, name string, args string) error {
	if err := w.Checkpoint(ctx, "before_tool"); err != nil {
		return err
	}
	_, err := w.Coord.AppendEvent(w.RunID, w.AgentID, AgentEvent{
		Type:      "tool_call_started",
		Message:   name,
		CreatedAt: time.Now().UTC(),
		Payload: map[string]any{
			"name":      name,
			"args":      safePreview(args, 1200),
			"args_size": len(strings.TrimSpace(args)),
		},
	})
	return err
}

func (w *WorkerController) AfterTool(ctx context.Context, name string, args string, result string, callErr error, duration time.Duration) error {
	status := "ok"
	errMsg := ""
	if callErr != nil {
		status = "error"
		errMsg = callErr.Error()
	}
	_, err := w.Coord.AppendEvent(w.RunID, w.AgentID, AgentEvent{
		Type:      "tool_call_finished",
		Message:   name,
		CreatedAt: time.Now().UTC(),
		Payload: map[string]any{
			"name":          name,
			"status":        status,
			"error":         errMsg,
			"duration_ms":   duration.Milliseconds(),
			"result":        safePreview(result, 2000),
			"result_length": len(result),
			"args_size":     len(strings.TrimSpace(args)),
		},
	})
	if err != nil {
		return err
	}
	return w.Checkpoint(ctx, "after_tool")
}

func (w *WorkerController) Finish(output string, runErr error) error {
	if w.Coord == nil {
		return errors.New("worker controller has nil coordinator")
	}
	status := StatusCompleted
	errText := ""
	if runErr != nil {
		errText = runErr.Error()
		switch {
		case errors.Is(runErr, ErrAgentCanceled):
			status = StatusCanceled
		case errors.Is(runErr, context.Canceled):
			status = StatusCanceled
		default:
			status = StatusFailed
		}
	}

	finishedAt := time.Now().UTC()
	result := AgentResult{
		RunID:      w.RunID,
		AgentID:    w.AgentID,
		Status:     status,
		Output:     output,
		Error:      errText,
		FinishedAt: finishedAt,
	}
	if err := w.Coord.WriteResult(w.RunID, w.AgentID, result); err != nil {
		return err
	}

	state, err := w.Coord.ReadAgentState(w.RunID, w.AgentID)
	if err != nil {
		return err
	}
	state.Status = status
	state.Error = errText
	state.ResultPath = w.Coord.AgentResultPath(w.RunID, w.AgentID)
	state.FinishedAt = finishedAt
	state.UpdatedAt = finishedAt
	if err := w.Coord.UpdateAgentState(w.RunID, state); err != nil {
		return err
	}

	_, err = w.Coord.AppendEvent(w.RunID, w.AgentID, AgentEvent{
		Type:      "worker_finished",
		Message:   status,
		CreatedAt: finishedAt,
		Payload: map[string]any{
			"status": status,
			"error":  errText,
		},
	})
	return err
}

func (w *WorkerController) pollCommands(ctx context.Context) error {
	commands, err := w.Coord.ReadCommandsAfter(w.RunID, w.AgentID, w.LastCommandSeq, 200)
	if err != nil {
		return err
	}
	for _, cmd := range commands {
		w.LastCommandSeq = cmd.Seq
		switch normalizeCommandType(cmd.Type) {
		case CommandPause:
			w.paused = true
			if err := w.markPaused(); err != nil {
				return err
			}
			_, _ = w.Coord.AppendEvent(w.RunID, w.AgentID, AgentEvent{
				Type:      "command_pause",
				Message:   "received pause",
				CreatedAt: time.Now().UTC(),
				Payload:   cmd.Payload,
			})
		case CommandResume:
			w.paused = false
			if err := w.markRunning(); err != nil {
				return err
			}
			_, _ = w.Coord.AppendEvent(w.RunID, w.AgentID, AgentEvent{
				Type:      "command_resume",
				Message:   "received resume",
				CreatedAt: time.Now().UTC(),
				Payload:   cmd.Payload,
			})
		case CommandCancel:
			_, _ = w.Coord.AppendEvent(w.RunID, w.AgentID, AgentEvent{
				Type:      "command_cancel",
				Message:   "received cancel",
				CreatedAt: time.Now().UTC(),
				Payload:   cmd.Payload,
			})
			return ErrAgentCanceled
		case CommandMessage:
			w.pendingMessages = append(w.pendingMessages, cmd)
			_, _ = w.Coord.AppendEvent(w.RunID, w.AgentID, AgentEvent{
				Type:      "command_message",
				Message:   "received message",
				CreatedAt: time.Now().UTC(),
				Payload:   cmd.Payload,
			})
		default:
			_, _ = w.Coord.AppendEvent(w.RunID, w.AgentID, AgentEvent{
				Type:      "command_unknown",
				Message:   fmt.Sprintf("unknown command: %s", cmd.Type),
				CreatedAt: time.Now().UTC(),
				Payload:   cmd.Payload,
			})
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (w *WorkerController) DrainMessages() []AgentCommand {
	if w == nil || len(w.pendingMessages) == 0 {
		return nil
	}
	out := append([]AgentCommand(nil), w.pendingMessages...)
	w.pendingMessages = w.pendingMessages[:0]
	return out
}

func (w *WorkerController) waitUntilResumed(ctx context.Context, stage string) error {
	if err := w.markPaused(); err != nil {
		return err
	}
	for {
		if err := w.pollCommands(ctx); err != nil {
			return err
		}
		if !w.paused {
			if err := w.markRunning(); err != nil {
				return err
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		if strings.TrimSpace(stage) != "" {
			_, _ = w.Coord.AppendEvent(w.RunID, w.AgentID, AgentEvent{
				Type:      "paused_waiting",
				Message:   stage,
				CreatedAt: time.Now().UTC(),
			})
		}
	}
}

func (w *WorkerController) markPaused() error {
	return w.updateStatus(StatusPaused, "")
}

func (w *WorkerController) markRunning() error {
	return w.updateStatus(StatusRunning, "")
}

func (w *WorkerController) updateStatus(status string, errText string) error {
	state, err := w.Coord.ReadAgentState(w.RunID, w.AgentID)
	if err != nil {
		return err
	}
	if state.Status == status && strings.TrimSpace(errText) == strings.TrimSpace(state.Error) {
		return nil
	}
	state.Status = status
	if strings.TrimSpace(errText) != "" {
		state.Error = errText
	}
	state.UpdatedAt = time.Now().UTC()
	return w.Coord.UpdateAgentState(w.RunID, state)
}

func safePreview(raw string, limit int) string {
	s := strings.TrimSpace(raw)
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}
