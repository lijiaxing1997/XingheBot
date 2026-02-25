package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"test_skill_agent/internal/autonomy"
	"test_skill_agent/internal/autonomy/heartbeatrunner"
	"test_skill_agent/internal/bootstrap"
	"test_skill_agent/internal/memory"
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

	memStatus := "skipped"
	memPath := ""
	if cfg, cfgErr := memory.LoadConfig(report.ConfigPath); cfgErr == nil {
		if cfg.Enabled == nil || *cfg.Enabled {
			cwd, _ := os.Getwd()
			if paths, err := memory.ResolvePaths(cfg, cwd); err == nil {
				memPath = filepath.Join(paths.RootDir, "MEMORY.md")
				_, statErr := os.Stat(memPath)
				hadTemplate := statErr == nil
				if err := memory.EnsureLayout(paths.RootDir); err == nil {
					if hadTemplate {
						memStatus = "exists"
					} else {
						memStatus = "created"
					}
				} else {
					memStatus = "error: " + err.Error()
				}
			} else {
				memStatus = "error: " + err.Error()
			}
		}
	} else if cfgErr != nil {
		memStatus = "error: " + cfgErr.Error()
	}
	if strings.TrimSpace(memPath) != "" {
		fmt.Fprintf(os.Stdout, "memory: %s (%s)\n", memPath, memStatus)
	} else {
		fmt.Fprintf(os.Stdout, "memory: (%s)\n", memStatus)
	}

	heartbeatStatus := "skipped"
	heartbeatPath := ""
	if cfg, cfgErr := autonomy.LoadConfig(report.ConfigPath); cfgErr == nil {
		cwd, _ := os.Getwd()
		if p, err := heartbeatrunner.ResolveHeartbeatFilePath(cfg.Heartbeat.Path, cwd); err == nil {
			heartbeatPath = p
			if _, err := os.Stat(heartbeatPath); err == nil {
				heartbeatStatus = "exists"
			} else if err != nil && os.IsNotExist(err) {
				if err := heartbeatrunner.WriteHeartbeatFileAtomic(heartbeatPath, defaultHeartbeatTemplate); err != nil {
					heartbeatStatus = "error: " + err.Error()
				} else {
					heartbeatStatus = "created"
				}
			} else if err != nil {
				heartbeatStatus = "error: " + err.Error()
			}
		} else if err != nil {
			heartbeatStatus = "error: " + err.Error()
		}
	} else if cfgErr != nil {
		heartbeatStatus = "error: " + cfgErr.Error()
	}
	if strings.TrimSpace(heartbeatPath) != "" {
		fmt.Fprintf(os.Stdout, "heartbeat: %s (%s)\n", heartbeatPath, heartbeatStatus)
	} else {
		fmt.Fprintf(os.Stdout, "heartbeat: (%s)\n", heartbeatStatus)
	}

	wantSkills := []string{
		"xinghebot-dev-manual",
		"skill-creator",
		"skill-installer",
		"mcp-builder",
		"mcp-config-manager",
		"ssh-deploy-slave",
		"slave-file-manager",
	}
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

const defaultHeartbeatTemplate = `# HEARTBEAT.md

- [ ]
`
