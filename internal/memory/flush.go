package memory

import (
	"context"
	"errors"
	"strings"
	"time"
)

type FlushItem struct {
	Kind string
	Text string
	Tags []string
}

type FlushResponse struct {
	Path     string `json:"path"`
	Appended int    `json:"appended"`
	Skipped  int    `json:"skipped"`
	Disabled bool   `json:"disabled"`
	Backend  string `json:"backend"`
	Root     string `json:"root"`
}

func FlushFromText(ctx context.Context, cfg Config, root string, text string, maxItems int, source string, now time.Time) (FlushResponse, error) {
	cfg = cfg.WithDefaults()
	resp := FlushResponse{
		Disabled: false,
		Backend:  "scan",
		Root:     root,
	}
	if cfg.Enabled != nil && !*cfg.Enabled {
		resp.Disabled = true
		resp.Backend = "disabled"
		return resp, nil
	}
	if strings.TrimSpace(text) == "" {
		return FlushResponse{}, errors.New("text is required")
	}
	if maxItems <= 0 {
		maxItems = 12
	}
	if maxItems > 40 {
		maxItems = 40
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if err := EnsureLayout(root); err != nil {
		return FlushResponse{}, err
	}

	items := ExtractDurableNotes(text, maxItems)
	if len(items) == 0 {
		return resp, nil
	}

	appended := 0
	skipped := 0
	path := ""
	for _, item := range items {
		kind := strings.TrimSpace(item.Kind)
		if kind == "" {
			kind = "note"
		}
		t := strings.TrimSpace(item.Text)
		if t == "" {
			skipped++
			continue
		}
		ar, err := AppendDaily(ctx, cfg, root, kind, t, item.Tags, source, now)
		if err != nil {
			return FlushResponse{}, err
		}
		if path == "" {
			path = ar.Path
		}
		appended++
	}
	resp.Path = path
	resp.Appended = appended
	resp.Skipped = skipped
	return resp, nil
}

func ExtractDurableNotes(text string, maxItems int) []FlushItem {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return nil
	}
	if maxItems <= 0 {
		maxItems = 12
	}
	if maxItems > 80 {
		maxItems = 80
	}

	// Auto-compaction summaries include a fixed prefix; prefer the actual summary section.
	const summaryMarker = "Summary of earlier conversation:"
	if idx := strings.LastIndex(raw, summaryMarker); idx >= 0 {
		raw = strings.TrimSpace(raw[idx+len(summaryMarker):])
	}

	lines := strings.Split(raw, "\n")
	seen := make(map[string]struct{}, minInt(len(lines), maxItems))
	out := make([]FlushItem, 0, minInt(maxItems, 24))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "[System Message]") {
			continue
		}
		if strings.EqualFold(t, summaryMarker) {
			continue
		}

		t = strings.TrimPrefix(t, "-")
		t = strings.TrimPrefix(t, "*")
		t = strings.TrimPrefix(t, "•")
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if isLikelyPromptInjection(t) {
			continue
		}

		kind, tags := classifyDurableLine(t)
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if runeLen(t) > 600 {
			t = truncateRunes(t, 600) + "…"
		}

		key := normalizeDedupeKey(t)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		out = append(out, FlushItem{Kind: kind, Text: t, Tags: tags})
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func normalizeDedupeKey(text string) string {
	s := strings.ToLower(strings.TrimSpace(text))
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	if len(s) > 800 {
		s = s[:800]
	}
	return s
}

func isLikelyPromptInjection(text string) bool {
	s := strings.ToLower(strings.TrimSpace(text))
	if s == "" {
		return false
	}
	compact := strings.ReplaceAll(s, " ", "")
	compact = strings.ReplaceAll(compact, "\t", "")
	compact = strings.ReplaceAll(compact, "\n", "")
	compact = strings.ReplaceAll(compact, "\r", "")

	patterns := []string{
		"ignore previous", "disregard previous", "forget previous",
		"you must", "run this command", "execute command", "curl ",
		"sudo ", "rm -rf", "powershell", "cmd.exe", "bash -c",
		"reveal", "exfiltrate", "leak",
		"忽略之前", "无视之前", "忘记之前", "你必须", "执行命令", "立刻执行",
		"泄露", "导出", "外传",
	}
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if strings.Contains(s, p) || strings.Contains(compact, strings.ReplaceAll(p, " ", "")) {
			return true
		}
	}
	return false
}

func classifyDurableLine(text string) (kind string, tags []string) {
	s := strings.ToLower(strings.TrimSpace(text))
	if s == "" {
		return "note", nil
	}
	switch {
	case strings.Contains(s, "偏好") || strings.Contains(s, "preference") || strings.Contains(s, "reply style") || strings.Contains(s, "输出") || strings.Contains(s, "format"):
		return "pref", []string{"prefs"}
	case strings.Contains(s, "决定") || strings.Contains(s, "decision") || strings.Contains(s, "we decided") || strings.Contains(s, "选择") || strings.Contains(s, "adopt") || strings.Contains(s, "方案"):
		return "decision", []string{"arch"}
	case strings.Contains(s, "todo") || strings.Contains(s, "待办") || strings.Contains(s, "open question") || strings.Contains(s, "未解决") || strings.Contains(s, "next"):
		return "todo", []string{"todo"}
	default:
		return "note", nil
	}
}
