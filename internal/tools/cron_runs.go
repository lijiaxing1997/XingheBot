package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"test_skill_agent/internal/autonomy/cron"
	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
)

type CronRunListTool struct {
	ConfigPath string
}

type cronRunListArgs struct {
	ID    string `json:"id"`
	Limit int    `json:"limit"`
}

func (t *CronRunListTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "cron_run_list",
			Description: "List recent run records for a scheduled cron job.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":    map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer", "description": "Max records returned (default: 20)"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (t *CronRunListTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in cronRunListArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.ID) == "" {
		return "", errors.New("id is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	jobsPath, _, _, err := resolveCronStore(t.ConfigPath)
	if err != nil {
		return "", err
	}
	runsDir := filepath.Join(filepath.Dir(jobsPath), "runs")
	jobID := strings.TrimSpace(in.ID)
	logPath := filepath.Join(runsDir, multiagent.SanitizeID(jobID, "job")+".jsonl")
	recs, err := readCronRunLogTail(logPath, limit)
	if err != nil {
		return "", err
	}
	reverseInPlace(recs)
	return prettyJSON(map[string]any{
		"jobs_path":     jobsPath,
		"runs_dir":      runsDir,
		"run_log_path":  logPath,
		"job_id":        jobID,
		"count":         len(recs),
		"run_records":   recs,
		"latest_run_at": newestFinishedAt(recs),
	})
}

type CronRunGetTool struct {
	ConfigPath string
}

type cronRunGetArgs struct {
	ID            string `json:"id"`
	StartedAt     string `json:"started_at"`
	Offset        int    `json:"offset"`
	IncludeOutput *bool  `json:"include_output"`
	MaxBytes      int    `json:"max_output_bytes"`
}

func (t *CronRunGetTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "cron_run_get",
			Description: "Get a cron job run record (latest by default) and optionally load its saved output.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":         map[string]any{"type": "string"},
					"started_at": map[string]any{"type": "string", "description": "Optional RFC3339 time to locate a specific run."},
					"offset":     map[string]any{"type": "integer", "description": "If started_at is omitted: 0=latest, 1=previous, ... (default: 0)."},
					"include_output": map[string]any{
						"type":        "boolean",
						"description": "Whether to include full output (default: true).",
					},
					"max_output_bytes": map[string]any{
						"type":        "integer",
						"description": "Max bytes to read from output file (default: 50000).",
					},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (t *CronRunGetTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in cronRunGetArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.ID) == "" {
		return "", errors.New("id is required")
	}
	includeOutput := true
	if in.IncludeOutput != nil {
		includeOutput = *in.IncludeOutput
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 50000
	}
	if maxBytes > 500000 {
		maxBytes = 500000
	}

	jobsPath, _, _, err := resolveCronStore(t.ConfigPath)
	if err != nil {
		return "", err
	}
	runsDir := filepath.Join(filepath.Dir(jobsPath), "runs")
	jobID := strings.TrimSpace(in.ID)
	logPath := filepath.Join(runsDir, multiagent.SanitizeID(jobID, "job")+".jsonl")

	var rec cron.RunRecord
	found := false
	if strings.TrimSpace(in.StartedAt) != "" {
		target, err := parseRFC3339(strings.TrimSpace(in.StartedAt))
		if err != nil {
			return "", err
		}
		got, ok, err := findCronRunRecord(logPath, target)
		if err != nil {
			return "", err
		}
		rec = got
		found = ok
	} else {
		if in.Offset < 0 {
			in.Offset = 0
		}
		got, ok, err := getCronRunRecordByOffset(logPath, in.Offset)
		if err != nil {
			return "", err
		}
		rec = got
		found = ok
	}

	out := map[string]any{
		"jobs_path":    jobsPath,
		"runs_dir":     runsDir,
		"run_log_path": logPath,
		"job_id":       jobID,
		"found":        found,
	}
	if !found {
		return prettyJSON(out)
	}

	out["run_record"] = rec

	if includeOutput {
		outputPath, ok := resolveCronOutputPath(runsDir, rec.OutputFile)
		if ok {
			output, truncated, err := readFileUpTo(outputPath, maxBytes)
			out["output_path"] = outputPath
			out["output_truncated"] = truncated
			out["output_read_error"] = errString(err)
			if err == nil {
				out["output_source"] = "file"
				out["output"] = output
			} else {
				out["output_source"] = "preview"
				out["output"] = strings.TrimSpace(rec.OutputPreview)
			}
		} else {
			out["output_path"] = ""
			preview := strings.TrimSpace(rec.OutputPreview)
			out["output_truncated"] = strings.HasSuffix(preview, "…") || len(preview) >= 800
			out["output_read_error"] = ""
			out["output_source"] = "preview"
			out["output"] = preview
		}
	}

	return prettyJSON(out)
}

func readCronRunLogTail(path string, limit int) ([]cron.RunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	out := make([]cron.RunRecord, 0, min(limit, 16))
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*64)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec cron.RunRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		out = append(out, rec)
		if len(out) > limit {
			out = out[1:]
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return out, nil
}

func getCronRunRecordByOffset(path string, offset int) (cron.RunRecord, bool, error) {
	if offset < 0 {
		offset = 0
	}
	recs, err := readCronRunLogTail(path, offset+1)
	if err != nil {
		return cron.RunRecord{}, false, err
	}
	if len(recs) == 0 {
		return cron.RunRecord{}, false, nil
	}
	idx := len(recs) - 1 - offset
	if idx < 0 || idx >= len(recs) {
		return cron.RunRecord{}, false, nil
	}
	return recs[idx], true, nil
}

func findCronRunRecord(path string, startedAt time.Time) (cron.RunRecord, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cron.RunRecord{}, false, nil
		}
		return cron.RunRecord{}, false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*64)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec cron.RunRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.StartedAt.Equal(startedAt.UTC()) {
			return rec, true, nil
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return cron.RunRecord{}, false, err
	}
	return cron.RunRecord{}, false, nil
}

