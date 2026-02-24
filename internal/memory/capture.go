package memory

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type CaptureResponse struct {
	Path     string `json:"path"`
	Messages int    `json:"messages"`
	Disabled bool   `json:"disabled"`
	Root     string `json:"root"`
}

type historyMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func CaptureSessionFromHistory(ctx context.Context, cfg Config, root string, runID string, historyPath string, maxMessages int, now time.Time) (CaptureResponse, error) {
	cfg = cfg.WithDefaults()
	resp := CaptureResponse{Disabled: false, Root: root}
	if cfg.Enabled != nil && !*cfg.Enabled {
		resp.Disabled = true
		return resp, nil
	}

	id := strings.TrimSpace(runID)
	if id == "" {
		return CaptureResponse{}, errors.New("run_id is required")
	}
	if strings.TrimSpace(historyPath) == "" {
		return CaptureResponse{}, errors.New("history_path is required")
	}
	if maxMessages <= 0 {
		maxMessages = 60
	}
	if maxMessages > 200 {
		maxMessages = 200
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if err := EnsureLayout(root); err != nil {
		return CaptureResponse{}, err
	}

	f, err := os.Open(historyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resp, nil
		}
		return CaptureResponse{}, err
	}
	defer f.Close()

	transcript := make([]historyMessage, 0, minInt(maxMessages, 64))
	compactionSummary := ""

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return CaptureResponse{}, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg historyMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}

		if role == "system" && strings.Contains(content, "Summary of earlier conversation:") && strings.Contains(content, "Context compacted automatically") {
			compactionSummary = extractSummarySection(content)
			continue
		}

		if role != "user" && role != "assistant" {
			continue
		}
		content = oneLine(content)
		if content == "" {
			continue
		}
		if isLikelyPromptInjection(content) {
			continue
		}
		if runeLen(content) > 900 {
			content = truncateRunes(content, 900) + "…"
		}
		content, _ = RedactText(cfg, content)

		transcript = append(transcript, historyMessage{Role: role, Content: content})
		if len(transcript) > maxMessages {
			transcript = transcript[len(transcript)-maxMessages:]
		}
	}
	if err := scanner.Err(); err != nil {
		return CaptureResponse{}, err
	}

	compactionSummary = strings.TrimSpace(compactionSummary)
	if compactionSummary != "" {
		compactionSummary, _ = RedactText(cfg, compactionSummary)
		if runeLen(compactionSummary) > 4000 {
			compactionSummary = truncateRunes(compactionSummary, 4000) + "…"
		}
	}

	if len(transcript) == 0 && compactionSummary == "" {
		return resp, nil
	}

	var b strings.Builder
	b.WriteString("# Session Capture\n\n")
	b.WriteString("- run_id: " + id + "\n")
	b.WriteString("- captured_at: " + now.UTC().Format(time.RFC3339) + "\n\n")

	if compactionSummary != "" {
		b.WriteString("## Compaction Summary\n\n")
		for _, line := range strings.Split(compactionSummary, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if isLikelyPromptInjection(line) {
				continue
			}
			b.WriteString("- " + oneLine(line) + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("## Messages (last %d)\n\n", len(transcript)))
	for _, msg := range transcript {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "unknown"
		}
		b.WriteString("- " + role + ": " + strings.TrimSpace(msg.Content) + "\n")
	}
	content := b.String()
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	date := now.UTC().Format("2006-01-02")
	rel := path.Join("sessions", fmt.Sprintf("%s-%s.md", date, id))
	abs, cleanRel, err := safeResolveMarkdownPath(root, rel, true)
	if err != nil {
		return CaptureResponse{}, err
	}
	if err := writeFileAtomic(abs, []byte(content), 0o644); err != nil {
		return CaptureResponse{}, err
	}
	resp.Path = cleanRel
	resp.Messages = len(transcript)
	return resp, nil
}

func extractSummarySection(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	const marker = "Summary of earlier conversation:"
	if idx := strings.LastIndex(raw, marker); idx >= 0 {
		return strings.TrimSpace(raw[idx+len(marker):])
	}
	return raw
}

func writeFileAtomic(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp.*.md")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if _, err := tmp.Write(content); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(path)
		if err2 := os.Rename(tmpPath, path); err2 != nil {
			_ = os.Remove(tmpPath)
			return err
		}
	}
	return nil
}
