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

	"test_skill_agent/internal/llm"
)

type CaptureResponse struct {
	Path          string `json:"path"`
	Messages      int    `json:"messages"`
	Disabled      bool   `json:"disabled"`
	Root          string `json:"root"`
	FlushPath     string `json:"flush_path,omitempty"`
	FlushAppended int    `json:"flush_appended,omitempty"`
	FlushError    string `json:"flush_error,omitempty"`
}

type historyMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	TS      string `json:"ts,omitempty"`
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

	historyInfo, _ := f.Stat()

	transcript := make([]historyMessage, 0, minInt(maxMessages, 64))
	compactionSummary := ""
	compactionSummaryAt := ""
	toolNameByCallID := make(map[string]string)

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
		var msg struct {
			llm.Message
			TS string `json:"ts,omitempty"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			if role != "assistant" || len(msg.ToolCalls) == 0 {
				continue
			}
		}

		if role == "system" && strings.Contains(content, "Summary of earlier conversation:") && strings.Contains(content, "Context compacted automatically") {
			compactionSummary = extractSummarySection(content)
			compactionSummaryAt = strings.TrimSpace(msg.TS)
			continue
		}

		if role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, call := range msg.ToolCalls {
				id := strings.TrimSpace(call.ID)
				name := strings.TrimSpace(call.Function.Name)
				if id == "" || name == "" {
					continue
				}
				toolNameByCallID[id] = name
			}
		}

		keep := role == "user" || role == "assistant" || role == "tool"
		if !keep {
			continue
		}
		content = oneLine(content)
		if content == "" {
			continue
		}
		if isLikelyPromptInjection(content) {
			continue
		}
		maxRunes := 900
		if role == "tool" {
			maxRunes = 700
		}
		if runeLen(content) > maxRunes {
			content = truncateRunes(content, maxRunes) + "…"
		}
		content, _ = RedactText(cfg, content)

		if role == "tool" {
			name := strings.TrimSpace(toolNameByCallID[strings.TrimSpace(msg.ToolCallID)])
			if name != "" {
				content = fmt.Sprintf("tool(%s): %s", name, strings.TrimSpace(content))
			} else if strings.TrimSpace(msg.ToolCallID) != "" {
				content = fmt.Sprintf("tool(call_id=%s): %s", strings.TrimSpace(msg.ToolCallID), strings.TrimSpace(content))
			} else {
				content = fmt.Sprintf("tool: %s", strings.TrimSpace(content))
			}
		}

		transcript = append(transcript, historyMessage{Role: role, Content: content, TS: strings.TrimSpace(msg.TS)})
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
		if strings.TrimSpace(compactionSummaryAt) != "" {
			b.WriteString("- at: " + strings.TrimSpace(compactionSummaryAt) + "\n")
		}
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
		at := strings.TrimSpace(msg.TS)
		if at == "" {
			at = "?"
		}
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "unknown"
		}
		b.WriteString("- " + at + " " + role + ": " + strings.TrimSpace(msg.Content) + "\n")
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

	if cfg.AutoFlushOnSessionCapture != nil && !*cfg.AutoFlushOnSessionCapture {
		return resp, nil
	}

	flushText := buildCaptureFlushText(compactionSummary, transcript)
	if strings.TrimSpace(flushText) == "" {
		return resp, nil
	}

	if historyInfo != nil && shouldSkipCaptureFlush(root, id, historyInfo.Size(), historyInfo.ModTime()) {
		return resp, nil
	}

	flushResp, err := FlushFromText(ctx, cfg, root, flushText, 12, id, now)
	if err != nil {
		resp.FlushError = err.Error()
		return resp, nil
	}
	resp.FlushPath = strings.TrimSpace(flushResp.Path)
	resp.FlushAppended = flushResp.Appended
	if historyInfo != nil {
		_ = writeCaptureFlushMarker(root, id, historyInfo.Size(), historyInfo.ModTime())
	}
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

type captureFlushMarker struct {
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

func captureFlushMarkerPath(root string, runID string) string {
	key := sanitizeKey(runID)
	if key == "" {
		key = "run"
	}
	return filepath.Join(root, "index", ".session_capture_flush."+key+".json")
}

func shouldSkipCaptureFlush(root string, runID string, size int64, modTime time.Time) bool {
	if size < 0 {
		return false
	}
	path := captureFlushMarkerPath(root, runID)
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var marker captureFlushMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return false
	}
	if marker.Size != size {
		return false
	}
	prev, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(marker.ModTime))
	if err != nil {
		return false
	}
	return prev.UTC().Equal(modTime.UTC())
}

func writeCaptureFlushMarker(root string, runID string, size int64, modTime time.Time) error {
	if size < 0 {
		size = 0
	}
	path := captureFlushMarkerPath(root, runID)
	marker := captureFlushMarker{
		Size:    size,
		ModTime: modTime.UTC().Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o644)
}

func buildCaptureFlushText(compactionSummary string, transcript []historyMessage) string {
	add := func(lines *[]string, text string) {
		t := strings.TrimSpace(text)
		if t == "" {
			return
		}
		t = strings.TrimPrefix(t, "-")
		t = strings.TrimPrefix(t, "*")
		t = strings.TrimPrefix(t, "•")
		t = strings.TrimSpace(t)
		if t == "" {
			return
		}
		if isLikelyPromptInjection(t) {
			return
		}
		kind, _ := classifyDurableLine(t)
		if strings.TrimSpace(kind) == "" || kind == "note" {
			return
		}
		*lines = append(*lines, t)
	}

	lines := make([]string, 0, 24)
	for _, line := range strings.Split(compactionSummary, "\n") {
		add(&lines, line)
	}
	for _, msg := range transcript {
		add(&lines, msg.Content)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
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
