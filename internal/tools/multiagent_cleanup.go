package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
)

type AgentRunPruneTool struct {
	Coordinator *multiagent.Coordinator
}

type agentRunPruneArgs struct {
	Mode                string `json:"mode"`
	ArchiveDir          string `json:"archive_dir"`
	KeepLast            *int   `json:"keep_last"`
	ArchiveAfterMinutes int    `json:"archive_after_minutes"`
	OlderThanDays       int    `json:"older_than_days"`
	OlderThanHours      int    `json:"older_than_hours"`
	OlderThanMinutes    int    `json:"older_than_minutes"`
	IncludeFailed       bool   `json:"include_failed"`
	DryRun              *bool  `json:"dry_run"`
}

func (t *AgentRunPruneTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name: "agent_run_prune",
			Description: "Prune (archive or delete) completed multi-agent runs under the coordinator root. " +
				"By default, skips active runs and keeps failed runs for debugging. " +
				"Set dry_run=false to apply changes.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode": map[string]any{
						"type": "string",
						"enum": []string{"archive", "delete"},
					},
					"archive_dir":           map[string]any{"type": "string"},
					"keep_last":             map[string]any{"type": "integer"},
					"archive_after_minutes": map[string]any{"type": "integer"},
					"older_than_days":       map[string]any{"type": "integer"},
					"older_than_hours":      map[string]any{"type": "integer"},
					"older_than_minutes":    map[string]any{"type": "integer"},
					"include_failed":        map[string]any{"type": "boolean"},
					"dry_run":               map[string]any{"type": "boolean"},
				},
			},
		},
	}
}

func (t *AgentRunPruneTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if t.Coordinator == nil {
		return "", errors.New("multi-agent coordinator is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in agentRunPruneArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}

	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = "archive"
	}
	if mode != "archive" && mode != "delete" {
		return "", fmt.Errorf("invalid mode: %s", mode)
	}

	dryRun := true
	if in.DryRun != nil {
		dryRun = *in.DryRun
	}

	keepLast := 20
	if in.KeepLast != nil {
		keepLast = *in.KeepLast
	}
	if keepLast < 0 {
		keepLast = 0
	}

	archiveAfterMinutes := in.ArchiveAfterMinutes
	if archiveAfterMinutes < 0 {
		archiveAfterMinutes = 0
	}
	if archiveAfterMinutes == 0 {
		olderThanDays := in.OlderThanDays
		if olderThanDays < 0 {
			olderThanDays = 0
		}
		olderThanHours := in.OlderThanHours
		if olderThanHours < 0 {
			olderThanHours = 0
		}
		olderThanMinutes := in.OlderThanMinutes
		if olderThanMinutes < 0 {
			olderThanMinutes = 0
		}
		archiveAfterMinutes = olderThanMinutes + (olderThanHours * 60) + (olderThanDays * 24 * 60)
	}
	archiveAfter := time.Duration(archiveAfterMinutes) * time.Minute

	archiveDir := strings.TrimSpace(in.ArchiveDir)
	if mode == "archive" && archiveDir == "" {
		archiveDir = multiagent.DefaultRunArchiveDir(t.Coordinator.RunRoot)
	}

	report, err := multiagent.PruneRuns(ctx, t.Coordinator, multiagent.RunPruneOptions{
		Mode:          multiagent.RunPruneMode(mode),
		ArchiveDir:    archiveDir,
		KeepLast:      keepLast,
		ArchiveAfter:  archiveAfter,
		IncludeFailed: in.IncludeFailed,
		DryRun:        dryRun,
	})
	if err != nil {
		return "", err
	}

	note := ""
	switch string(report.Mode) {
	case "archive":
		if dryRun {
			note = "dry-run: set dry_run=false to move run dirs into archive_dir"
		} else {
			note = "archived run dirs into archive_dir"
		}
	case "delete":
		if dryRun {
			note = "dry-run: set dry_run=false to delete run dirs"
		} else {
			note = "deleted run dirs"
		}
	}

	olderThanMinutes := 0
	if archiveAfterMinutes > 0 {
		olderThanMinutes = archiveAfterMinutes
	}

	return prettyJSON(map[string]any{
		"status":                "ok",
		"mode":                  string(report.Mode),
		"dry_run":               dryRun,
		"include_failed":        report.IncludeFailed,
		"keep_last":             report.KeepLast,
		"archive_after_minutes": olderThanMinutes,
		"archive_after":         report.ArchiveAfter,
		"cutoff":                report.Cutoff,
		"coordinator_root":      report.CoordinatorRoot,
		"archive_dir":           report.ArchiveDir,
		"found_runs":            report.FoundRuns,
		"eligible_runs":         report.EligibleRuns,
		"prune_candidates":      report.PruneCandidates,
		"applied":               report.Applied,
		"kept":                  report.Kept,
		"skipped_active":        report.SkippedActive,
		"skipped_failed":        report.SkippedFailed,
		"actions":               report.Actions,
		"errors":                report.Errors,
		"text": fmt.Sprintf(
			"%s: applied=%d candidates=%d kept=%d skipped_active=%d skipped_failed=%d",
			note,
			report.Applied,
			report.PruneCandidates,
			len(report.Kept),
			len(report.SkippedActive),
			len(report.SkippedFailed),
		),
		"checked_at": report.CheckedAt,
	})
}
