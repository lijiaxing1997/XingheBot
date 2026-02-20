package multiagent

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusPaused    = "paused"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

const (
	CommandPause   = "pause"
	CommandResume  = "resume"
	CommandCancel  = "cancel"
	CommandMessage = "message"
)

type RunManifest struct {
	ID        string         `json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type AgentSpec struct {
	RunID       string         `json:"run_id"`
	ID          string         `json:"id"`
	Task        string         `json:"task"`
	MaxTurns    int            `json:"max_turns,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type AgentState struct {
	RunID      string    `json:"run_id"`
	AgentID    string    `json:"agent_id"`
	Status     string    `json:"status"`
	PID        int       `json:"pid,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	ResultPath string    `json:"result_path,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type AgentCommand struct {
	Seq       int64          `json:"seq"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

func (c AgentCommand) GetSeq() int64 { return c.Seq }

type AgentEvent struct {
	Seq       int64          `json:"seq"`
	Type      string         `json:"type"`
	Message   string         `json:"message,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

func (e AgentEvent) GetSeq() int64 { return e.Seq }

type AgentResult struct {
	RunID      string    `json:"run_id"`
	AgentID    string    `json:"agent_id"`
	Status     string    `json:"status"`
	Output     string    `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
	FinishedAt time.Time `json:"finished_at"`
}

type Signal struct {
	Seq         int64          `json:"seq"`
	Key         string         `json:"key"`
	FromAgentID string         `json:"from_agent_id,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

func (s Signal) GetSeq() int64 { return s.Seq }

func IsTerminalStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case StatusCompleted, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

func GenerateRunID() string {
	return "run-" + time.Now().UTC().Format("20060102-150405") + "-" + randomHex(3)
}

func GenerateAgentID() string {
	return "agent-" + randomHex(4)
}

func SanitizeID(raw string, fallback string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		s = strings.TrimSpace(fallback)
	}
	if s == "" {
		s = "id-" + randomHex(4)
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "id-" + randomHex(4)
	}
	return out
}

func randomHex(n int) string {
	if n <= 0 {
		n = 4
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		now := time.Now().UTC().UnixNano()
		return strings.ReplaceAll(time.Unix(0, now).UTC().Format("150405.000000000"), ".", "")
	}
	return hex.EncodeToString(buf)
}
