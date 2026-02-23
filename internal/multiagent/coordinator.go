package multiagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrSignalWaitTimeout = errors.New("timed out waiting for signal")

type Coordinator struct {
	RunRoot string
}

func NewCoordinator(runRoot string) *Coordinator {
	root := strings.TrimSpace(runRoot)
	if root == "" {
		root = ".multi_agent/runs"
	}
	return &Coordinator{RunRoot: filepath.Clean(root)}
}

func (c *Coordinator) CreateRun(runID string, metadata map[string]any) (RunManifest, error) {
	id := SanitizeID(runID, GenerateRunID())
	runDir := c.RunDir(id)
	if err := os.MkdirAll(c.AgentsDir(id), 0o755); err != nil {
		return RunManifest{}, err
	}
	if err := os.MkdirAll(c.SignalsDir(id), 0o755); err != nil {
		return RunManifest{}, err
	}
	path := c.RunManifestPath(id)
	if _, err := os.Stat(path); err == nil {
		return c.ReadRun(id)
	}

	now := time.Now().UTC()
	manifest := RunManifest{
		ID:        id,
		CreatedAt: now,
		Metadata:  metadata,
	}
	if err := writeJSONAtomic(path, manifest); err != nil {
		return RunManifest{}, err
	}
	_ = os.MkdirAll(runDir, 0o755)
	return manifest, nil
}

func (c *Coordinator) EnsureRun(runID string) (RunManifest, error) {
	id := SanitizeID(runID, GenerateRunID())
	manifest, err := c.ReadRun(id)
	if err == nil {
		return manifest, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return RunManifest{}, err
	}
	return c.CreateRun(id, nil)
}

func (c *Coordinator) ReadRun(runID string) (RunManifest, error) {
	id := SanitizeID(runID, GenerateRunID())
	var run RunManifest
	if err := readJSONFile(c.RunManifestPath(id), &run); err != nil {
		return RunManifest{}, err
	}
	return run, nil
}

func (c *Coordinator) CreateAgent(runID string, spec AgentSpec) (AgentSpec, AgentState, error) {
	run, err := c.EnsureRun(runID)
	if err != nil {
		return AgentSpec{}, AgentState{}, err
	}
	agentID := SanitizeID(spec.ID, GenerateAgentID())
	agentDir := c.AgentDir(run.ID, agentID)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return AgentSpec{}, AgentState{}, err
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "asset"), 0o755); err != nil {
		return AgentSpec{}, AgentState{}, err
	}

	spec.ID = agentID
	spec.RunID = run.ID
	if spec.MaxTurns < 0 {
		spec.MaxTurns = 0
	}
	spec.CreatedAt = time.Now().UTC()
	if strings.TrimSpace(spec.Task) == "" {
		return AgentSpec{}, AgentState{}, errors.New("task is required")
	}

	specPath := c.AgentSpecPath(run.ID, agentID)
	if _, err := os.Stat(specPath); err == nil {
		return AgentSpec{}, AgentState{}, fmt.Errorf("agent already exists: %s", agentID)
	}
	if err := writeJSONAtomic(specPath, spec); err != nil {
		return AgentSpec{}, AgentState{}, err
	}

	now := time.Now().UTC()
	state := AgentState{
		RunID:     run.ID,
		AgentID:   agentID,
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := writeJSONAtomic(c.AgentStatePath(run.ID, agentID), state); err != nil {
		return AgentSpec{}, AgentState{}, err
	}

	return spec, state, nil
}

func (c *Coordinator) ReadAgentSpec(runID string, agentID string) (AgentSpec, error) {
	run := SanitizeID(runID, GenerateRunID())
	agent := SanitizeID(agentID, GenerateAgentID())
	var out AgentSpec
	if err := readJSONFile(c.AgentSpecPath(run, agent), &out); err != nil {
		return AgentSpec{}, err
	}
	return out, nil
}

func (c *Coordinator) ReadAgentState(runID string, agentID string) (AgentState, error) {
	run := SanitizeID(runID, GenerateRunID())
	agent := SanitizeID(agentID, GenerateAgentID())
	var out AgentState
	if err := readJSONFile(c.AgentStatePath(run, agent), &out); err != nil {
		return AgentState{}, err
	}
	return out, nil
}

