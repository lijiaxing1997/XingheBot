package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureSessionFromHistoryWritesSessionsFile(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig().WithDefaults()
	runID := "run-20260224-101010-acde01"
	now := time.Date(2026, 2, 24, 10, 10, 10, 0, time.UTC)

	historyPath := filepath.Join(t.TempDir(), "history.jsonl")
	lines := []map[string]any{
		{"role": "user", "content": "Remember this token sk-1234567890abcdef"},
		{"role": "assistant", "content": "Ok. I will do it."},
		{"role": "tool", "content": "noise"},
		{"role": "system", "content": "[System Message] Context compacted automatically due to context overflow.\n\nSummary of earlier conversation:\n- TODO: do X"},
	}
	f, err := os.OpenFile(historyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	for _, line := range lines {
		b, _ := json.Marshal(line)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()

	resp, err := CaptureSessionFromHistory(context.Background(), cfg, root, runID, historyPath, 10, now)
	if err != nil {
		t.Fatalf("CaptureSessionFromHistory: %v", err)
	}
	if resp.Disabled {
		t.Fatalf("expected enabled capture")
	}
	if resp.Path == "" {
		t.Fatalf("expected sessions path, got empty")
	}
	if !strings.HasPrefix(resp.Path, "sessions/") {
		t.Fatalf("unexpected path: %q", resp.Path)
	}

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(resp.Path)))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)
	if strings.Contains(got, "sk-1234567890abcdef") {
		t.Fatalf("expected secret redacted in capture, got: %q", got)
	}
	if !strings.Contains(got, "Compaction Summary") {
		t.Fatalf("expected compaction summary section, got: %q", got)
	}
	if !strings.Contains(got, "Messages (last") {
		t.Fatalf("expected messages section, got: %q", got)
	}
}
