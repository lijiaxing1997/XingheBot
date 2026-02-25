package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"test_skill_agent/internal/llm"
)

type fakeChatClient struct {
	out        string
	err        error
	calls      int
	lastPrompt string
}

func (f *fakeChatClient) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	f.calls++
	if len(req.Messages) > 0 {
		f.lastPrompt = req.Messages[len(req.Messages)-1].Content
	}
	if f.err != nil {
		return nil, f.err
	}
	return &llm.ChatResponse{
		Choices: []llm.Choice{
			{Message: llm.Message{Role: "assistant", Content: f.out}},
		},
	}, nil
}

func TestUpdateMemoryMDFromTurn_WritesFileAndStamp(t *testing.T) {
	root := t.TempDir()
	if err := EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	memPath := filepath.Join(root, "MEMORY.md")
	if err := os.WriteFile(memPath, []byte("# MEMORY\n\n## Preferences\n- old\n"), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	cfg := DefaultConfig().WithDefaults()
	cfg.Timezone = "UTC"
	now := time.Date(2026, 2, 25, 3, 16, 14, 0, time.UTC)
	client := &fakeChatClient{
		out: strings.TrimSpace("```markdown\n# MEMORY\n\n## Preferences\n- 用户希望被称呼为“老板” (source=run-1)\n```"),
	}

	resp, err := UpdateMemoryMDFromTurn(context.Background(), client, cfg, root, MemoryMDUpdateInput{
		RunID:          "run-1",
		RunTitle:       "天气查询",
		UserRequest:    "以后叫我老板",
		AssistantFinal: "好的，老板！记住了。",
		Now:            now,
	})
	if err != nil {
		t.Fatalf("UpdateMemoryMDFromTurn: %v", err)
	}
	if !resp.Updated {
		t.Fatalf("expected Updated=true, got false (resp=%+v)", resp)
	}
	if client.calls != 1 {
		t.Fatalf("expected 1 chat call, got %d", client.calls)
	}
	if !strings.Contains(client.lastPrompt, "run_title: 天气查询") {
		t.Fatalf("expected prompt to include run_title, got:\n%s", client.lastPrompt)
	}
	if !strings.Contains(client.lastPrompt, "at_minute: 2026-02-25 03:16") {
		t.Fatalf("expected prompt to include at_minute, got:\n%s", client.lastPrompt)
	}

	updated, err := os.ReadFile(memPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	text := strings.TrimSpace(string(updated))
	if !strings.HasPrefix(text, "# MEMORY") {
		t.Fatalf("expected # MEMORY prefix, got: %q", text[:min(80, len(text))])
	}
	if !strings.Contains(text, "memory_md_update:") {
		t.Fatalf("expected memory_md_update stamp, got:\n%s", text)
	}
	if !strings.Contains(text, "老板") {
		t.Fatalf("expected preference persisted, got:\n%s", text)
	}
	if runeLen(text) > cfg.MemoryMDMaxChars {
		t.Fatalf("expected <= %d chars, got %d", cfg.MemoryMDMaxChars, runeLen(text))
	}
}

func TestUpdateMemoryMDFromTurn_EnforcesMaxCharsWithoutSecondModelCall(t *testing.T) {
	root := t.TempDir()
	if err := EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}

	cfg := DefaultConfig().WithDefaults()
	cfg.MemoryMDMaxChars = 200
	now := time.Date(2026, 2, 25, 3, 16, 14, 0, time.UTC)
	client := &fakeChatClient{
		out: "# MEMORY\n\n## Notes\n- " + strings.Repeat("a", 1200),
	}

	resp, err := UpdateMemoryMDFromTurn(context.Background(), client, cfg, root, MemoryMDUpdateInput{
		RunID:       "run-2",
		UserRequest: "记住一些很长的内容",
		Now:         now,
	})
	if err != nil {
		t.Fatalf("UpdateMemoryMDFromTurn: %v", err)
	}
	if !resp.Updated {
		t.Fatalf("expected Updated=true, got false (resp=%+v)", resp)
	}
	if client.calls != 1 {
		t.Fatalf("expected 1 chat call, got %d", client.calls)
	}

	memPath := filepath.Join(root, "MEMORY.md")
	updated, err := os.ReadFile(memPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	text := strings.TrimSpace(string(updated))
	if runeLen(text) > cfg.MemoryMDMaxChars {
		t.Fatalf("expected <= %d chars, got %d", cfg.MemoryMDMaxChars, runeLen(text))
	}
	if !strings.Contains(text, "memory_md_update:") {
		t.Fatalf("expected stamp preserved under truncation, got:\n%s", text)
	}
	if resp.Compressed != true {
		t.Fatalf("expected Compressed=true when over limit, got false")
	}
}

func TestUpdateMemoryMDFromTurn_DisabledSkipsModelCall(t *testing.T) {
	root := t.TempDir()
	if err := EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}

	cfg := DefaultConfig().WithDefaults()
	disabled := false
	cfg.AutoUpdateMemoryMD = &disabled
	client := &fakeChatClient{out: "# MEMORY\n- should not be called"}

	resp, err := UpdateMemoryMDFromTurn(context.Background(), client, cfg, root, MemoryMDUpdateInput{
		RunID:       "run-3",
		UserRequest: "test",
		Now:         time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("UpdateMemoryMDFromTurn: %v", err)
	}
	if resp.Updated {
		t.Fatalf("expected Updated=false when disabled")
	}
	if client.calls != 0 {
		t.Fatalf("expected 0 chat calls when disabled, got %d", client.calls)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
