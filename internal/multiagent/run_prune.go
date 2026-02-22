package multiagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type RunPruneMode string

const (
	RunPruneArchive RunPruneMode = "archive"
	RunPruneDelete  RunPruneMode = "delete"
)

type RunPruneOptions struct {
	Mode          RunPruneMode
	ArchiveDir    string
	KeepLast      int
	ArchiveAfter  time.Duration
	IncludeFailed bool
	DryRun        bool
	Now           time.Time
}

type RunPruneRunView struct {
	RunID     string    `json:"run_id"`
	CreatedAt time.Time `json:"created_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	RunDir    string    `json:"run_dir"`
	Agents    int       `json:"agents"`
	HasFailed bool      `json:"has_failed"`
}

type RunPruneAction struct {
	RunID     string `json:"run_id"`
	FromDir   string `json:"from_dir"`
	ToDir     string `json:"to_dir,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

type RunPruneReport struct {
	Mode          RunPruneMode `json:"mode"`
	DryRun        bool         `json:"dry_run"`
	IncludeFailed bool         `json:"include_failed"`
	KeepLast      int          `json:"keep_last"`
	ArchiveAfter  string       `json:"archive_after"`
	Cutoff        time.Time    `json:"cutoff,omitempty"`

	CoordinatorRoot string `json:"coordinator_root"`
	ArchiveDir      string `json:"archive_dir,omitempty"`

	FoundRuns       int `json:"found_runs"`
	EligibleRuns    int `json:"eligible_runs"`
	PruneCandidates int `json:"prune_candidates"`
	Applied         int `json:"applied"`

	Kept          []RunPruneRunView `json:"kept"`
	SkippedActive []RunPruneRunView `json:"skipped_active"`
	SkippedFailed []RunPruneRunView `json:"skipped_failed"`

	Actions []RunPruneAction `json:"actions"`
	Errors  []RunPruneAction `json:"errors"`

	CheckedAt time.Time `json:"checked_at"`
}

func PruneRuns(ctx context.Context, coord *Coordinator, opts RunPruneOptions) (RunPruneReport, error) {
	if coord == nil {
		return RunPruneReport{}, errors.New("coordinator is nil")
	}
	if err := ctx.Err(); err != nil {
		return RunPruneReport{}, err
	}

	mode := opts.Mode
	if mode == "" {
		mode = RunPruneArchive
	}
	if mode != RunPruneArchive && mode != RunPruneDelete {
		return RunPruneReport{}, fmt.Errorf("invalid prune mode: %s", mode)
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	keepLast := opts.KeepLast
	if keepLast < 0 {
		keepLast = 0
	}
	archiveAfter := opts.ArchiveAfter
	if archiveAfter < 0 {
		archiveAfter = 0
	}

	archiveDir := strings.TrimSpace(opts.ArchiveDir)
	if mode == RunPruneArchive && archiveDir == "" {
		archiveDir = DefaultRunArchiveDir(coord.RunRoot)
	}
	if mode == RunPruneArchive && strings.TrimSpace(archiveDir) == "" {
		return RunPruneReport{}, errors.New("archive_dir is required for archive mode")
	}
	if mode == RunPruneArchive {
		if err := ValidateArchiveDir(coord.RunRoot, archiveDir); err != nil {
			return RunPruneReport{}, err
		}
	}

	var cutoff time.Time
	if archiveAfter > 0 {
		cutoff = now.Add(-archiveAfter)
	}

	runs, err := coord.ListRuns()
	if err != nil {
		return RunPruneReport{}, err
	}

	activeRuns := make([]RunPruneRunView, 0, 8)
	failedRuns := make([]RunPruneRunView, 0, 8)
	eligible := make([]RunPruneRunView, 0, len(runs))

	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return RunPruneReport{}, err
		}

		states, err := coord.ListAgentStates(run.ID)
		if err != nil {
			continue
		}

		hasActive := false
		hasFailed := false
		var endedAt time.Time
		for _, st := range states {
			status := strings.ToLower(strings.TrimSpace(st.Status))
			if !IsTerminalStatus(status) {
				hasActive = true
			}
			if status == StatusFailed {
				hasFailed = true
			}
			if !st.FinishedAt.IsZero() && st.FinishedAt.After(endedAt) {
				endedAt = st.FinishedAt
			}
		}
		if endedAt.IsZero() && !hasActive {
			// Best-effort fallback: derive a completion-ish time from UpdatedAt.
			for _, st := range states {
				if st.UpdatedAt.After(endedAt) {
					endedAt = st.UpdatedAt
				}
			}
		}

		view := RunPruneRunView{
			RunID:     run.ID,
			CreatedAt: run.CreatedAt,
			EndedAt:   endedAt,
			RunDir:    coord.RunDir(run.ID),
			Agents:    len(states),
			HasFailed: hasFailed,
		}

		if hasActive {
			activeRuns = append(activeRuns, view)
			continue
		}
		if hasFailed && !opts.IncludeFailed {
			failedRuns = append(failedRuns, view)
			continue
		}
		eligible = append(eligible, view)
	}

	sort.Slice(eligible, func(i, j int) bool {
		a := eligible[i]
		b := eligible[j]
		ai := a.EndedAt
		bi := b.EndedAt
		if ai.IsZero() {
			ai = a.CreatedAt
		}
		if bi.IsZero() {
			bi = b.CreatedAt
		}
		if ai.Equal(bi) {
			return a.RunID < b.RunID
		}
		if ai.IsZero() {
			return false
		}
		if bi.IsZero() {
			return true
		}
		return ai.After(bi)
	})

	kept := make([]RunPruneRunView, 0, min(keepLast, len(eligible)))
	pruneCandidates := make([]RunPruneRunView, 0, maxInt(0, len(eligible)-keepLast))
	for idx, run := range eligible {
		if keepLast > 0 && idx < keepLast {
			kept = append(kept, run)
			continue
		}
		if !cutoff.IsZero() {
			when := run.EndedAt
			if when.IsZero() {
				when = run.CreatedAt
			}
			if when.IsZero() || !when.Before(cutoff) {
				continue
			}
		}
		pruneCandidates = append(pruneCandidates, run)
	}

	actions := make([]RunPruneAction, 0, len(pruneCandidates))
	applied := 0
	errorsOut := make([]RunPruneAction, 0, 4)

	if mode == RunPruneArchive && !opts.DryRun {
		if err := os.MkdirAll(archiveDir, 0o755); err != nil {
			return RunPruneReport{}, err
		}
	}

	for _, run := range pruneCandidates {
		if err := ctx.Err(); err != nil {
			return RunPruneReport{}, err
		}
		act := RunPruneAction{
			RunID:     run.RunID,
			FromDir:   run.RunDir,
			CreatedAt: formatRFC3339(run.CreatedAt),
			EndedAt:   formatRFC3339(run.EndedAt),
		}
		if opts.DryRun {
			if mode == RunPruneArchive {
				act.ToDir = filepath.Join(archiveDir, run.RunID)
			}
			actions = append(actions, act)
			continue
		}

		switch mode {
		case RunPruneDelete:
			if err := os.RemoveAll(run.RunDir); err != nil {
				act.Error = err.Error()
				errorsOut = append(errorsOut, act)
				continue
			}
			applied++
			actions = append(actions, act)
		case RunPruneArchive:
			dst, err := uniqueArchivePath(archiveDir, run.RunID, run.EndedAt, run.CreatedAt)
			if err != nil {
				act.Error = err.Error()
				errorsOut = append(errorsOut, act)
				continue
			}
			if err := os.Rename(run.RunDir, dst); err != nil {
				act.Error = err.Error()
				errorsOut = append(errorsOut, act)
				continue
			}
			act.ToDir = dst
			applied++
			actions = append(actions, act)
		}
	}

	return RunPruneReport{
		Mode:            mode,
		DryRun:          opts.DryRun,
		IncludeFailed:   opts.IncludeFailed,
		KeepLast:        keepLast,
		ArchiveAfter:    archiveAfter.String(),
		Cutoff:          cutoff,
		CoordinatorRoot: coord.RunRoot,
		ArchiveDir:      archiveDir,
		FoundRuns:       len(runs),
		EligibleRuns:    len(eligible),
		PruneCandidates: len(pruneCandidates),
		Applied:         applied,
		Kept:            kept,
		SkippedActive:   activeRuns,
		SkippedFailed:   failedRuns,
		Actions:         actions,
		Errors:          errorsOut,
		CheckedAt:       now,
	}, nil
}

func uniqueArchivePath(archiveDir string, runID string, endedAt time.Time, createdAt time.Time) (string, error) {
	base := filepath.Join(archiveDir, runID)
	if _, err := os.Stat(base); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return base, nil
		}
		return "", err
	}

	stamp := "unknown"
	best := endedAt
	if best.IsZero() {
		best = createdAt
	}
	if !best.IsZero() {
		stamp = best.UTC().Format("20060102-150405")
	}
	for attempt := 0; attempt < 50; attempt++ {
		suffix := fmt.Sprintf(".%s.%d", stamp, attempt+1)
		dst := base + suffix
		if _, err := os.Stat(dst); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return dst, nil
			}
			return "", err
		}
	}
	return "", fmt.Errorf("unable to find unique archive path for run_id=%s", runID)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func formatRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
