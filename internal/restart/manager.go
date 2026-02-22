package restart

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Sentinel struct {
	Version int           `json:"version"`
	Payload SentinelEntry `json:"payload"`
}

type SentinelEntry struct {
	Kind    string    `json:"kind"`
	Status  string    `json:"status"`
	TS      time.Time `json:"ts"`
	App     string    `json:"app,omitempty"`
	Version string    `json:"version,omitempty"`
	PID     int       `json:"pid,omitempty"`
	RunID   string    `json:"run_id,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Note    string    `json:"note,omitempty"`
}

type Manager struct {
	sentinelPath string
	requested    atomic.Bool
	mu           sync.Mutex
	lastRequest  SentinelEntry
}

func NewManager(sentinelPath string) *Manager {
	trimmed := strings.TrimSpace(sentinelPath)
	if trimmed == "" {
		return &Manager{}
	}
	return &Manager{sentinelPath: filepath.Clean(trimmed)}
}

func (m *Manager) SentinelPath() string {
	if m == nil {
		return ""
	}
	return m.sentinelPath
}

func (m *Manager) IsRestartRequested() bool {
	if m == nil {
		return false
	}
	return m.requested.Load()
}

func (m *Manager) LastRequest() (SentinelEntry, bool) {
	if m == nil {
		return SentinelEntry{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.requested.Load() {
		return SentinelEntry{}, false
	}
	return m.lastRequest, true
}

func (m *Manager) RequestRestart(entry SentinelEntry) (string, bool, error) {
	if m == nil {
		return "", false, errors.New("restart manager is nil")
	}
	if m.sentinelPath == "" {
		return "", false, errors.New("restart sentinel path is empty")
	}

	entry.Kind = "restart"
	if entry.Status == "" {
		entry.Status = "ok"
	}
	if entry.TS.IsZero() {
		entry.TS = time.Now().UTC()
	}
	if entry.PID == 0 {
		entry.PID = os.Getpid()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.requested.Load() {
		return m.sentinelPath, false, nil
	}

	if err := writeSentinelAtomic(m.sentinelPath, entry); err != nil {
		return "", false, err
	}
	m.lastRequest = entry
	m.requested.Store(true)
	return m.sentinelPath, true, nil
}

func (m *Manager) ConsumeSentinel() (*Sentinel, error) {
	if m == nil {
		return nil, errors.New("restart manager is nil")
	}
	return consumeSentinel(m.sentinelPath)
}

func writeSentinelAtomic(path string, payload SentinelEntry) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("sentinel path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(Sentinel{
		Version: 1,
		Payload: payload,
	}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".restart-sentinel-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	shouldRemove := true
	defer func() {
		_ = tmp.Close()
		if shouldRemove {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	shouldRemove = false

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

func consumeSentinel(path string) (*Sentinel, error) {
	p := filepath.Clean(strings.TrimSpace(path))
	if p == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out Sentinel
	if err := json.Unmarshal(raw, &out); err != nil {
		_ = os.Remove(p)
		return nil, nil
	}
	if out.Version != 1 {
		_ = os.Remove(p)
		return nil, nil
	}
	_ = os.Remove(p)
	return &out, nil
}

func FormatSentinelMessage(s *Sentinel) string {
	if s == nil {
		return ""
	}
	p := s.Payload
	note := strings.TrimSpace(p.Note)
	reason := strings.TrimSpace(p.Reason)
	switch {
	case note != "" && reason != "":
		return fmt.Sprintf("Restarted (%s): %s", reason, note)
	case note != "":
		return "Restarted: " + note
	case reason != "":
		return "Restarted (" + reason + ")."
	default:
		return "Restarted."
	}
}

func ResolveSentinelPath(multiAgentRoot string) string {
	root := strings.TrimSpace(multiAgentRoot)
	if root == "" {
		root = ".multi_agent/runs"
	}
	root = filepath.Clean(root)
	base := filepath.Dir(root)
	if base == "" {
		base = "."
	}
	return filepath.Join(base, "restart-sentinel.json")
}
