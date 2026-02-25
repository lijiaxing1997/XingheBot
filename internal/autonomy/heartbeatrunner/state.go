package heartbeatrunner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const heartbeatStateVersion = 1

type heartbeatState struct {
	Version int `json:"version"`

	LastRunAt  time.Time `json:"last_run_at,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	LastReason string    `json:"last_reason,omitempty"`

	LastSentAt      time.Time `json:"last_sent_at,omitempty"`
	LastSentHash    string    `json:"last_sent_hash,omitempty"`
	LastSentPreview string    `json:"last_sent_preview,omitempty"`
}

func loadHeartbeatState(path string) (heartbeatState, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return heartbeatState{}, errors.New("state path is empty")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return heartbeatState{Version: heartbeatStateVersion}, nil
		}
		return heartbeatState{}, err
	}
	var st heartbeatState
	if err := json.Unmarshal(data, &st); err != nil {
		return heartbeatState{}, fmt.Errorf("parse heartbeat state: %w", err)
	}
	if st.Version <= 0 {
		st.Version = heartbeatStateVersion
	}
	return st, nil
}

func saveHeartbeatState(path string, st heartbeatState) error {
	p := strings.TrimSpace(path)
	if p == "" {
		return errors.New("state path is empty")
	}
	st.Version = heartbeatStateVersion
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", p, time.Now().UTC().UnixNano())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func hashHeartbeatText(text string) string {
	raw := strings.TrimSpace(text)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func previewHeartbeatText(text string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 800
	}
	s := strings.TrimSpace(text)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return strings.TrimSpace(string(r[:maxRunes])) + "…"
}

func withFileLock(lockPath string, timeout time.Duration, fn func() error) error {
	if strings.TrimSpace(lockPath) == "" {
		return errors.New("lock path is empty")
	}
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