func (c *Coordinator) UpdateAgentState(runID string, state AgentState) error {
	run := SanitizeID(runID, GenerateRunID())
	agent := SanitizeID(state.AgentID, GenerateAgentID())
	if state.RunID == "" {
		state.RunID = run
	}
	if state.AgentID == "" {
		state.AgentID = agent
	}
	if state.Status == "" {
		state.Status = StatusPending
	}
	if state.CreatedAt.IsZero() {
		existing, err := c.ReadAgentState(run, agent)
		if err == nil {
			state.CreatedAt = existing.CreatedAt
		}
		if state.CreatedAt.IsZero() {
			state.CreatedAt = time.Now().UTC()
		}
	}
	state.UpdatedAt = time.Now().UTC()
	if IsTerminalStatus(state.Status) && state.FinishedAt.IsZero() {
		state.FinishedAt = state.UpdatedAt
	}
	return writeJSONAtomic(c.AgentStatePath(run, agent), state)
}

func (c *Coordinator) ListAgentStates(runID string) ([]AgentState, error) {
	run := SanitizeID(runID, GenerateRunID())
	entries, err := os.ReadDir(c.AgentsDir(run))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]AgentState, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		statePath := filepath.Join(c.AgentsDir(run), entry.Name(), "state.json")
		var state AgentState
		if err := readJSONFile(statePath, &state); err != nil {
			continue
		}
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].AgentID < out[j].AgentID
	})
	return out, nil
}

func (c *Coordinator) AppendCommand(runID string, agentID string, cmd AgentCommand) (AgentCommand, error) {
	run := SanitizeID(runID, GenerateRunID())
	agent := SanitizeID(agentID, GenerateAgentID())
	cmd.Type = normalizeCommandType(cmd.Type)
	if cmd.Type == "" {
		return AgentCommand{}, errors.New("command type is required")
	}
	if cmd.CreatedAt.IsZero() {
		cmd.CreatedAt = time.Now().UTC()
	}
	path := c.AgentCommandsPath(run, agent)
	seq, err := appendSequencedJSONL(path, func(seq int64) {
		cmd.Seq = seq
	}, &cmd)
	if err != nil {
		return AgentCommand{}, err
	}
	cmd.Seq = seq
	return cmd, nil
}

func (c *Coordinator) ReadCommandsAfter(runID string, agentID string, afterSeq int64, limit int) ([]AgentCommand, error) {
	run := SanitizeID(runID, GenerateRunID())
	agent := SanitizeID(agentID, GenerateAgentID())
	return readSequencedJSONL[AgentCommand](c.AgentCommandsPath(run, agent), afterSeq, limit)
}

func (c *Coordinator) AppendEvent(runID string, agentID string, evt AgentEvent) (AgentEvent, error) {
	run := SanitizeID(runID, GenerateRunID())
	agent := SanitizeID(agentID, GenerateAgentID())
	if strings.TrimSpace(evt.Type) == "" {
		return AgentEvent{}, errors.New("event type is required")
	}
	if evt.CreatedAt.IsZero() {
		evt.CreatedAt = time.Now().UTC()
	}
	path := c.AgentEventsPath(run, agent)
	seq, err := appendSequencedJSONL(path, func(seq int64) {
		evt.Seq = seq
	}, &evt)
	if err != nil {
		return AgentEvent{}, err
	}
	evt.Seq = seq
	return evt, nil
}

func (c *Coordinator) ReadEvents(runID string, agentID string, afterSeq int64, limit int) ([]AgentEvent, error) {
	run := SanitizeID(runID, GenerateRunID())
	agent := SanitizeID(agentID, GenerateAgentID())
	return readSequencedJSONL[AgentEvent](c.AgentEventsPath(run, agent), afterSeq, limit)
}

func (c *Coordinator) WriteResult(runID string, agentID string, result AgentResult) error {
	run := SanitizeID(runID, GenerateRunID())
	agent := SanitizeID(agentID, GenerateAgentID())
	if result.RunID == "" {
		result.RunID = run
	}
	if result.AgentID == "" {
		result.AgentID = agent
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now().UTC()
	}
	return writeJSONAtomic(c.AgentResultPath(run, agent), result)
}

func (c *Coordinator) ReadResult(runID string, agentID string) (AgentResult, error) {
	run := SanitizeID(runID, GenerateRunID())
	agent := SanitizeID(agentID, GenerateAgentID())
	var out AgentResult
	if err := readJSONFile(c.AgentResultPath(run, agent), &out); err != nil {
		return AgentResult{}, err
	}
	return out, nil
}

func (c *Coordinator) AppendSignal(runID string, key string, sig Signal) (Signal, error) {
	run := SanitizeID(runID, GenerateRunID())
	signalKey := SanitizeID(key, "default")
	if sig.Key == "" {
		sig.Key = signalKey
	}
	if sig.CreatedAt.IsZero() {
		sig.CreatedAt = time.Now().UTC()
	}
	path := c.SignalPath(run, signalKey)
	seq, err := appendSequencedJSONL(path, func(seq int64) {
		sig.Seq = seq
	}, &sig)
	if err != nil {
		return Signal{}, err
	}
	sig.Seq = seq
	return sig, nil
}

