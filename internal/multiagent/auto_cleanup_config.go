package multiagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type AutoCleanupConfig struct {
	Enabled             *bool  `json:"enabled"`
	IntervalMinutes     *int   `json:"interval_minutes"`
	Mode                string `json:"mode"`
	ArchiveDir          string `json:"archive_dir"`
	KeepLast            *int   `json:"keep_last"`
	ArchiveAfterMinutes *int   `json:"archive_after_minutes"`
	IncludeFailed       *bool  `json:"include_failed"`
	DryRun              *bool  `json:"dry_run"`
}

type ResolvedAutoCleanupConfig struct {
	Enabled       bool
	Interval      time.Duration
	Mode          string
	ArchiveDir    string
	KeepLast      int
	ArchiveAfter  time.Duration
	IncludeFailed bool
	DryRun        bool
}

type rootCleanupConfig struct {
	MultiAgent struct {
		Cleanup AutoCleanupConfig `json:"cleanup"`
	} `json:"multi_agent"`
}

func LoadAutoCleanupConfig(configPath string) (AutoCleanupConfig, error) {
	path := strings.TrimSpace(configPath)
	if path == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return AutoCleanupConfig{}, err
	}
	var cfg rootCleanupConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return AutoCleanupConfig{}, fmt.Errorf("parse config.json: %w", err)
	}
	return cfg.MultiAgent.Cleanup, nil
}

func ResolveAutoCleanupConfig(runRoot string, raw AutoCleanupConfig) (ResolvedAutoCleanupConfig, error) {
	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}

	intervalMinutes := 10
	if raw.IntervalMinutes != nil {
		intervalMinutes = *raw.IntervalMinutes
	}
	if intervalMinutes <= 0 {
		intervalMinutes = 10
	}
	if intervalMinutes > 24*60 {
		intervalMinutes = 24 * 60
	}

	mode := strings.ToLower(strings.TrimSpace(raw.Mode))
	if mode == "" {
		mode = "archive"
	}
	if mode != "archive" && mode != "delete" {
		return ResolvedAutoCleanupConfig{}, fmt.Errorf("invalid cleanup mode: %s", mode)
	}

	keepLast := 20
	if raw.KeepLast != nil {
		keepLast = *raw.KeepLast
	}
	if keepLast < 0 {
		keepLast = 0
	}

	archiveAfterMinutes := 60
	if raw.ArchiveAfterMinutes != nil {
		archiveAfterMinutes = *raw.ArchiveAfterMinutes
	}
	if archiveAfterMinutes < 0 {
		archiveAfterMinutes = 0
	}
	archiveAfter := time.Duration(archiveAfterMinutes) * time.Minute

	includeFailed := false
	if raw.IncludeFailed != nil {
		includeFailed = *raw.IncludeFailed
	}

	dryRun := false
	if raw.DryRun != nil {
		dryRun = *raw.DryRun
	}

	archiveDir := strings.TrimSpace(raw.ArchiveDir)
	if mode == "archive" && archiveDir == "" {
		archiveDir = DefaultRunArchiveDir(runRoot)
	}

	if mode == "archive" && archiveDir == "" {
		return ResolvedAutoCleanupConfig{}, errors.New("archive_dir is required for archive mode")
	}
	if mode == "archive" {
		if err := ValidateArchiveDir(runRoot, archiveDir); err != nil {
			return ResolvedAutoCleanupConfig{}, err
		}
	}

	return ResolvedAutoCleanupConfig{
		Enabled:       enabled,
		Interval:      time.Duration(intervalMinutes) * time.Minute,
		Mode:          mode,
		ArchiveDir:    archiveDir,
		KeepLast:      keepLast,
		ArchiveAfter:  archiveAfter,
		IncludeFailed: includeFailed,
		DryRun:        dryRun,
	}, nil
}

func DefaultRunArchiveDir(runRoot string) string {
	root := filepath.Clean(strings.TrimSpace(runRoot))
	if root == "" {
		root = filepath.Clean(".multi_agent/runs")
	}
	return filepath.Join(filepath.Dir(root), "archive")
}

func ValidateArchiveDir(runRoot string, archiveDir string) error {
	root := filepath.Clean(strings.TrimSpace(runRoot))
	arc := filepath.Clean(strings.TrimSpace(archiveDir))
	if root == "" || arc == "" {
		return nil
	}
	if root == arc {
		return errors.New("archive_dir must not equal run_root")
	}
	rel, err := filepath.Rel(root, arc)
	if err == nil {
		rel = filepath.Clean(rel)
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..") {
			// archive is inside run root -> risk of self-recursion / hiding files
			return fmt.Errorf("archive_dir must not be inside run_root: %s", arc)
		}
	}
	return nil
}
