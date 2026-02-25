package cron

import (
	"path/filepath"
	"strings"

	"test_skill_agent/internal/memory"
)

func ResolveDefaultJobsPath(configPath string, workDir string) (string, error) {
	memCfg, err := memory.LoadConfig(configPath)
	if err != nil {
		return "", err
	}
	paths, err := memory.ResolvePaths(memCfg, workDir)
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(paths.RootDir)
	if root == "" {
		return "", nil
	}
	projectDir := filepath.Dir(root)
	return filepath.Join(projectDir, "scheduler", "cron", "jobs.json"), nil
}
