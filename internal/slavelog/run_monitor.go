package slavelog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"test_skill_agent/internal/multiagent"
)

type RunMonitor struct {
	Coord *multiagent.Coordinator
	Log   *Logger

	mu   sync.Mutex
	runs map[string]*runCursor
}

type runCursor struct {
	agentSeq map[string]int64
	lastSeen time.Time
}

func NewRunMonitor(coord *multiagent.Coordinator, log *Logger) *RunMonitor {
	return &RunMonitor{
		Coord: coord,
		Log:   log,
		runs:  make(map[string]*runCursor),
	}
}

func (m *RunMonitor) AddRun(runID string) {
	if m == nil {
		return
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runs == nil {
		m.runs = make(map[string]*runCursor)
	}
	if _, ok := m.runs[id]; ok {
		return
	}
	m.runs[id] = &runCursor{
		agentSeq: make(map[string]int64),
		lastSeen: time.Now().UTC(),
	}
	if m.Log != nil {
		m.Log.Logf(KindInfo, "monitor: attached run_id=%s", id)
	}
}

func (m *RunMonitor) Run(ctx context.Context) {
	if m == nil || m.Coord == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pollOnce(ctx)
		}
	}
}

func (m *RunMonitor) pollOnce(ctx context.Context) {
	runIDs := m.snapshotRuns()
	if len(runIDs) == 0 {
		return
	}
	for _, runID := range runIDs {
		m.pollRun(ctx, runID)
	}
	m.pruneRuns()
}

func (m *RunMonitor) snapshotRuns() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.runs))
	for runID := range m.runs {
		out = append(out, runID)
	}
	return out
}

func (m *RunMonitor) pruneRuns() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for runID, cur := range m.runs {
		if cur == nil {
			delete(m.runs, runID)
			continue
		}
		if now.Sub(cur.lastSeen) > 2*time.Hour {
			delete(m.runs, runID)
		}
	}
}

func (m *RunMonitor) pollRun(ctx context.Context, runID string) {
	states, err := m.Coord.ListAgentStates(runID)
	if err != nil {
		return
	}
	if len(states) == 0 {
		if _, err := m.Coord.ReadRun(runID); err != nil && errors.Is(err, os.ErrNotExist) {
			m.forgetRun(runID)
			return
		}
		m.markRunSeen(runID)
		return
	}
	for _, state := range states {
		agentID := strings.TrimSpace(state.AgentID)
		if agentID == "" {
			continue
		}
		lastSeq := m.lastSeq(runID, agentID)
		evts, err := m.Coord.ReadEvents(runID, agentID, lastSeq, 200)
		if err != nil {
			continue
		}
		if len(evts) == 0 {
			continue
		}
		for _, evt := range evts {
			m.logEvent(runID, agentID, evt)
			if evt.Seq > lastSeq {
				lastSeq = evt.Seq
			}
		}
		m.setLastSeq(runID, agentID, lastSeq)
	}
	m.markRunSeen(runID)
}

func (m *RunMonitor) forgetRun(runID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runs == nil {
		return
	}
	if _, ok := m.runs[runID]; !ok {
		return
	}
	delete(m.runs, runID)
	if m.Log != nil {
		m.Log.Logf(KindInfo, "monitor: detached run_id=%s", runID)
	}
}

func (m *RunMonitor) markRunSeen(runID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.runs[runID]; ok && cur != nil {
		cur.lastSeen = time.Now().UTC()
	}
}

func (m *RunMonitor) lastSeq(runID string, agentID string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.runs[runID]
	if !ok || cur == nil || cur.agentSeq == nil {
		return 0
	}
	return cur.agentSeq[agentID]
}

func (m *RunMonitor) setLastSeq(runID string, agentID string, seq int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.runs[runID]
	if !ok || cur == nil {
		return
	}
	if cur.agentSeq == nil {
		cur.agentSeq = make(map[string]int64)
	}
	cur.agentSeq[agentID] = seq
}

func (m *RunMonitor) logEvent(runID string, agentID string, evt multiagent.AgentEvent) {
	if m == nil || m.Log == nil {
		return
	}
	switch strings.TrimSpace(evt.Type) {
	case "tool_call_started":
		name, _ := evt.Payload["name"].(string)
		args, _ := evt.Payload["args"].(string)
		m.Log.Logf(
			KindWorker,
			"run_id=%s agent_id=%s evt_seq=%d tool_start name=%s args=%s",
			runID,
			agentID,
			evt.Seq,
			strings.TrimSpace(name),
			Preview(args, 240),
		)
	case "tool_call_finished":
		name, _ := evt.Payload["name"].(string)
		status, _ := evt.Payload["status"].(string)
		errMsg, _ := evt.Payload["error"].(string)
		dur := fmt.Sprintf("%v", evt.Payload["duration_ms"])
		result, _ := evt.Payload["result"].(string)
		kind := KindWorker
		if strings.EqualFold(strings.TrimSpace(status), "error") || strings.TrimSpace(errMsg) != "" {
			kind = KindWarn
		}
		m.Log.Logf(
			kind,
			"run_id=%s agent_id=%s evt_seq=%d tool_done name=%s status=%s duration_ms=%s error=%s result=%s",
			runID,
			agentID,
			evt.Seq,
			strings.TrimSpace(name),
			strings.TrimSpace(status),
			strings.TrimSpace(dur),
			Preview(errMsg, 160),
			Preview(result, 240),
		)
	case "spawned":
		pid := fmt.Sprintf("%v", evt.Payload["pid"])
		task := fmt.Sprintf("%v", evt.Payload["task"])
		m.Log.Logf(
			KindWorker,
			"run_id=%s agent_id=%s evt_seq=%d spawned pid=%s task=%s",
			runID,
			agentID,
			evt.Seq,
			strings.TrimSpace(pid),
			Preview(task, 240),
		)
	case "process_exit":
		errText, _ := evt.Payload["error"].(string)
		m.Log.Logf(
			KindWarn,
			"run_id=%s agent_id=%s evt_seq=%d process_exit error=%s",
			runID,
			agentID,
			evt.Seq,
			Preview(errText, 240),
		)
	case "worker_started", "worker_finished":
		msg := Preview(evt.Message, 200)
		m.Log.Logf(
			KindWorker,
			"run_id=%s agent_id=%s evt_seq=%d %s msg=%s",
			runID,
			agentID,
			evt.Seq,
			strings.TrimSpace(evt.Type),
			msg,
		)
	default:
		// keep logs focused; ignore other events by default
	}
}
