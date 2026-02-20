package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSearchTool(t *testing.T, payload any) (string, error) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}
	tool := &SearchTool{}
	return tool.Call(context.Background(), json.RawMessage(data))
}

func TestSearchToolSupportsLogicalAndOr(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("apple banana\napple only\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("orange mango\norange pear\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	out, err := runSearchTool(t, map[string]any{
		"path":  dir,
		"query": "apple&banana|orange&pear",
	})
	if err != nil {
		t.Fatalf("search returned error: %v", err)
	}

	if !strings.Contains(out, "a.txt:1:apple banana") {
		t.Fatalf("expected AND match from a.txt, got:\n%s", out)
	}
	if !strings.Contains(out, "b.txt:2:orange pear") {
		t.Fatalf("expected AND match from b.txt, got:\n%s", out)
	}
	if strings.Contains(out, "orange mango") {
		t.Fatalf("did not expect partial AND match, got:\n%s", out)
	}
}

func TestSearchToolRespectsRecursiveAndHiddenOptions(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatalf("write root.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "child.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatalf("write child.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatalf("write .hidden.txt: %v", err)
	}

	out, err := runSearchTool(t, map[string]any{
		"path":      dir,
		"query":     "needle",
		"recursive": false,
	})
	if err != nil {
		t.Fatalf("search returned error: %v", err)
	}

	if !strings.Contains(out, "root.txt:1:needle") {
		t.Fatalf("expected root file match, got:\n%s", out)
	}
	if strings.Contains(out, "child.txt:1:needle") {
		t.Fatalf("did not expect recursive child match, got:\n%s", out)
	}
	if strings.Contains(out, ".hidden.txt:1:needle") {
		t.Fatalf("did not expect hidden file match by default, got:\n%s", out)
	}
}

func TestSearchToolRejectsInvalidQuery(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("foo bar\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	_, err := runSearchTool(t, map[string]any{
		"path":  dir,
		"query": "foo&&bar",
	})
	if err == nil {
		t.Fatalf("expected invalid query error")
	}
	if !strings.Contains(err.Error(), "invalid query") {
		t.Fatalf("expected invalid query message, got: %v", err)
	}
}
