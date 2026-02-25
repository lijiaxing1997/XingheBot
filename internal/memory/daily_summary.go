package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
)

const dailySummaryMarkerPrefix = "<!-- auto_daily_summary:"

func RunAutoDailySummaryLoop(ctx context.Context, client chatClient, configPath string, workDir string) {
	if client == nil {
		return
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return
	}
	cfg = cfg.WithDefaults()
	if cfg.Enabled != nil && !*cfg.Enabled {
		return
	}
	if cfg.AutoDailySummary != nil && !*cfg.AutoDailySummary {
		return
	}
	loc, _ := ResolveLocation(cfg.Timezone)
	if loc == nil {
		loc = time.Local
	}
	paths, err := ResolvePaths(cfg, workDir)
	if err != nil {
		return
	}
	if err := EnsureLayout(paths.RootDir); err != nil {
		return
	}

	// Startup catch-up: ensure yesterday has a daily summary.
	nowLoc := time.Now().In(loc)
	yesterday := nowLoc.AddDate(0, 0, -1)
	_ = ensureDailySummaryForDate(ctx, client, cfg, paths.RootDir, yesterday, loc)

	for {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
		next := nextLocalMidnight(time.Now().In(loc), loc)
		d := time.Until(next)
		if d < 0 {
			d = time.Second
		}
		timer := time.NewTimer(d)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
		timer.Stop()

		// At midnight: summarize the day that just ended.
		nowLoc = time.Now().In(loc)
		prevDay := nowLoc.AddDate(0, 0, -1)
		_ = ensureDailySummaryForDate(ctx, client, cfg, paths.RootDir, prevDay, loc)
	}
}

func nextLocalMidnight(now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	t := now.In(loc)
	startOfDay := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	return startOfDay.AddDate(0, 0, 1)
}

func ensureDailySummaryForDate(ctx context.Context, client chatClient, cfg Config, root string, day time.Time, loc *time.Location) error {
	cfg = cfg.WithDefaults()
	if client == nil {
		return errors.New("llm client is nil")
	}
	if strings.TrimSpace(root) == "" {
		return errors.New("memory root is empty")
	}
	if loc == nil {
		loc = time.Local
	}
	dayLoc := day.In(loc)
	dateStr := dayLoc.Format("2006-01-02")
	marker := dailySummaryMarker(dateStr)
	lockPath := filepath.Join(root, "index", ".daily_summary.lock")

	if ctx == nil {
		ctx = context.Background()
	}

	return withFileLock(ctx, lockPath, 20*time.Second, func() error {
		if err := EnsureLayout(root); err != nil {
			return err
		}
		rel := path.Join("daily", dateStr+".md")
		abs, cleanRel, err := safeResolveMarkdownPath(root, rel, true)
		if err != nil {
			return err
		}

		existing := ""
		if data, err := os.ReadFile(abs); err == nil {
			existing = string(data)
		}
		if strings.Contains(existing, marker) {
			return nil
		}

		sourceLines, runIDs, err := collectSessionsLinesForDay(ctx, cfg, root, dayLoc, loc)
		if err != nil {
			return err
		}
		if len(sourceLines) == 0 {
			// Nothing to summarize; do not write a marker so a future capture can still backfill.
			return nil
		}

		summary, err := summarizeDayFromLines(ctx, client, cfg, dateStr, runIDs, sourceLines)
		if err != nil {
			return err
		}
		summary = strings.TrimSpace(summary)
		if summary == "" {
			return nil
		}
		summary, _ = RedactText(cfg, summary)
		if runeLen(summary) > 6000 {
			summary = truncateRunes(summary, 6000) + "…"
		}

		block := buildDailySummaryBlock(dateStr, marker, time.Now().In(loc), runIDs, summary)
		newText := strings.TrimRight(existing, "\n")
		if newText != "" {
			newText += "\n\n"
		}
		newText += block
		if err := writeFileAtomic(abs, []byte(ensureTrailingNewline(newText)), 0o644); err != nil {
			return err
		}
		_ = cleanRel
		return nil
	})
}

func dailySummaryMarker(date string) string {
	d := strings.TrimSpace(date)
	if d == "" {
		d = "unknown"
	}
	return fmt.Sprintf("%s %s -->", dailySummaryMarkerPrefix, d)
}

