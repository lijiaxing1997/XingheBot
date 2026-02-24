package memory

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAppendSearchGetRoundtrip(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig().WithDefaults()

	ctx := context.Background()
	now := time.Date(2026, 2, 24, 10, 12, 33, 0, time.UTC)
	appendResp, err := AppendDaily(ctx, cfg, root, "pref", "User prefers concise output. key=sk-1234567890abcdef", []string{"prefs"}, "test", now)
	if err != nil {
		t.Fatalf("AppendDaily: %v", err)
	}
	if appendResp.Path != "daily/2026-02-24.md" {
		t.Fatalf("unexpected append path: %q", appendResp.Path)
	}
	if strings.Contains(appendResp.Line, "sk-1234567890abcdef") {
		t.Fatalf("expected secret to be redacted, got: %q", appendResp.Line)
	}

	searchResp, err := Search(ctx, cfg, root, "prefers concise", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(searchResp.Results) == 0 {
		t.Fatalf("expected search results, got 0")
	}
	if searchResp.Results[0].Path != "daily/2026-02-24.md" {
		t.Fatalf("unexpected result path: %q", searchResp.Results[0].Path)
	}

	getResp, err := Get(ctx, root, "daily/2026-02-24.md", 1, 5)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(getResp.Text, "prefers concise") {
		t.Fatalf("expected get text to contain appended line, got: %q", getResp.Text)
	}
	if strings.Contains(getResp.Text, "sk-1234567890abcdef") {
		t.Fatalf("expected stored content to be redacted, got: %q", getResp.Text)
	}
}

func TestGetRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	_, err := Get(context.Background(), root, "../etc/passwd", 1, 10)
	if err == nil {
		t.Fatalf("expected traversal to be rejected")
	}
}

func TestSearchSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test skipped on windows")
	}
	root := t.TempDir()
	cfg := DefaultConfig().WithDefaults()
	if err := EnsureLayout(root); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside secret"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(root, "daily", "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	resp, err := Search(context.Background(), cfg, root, "outside", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range resp.Results {
		if r.Path == "daily/link.md" {
			t.Fatalf("expected symlink file to be skipped")
		}
	}

	_, err = Get(context.Background(), root, "daily/link.md", 1, 5)
	if err == nil {
		t.Fatalf("expected Get to reject symlink")
	}
}

func TestSearchSkipsSymlinkDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test skipped on windows")
	}
	root := t.TempDir()
	cfg := DefaultConfig().WithDefaults()

	outsideDir := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "2026-02-24.md"), []byte("outside marker"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := os.Symlink(outsideDir, filepath.Join(root, "daily")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	resp, err := Search(context.Background(), cfg, root, "outside marker", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("expected 0 results due to symlink dir, got %d", len(resp.Results))
	}
}