func resolveCronOutputPath(runsDir string, outputFile string) (string, bool) {
	base := strings.TrimSpace(runsDir)
	if base == "" {
		return "", false
	}
	rel := strings.TrimSpace(outputFile)
	if rel == "" {
		return "", false
	}
	rel = filepath.Clean(filepath.FromSlash(rel))
	if rel == "." || rel == string(os.PathSeparator) {
		return "", false
	}
	if filepath.IsAbs(rel) {
		return "", false
	}
	if strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", false
	}
	p := filepath.Join(base, rel)
	relCheck, err := filepath.Rel(base, p)
	if err != nil {
		return "", false
	}
	relCheck = filepath.Clean(relCheck)
	if strings.HasPrefix(relCheck, ".."+string(os.PathSeparator)) || relCheck == ".." {
		return "", false
	}
	return p, true
}

func readFileUpTo(path string, maxBytes int) (string, bool, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", false, errors.New("path is empty")
	}
	limit := maxBytes
	if limit <= 0 {
		limit = 50000
	}
	f, err := os.Open(p)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", false, err
	}
	truncated := false
	if len(data) > limit {
		data = data[:limit]
		truncated = true
	}
	return string(data), truncated, nil
}

func parseRFC3339(raw string) (time.Time, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, errors.New("time is empty")
	}
	if ts, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return ts.UTC(), nil
	}
	ts, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, err
	}
	return ts.UTC(), nil
}

func reverseInPlace[T any](items []T) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func newestFinishedAt(recs []cron.RunRecord) string {
	var newest time.Time
	for _, r := range recs {
		if r.FinishedAt.After(newest) {
			newest = r.FinishedAt
		}
	}
	if newest.IsZero() {
		return ""
	}
	return newest.UTC().Format(time.RFC3339Nano)
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