func collectSessionsLinesForDay(ctx context.Context, cfg Config, root string, day time.Time, loc *time.Location) ([]string, []string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, nil, errors.New("memory root is empty")
	}
	if loc == nil {
		loc = time.Local
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)

	sessionsDir := filepath.Join(root, "sessions")
	info, err := os.Lstat(sessionsDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, nil
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, nil, nil
	}
	files := make([]string, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if ent.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		files = append(files, filepath.Join(sessionsDir, name))
	}
	sortFilesByMtimeDesc(files)

	type timedLine struct {
		ts   time.Time
		line string
		run  string
	}
	collected := make([]timedLine, 0, 256)
	runSet := make(map[string]struct{}, 16)

	for _, file := range files {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		runID := ""
		inMessages := false
		for _, raw := range lines {
			trimmed := strings.TrimSpace(raw)
			if strings.HasPrefix(trimmed, "- run_id:") && runID == "" {
				runID = strings.TrimSpace(strings.TrimPrefix(trimmed, "- run_id:"))
				continue
			}
			if strings.HasPrefix(trimmed, "## Messages") {
				inMessages = true
				continue
			}
			if strings.HasPrefix(trimmed, "## ") && inMessages {
				inMessages = false
				continue
			}
			if !inMessages {
				continue
			}
			if !strings.HasPrefix(trimmed, "- ") {
				continue
			}
			rest := strings.TrimSpace(trimmed[2:])
			fields := strings.Fields(rest)
			if len(fields) < 2 {
				continue
			}
			tsToken := strings.TrimSpace(fields[0])
			if tsToken == "" || tsToken == "?" {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, tsToken)
			if err != nil {
				ts, err = time.Parse(time.RFC3339, tsToken)
				if err != nil {
					continue
				}
			}
			tsLoc := ts.In(loc)
			if tsLoc.Before(start) || !tsLoc.Before(end) {
				continue
			}
			line := "- " + ts.Format(time.RFC3339) + " "
			if strings.TrimSpace(runID) != "" {
				line += "(run=" + strings.TrimSpace(runID) + ") "
			}
			line += strings.TrimSpace(strings.TrimPrefix(rest, tsToken))
			line, _ = RedactText(cfg, line)
			collected = append(collected, timedLine{ts: tsLoc, line: line, run: strings.TrimSpace(runID)})
			if strings.TrimSpace(runID) != "" {
				runSet[strings.TrimSpace(runID)] = struct{}{}
			}
		}
	}

	if len(collected) == 0 {
		return nil, nil, nil
	}
	sort.Slice(collected, func(i, j int) bool {
		a := collected[i]
		b := collected[j]
		if !a.ts.Equal(b.ts) {
			return a.ts.Before(b.ts)
		}
		if a.run != b.run {
			return a.run < b.run
		}
		return a.line < b.line
	})

	out := make([]string, 0, len(collected))
	for _, item := range collected {
		out = append(out, item.line)
		if len(out) >= 600 {
			break
		}
	}

	runIDs := make([]string, 0, len(runSet))
	for id := range runSet {
		runIDs = append(runIDs, id)
	}
	sort.Strings(runIDs)
	return out, runIDs, nil
}

func summarizeDayFromLines(ctx context.Context, client chatClient, cfg Config, dateStr string, runIDs []string, lines []string) (string, error) {
	if client == nil {
		return "", errors.New("llm client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	runText := "(unknown)"
	if len(runIDs) > 0 {
		runText = strings.Join(runIDs, ", ")
	}

	sys := llm.Message{
		Role: "system",
		Content: strings.Join([]string{
			"You summarize a day's worth of chat session excerpts into a concise daily note for durable memory.",
			"Output plain Markdown only (no code fences).",
			"Keep it factual and compact. Do not invent details. Do not include secrets.",
			"Prefer bullet points grouped by: Highlights, Decisions, TODO, Risks/Questions.",
		}, "\n"),
	}
	user := llm.Message{
		Role: "user",
		Content: strings.Join([]string{
			fmt.Sprintf("Date: %s", strings.TrimSpace(dateStr)),
			fmt.Sprintf("Source run_ids: %s", runText),
			"",
			"Session excerpts (time-ordered):",
			strings.Join(lines, "\n"),
		}, "\n"),
	}
	resp, err := client.Chat(callCtx, llm.ChatRequest{
		Messages:    []llm.Message{sys, user},
		Temperature: 0.2,
		MaxTokens:   1200,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("empty llm response")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func buildDailySummaryBlock(dateStr string, marker string, generatedAt time.Time, runIDs []string, summary string) string {
	gen := generatedAt.Format(time.RFC3339)
	runs := strings.Join(runIDs, ", ")
	if strings.TrimSpace(runs) == "" {
		runs = "unknown"
	}
	var b strings.Builder
	b.WriteString("## Daily Summary (auto)\n")
	b.WriteString(marker + "\n")
	b.WriteString("- date: " + strings.TrimSpace(dateStr) + "\n")
	b.WriteString("- generated_at: " + gen + "\n")
	b.WriteString("- source_runs: " + runs + "\n\n")

	s := strings.TrimSpace(summary)
	if s == "" {
		return b.String()
	}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