func (c *Coordinator) ReadSignals(runID string, key string, afterSeq int64, limit int) ([]Signal, error) {
	run := SanitizeID(runID, GenerateRunID())
	signalKey := SanitizeID(key, "default")
	return readSequencedJSONL[Signal](c.SignalPath(run, signalKey), afterSeq, limit)
}

func (c *Coordinator) WaitForSignal(ctx context.Context, runID string, key string, afterSeq int64, timeout time.Duration, poll time.Duration) (Signal, error) {
	if poll <= 0 {
		poll = 300 * time.Millisecond
	}
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().UTC().Add(timeout)
	}
	for {
		signals, err := c.ReadSignals(runID, key, afterSeq, 1)
		if err != nil {
			return Signal{}, err
		}
		if len(signals) > 0 {
			return signals[0], nil
		}
		if !deadline.IsZero() && time.Now().UTC().After(deadline) {
			return Signal{}, ErrSignalWaitTimeout
		}
		select {
		case <-ctx.Done():
			return Signal{}, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (c *Coordinator) RunDir(runID string) string {
	id := SanitizeID(runID, GenerateRunID())
	return filepath.Join(c.RunRoot, id)
}

func (c *Coordinator) RunManifestPath(runID string) string {
	return filepath.Join(c.RunDir(runID), "run.json")
}

func (c *Coordinator) AgentsDir(runID string) string {
	return filepath.Join(c.RunDir(runID), "agents")
}

func (c *Coordinator) AgentDir(runID string, agentID string) string {
	agent := SanitizeID(agentID, GenerateAgentID())
	return filepath.Join(c.AgentsDir(runID), agent)
}

func (c *Coordinator) AgentSpecPath(runID string, agentID string) string {
	return filepath.Join(c.AgentDir(runID, agentID), "spec.json")
}

func (c *Coordinator) AgentStatePath(runID string, agentID string) string {
	return filepath.Join(c.AgentDir(runID, agentID), "state.json")
}

func (c *Coordinator) AgentCommandsPath(runID string, agentID string) string {
	return filepath.Join(c.AgentDir(runID, agentID), "commands.jsonl")
}

func (c *Coordinator) AgentEventsPath(runID string, agentID string) string {
	return filepath.Join(c.AgentDir(runID, agentID), "events.jsonl")
}

func (c *Coordinator) AgentResultPath(runID string, agentID string) string {
	return filepath.Join(c.AgentDir(runID, agentID), "result.json")
}

func (c *Coordinator) SignalsDir(runID string) string {
	return filepath.Join(c.RunDir(runID), "signals")
}

func (c *Coordinator) SignalPath(runID string, key string) string {
	signalKey := SanitizeID(key, "default")
	return filepath.Join(c.SignalsDir(runID), signalKey+".jsonl")
}

func normalizeCommandType(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case CommandPause:
		return CommandPause
	case CommandResume:
		return CommandResume
	case CommandCancel:
		return CommandCancel
	case CommandMessage:
		return CommandMessage
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp_json_*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func readJSONFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

type sequenced interface {
	GetSeq() int64
}

func readSequencedJSONL[T sequenced](path string, afterSeq int64, limit int) ([]T, error) {
	if limit <= 0 {
		limit = 100
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	out := make([]T, 0, min(limit, 16))
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*64)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item T
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		if item.GetSeq() <= afterSeq {
			continue
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return out, nil
}

func appendSequencedJSONL(path string, setSeq func(int64), payload any) (int64, error) {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	var seq int64
	err := withFileLock(lockPath, 5*time.Second, func() error {
		last, err := lastSequence(path)
		if err != nil {
			return err
		}
		seq = last + 1
		if setSeq != nil {
			setSeq(seq)
		}
		line, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return seq, nil
}

func lastSequence(path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()

	var last int64
	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024*64)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var node struct {
			Seq int64 `json:"seq"`
		}
		if err := json.Unmarshal([]byte(line), &node); err != nil {
			continue
		}
		if node.Seq > last {
			last = node.Seq
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return last, nil
}

func withFileLock(lockPath string, timeout time.Duration, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	start := time.Now().UTC()
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if timeout > 0 && time.Since(start) > timeout {
			return fmt.Errorf("acquire lock timeout: %s", lockPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer os.Remove(lockPath)
	return fn()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
