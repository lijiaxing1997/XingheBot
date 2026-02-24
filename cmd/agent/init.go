package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"test_skill_agent/internal/bootstrap"
)

func runInit(role string, configPath string, mcpConfigPath string, skillsDir string) error {
	report, err := bootstrap.Init(bootstrap.InitOptions{
		ConfigPath:    configPath,
		MCPConfigPath: mcpConfigPath,
		SkillsDir:     skillsDir,
		Role:          role,
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, "init complete")

	created := make(map[string]bool, 2)
	for _, p := range report.Created {
		created[strings.TrimSpace(p)] = true
	}
	skipped := make(map[string]bool, 2)
	for _, p := range report.Skipped {
		skipped[strings.TrimSpace(p)] = true
	}

	printStatus := func(label, path string) {
		path = strings.TrimSpace(path)
		status := "ok"
		switch {
		case created[path]:
			status = "created"
		case skipped[path]:
			status = "exists"
		}
		fmt.Fprintf(os.Stdout, "%s: %s (%s)\n", label, path, status)
	}

	printStatus("config", report.ConfigPath)
	printStatus("reply_style", filepath.Join(filepath.Dir(report.ConfigPath), "reply_style.md"))
	printStatus("mcp", report.MCPConfigPath)
	fmt.Fprintf(os.Stdout, "skills: %s\n", strings.TrimSpace(report.SkillsDir))

	wantSkills := []string{"skill-creator", "skill-installer", "mcp-builder", "mcp-config-manager", "ssh-deploy-slave"}
	found := make([]string, 0, len(wantSkills))
	for _, s := range wantSkills {
		if info, err := os.Stat(filepath.Join(report.SkillsDir, s)); err == nil && info.IsDir() {
			found = append(found, s)
		}
	}
	fmt.Fprintf(os.Stdout, "bundled skills: %s\n", strings.Join(found, ", "))

	if len(found) != len(wantSkills) {
		missing := make([]string, 0, len(wantSkills)-len(found))
		foundSet := make(map[string]bool, len(found))
		for _, s := range found {
			foundSet[s] = true
		}
		for _, s := range wantSkills {
			if !foundSet[s] {
				missing = append(missing, s)
			}
		}
		fmt.Fprintf(os.Stdout, "warning: missing skills after init: %s\n", strings.Join(missing, ", "))
	}

	return nil
}
