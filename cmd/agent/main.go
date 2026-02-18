package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"test_skill_agent/internal/agent"
	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/mcpclient"
	"test_skill_agent/internal/skills"
	"test_skill_agent/internal/tools"
)

func main() {
	if len(os.Args) < 2 {
		if err := runChat(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	switch os.Args[1] {
	case "chat":
		if err := runChat(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "skills":
		if err := runSkills(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		if err := runChat(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
}

func runChat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	skillsDir := fs.String("skills-dir", defaultSkillsDir(), "skills directory")
	temperature := fs.Float64("temperature", 0.2, "LLM temperature")
	configPath := fs.String("config", "config.json", "path to config.json")
	mcpConfigPath := fs.String("mcp-config", "", "path to MCP config (default: same as --config)")
	fs.Parse(args)

	client, err := llm.NewClientFromConfig(*configPath)
	if err != nil {
		return err
	}

	registry := tools.NewRegistry()
	registry.Register(&tools.ListFilesTool{})
	registry.Register(&tools.ReadFileTool{})
	registry.Register(&tools.WriteFileTool{})
	registry.Register(&tools.EditFileTool{})
	registry.Register(&tools.MoveFileTool{})
	registry.Register(&tools.CopyFileTool{})
	registry.Register(&tools.DeleteFileTool{})
	registry.Register(&tools.ExecCommandTool{})

	registry.Register(&tools.SkillListTool{SkillsDir: *skillsDir})
	registry.Register(&tools.SkillLoadTool{SkillsDir: *skillsDir})
	registry.Register(&tools.SkillCreateTool{SkillsDir: *skillsDir})
	registry.Register(&tools.SkillInstallTool{SkillsDir: *skillsDir})

	cfgPath := *configPath
	if strings.TrimSpace(*mcpConfigPath) != "" {
		cfgPath = *mcpConfigPath
	}
	mcpCfg, err := mcpclient.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	mcpServers, err := mcpclient.ConnectServers(context.Background(), mcpCfg.Servers)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	defer func() {
		if err := mcpclient.CloseServers(mcpServers); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}()
	mcpTools, err := mcpclient.ToolsFromServers(mcpServers)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	for _, tool := range mcpTools {
		registry.Register(tool)
	}

	ag, err := agent.New(client, registry, *skillsDir)
	if err != nil {
		return err
	}
	ag.Temperature = float32(*temperature)

	fmt.Println("Agent ready. Type /exit to quit.")
	return ag.RunInteractive(context.Background(), os.Stdin, os.Stdout)
}

func runSkills(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: skills <list|create|install>")
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("skills list", flag.ExitOnError)
		dir := fs.String("dir", defaultSkillsDir(), "skills directory")
		fs.Parse(args[1:])
		list, err := skills.LoadSkills(*dir)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("(no skills)")
			return nil
		}
		for _, s := range list {
			if s.Description != "" {
				fmt.Printf("%s - %s\n", s.Name, s.Description)
			} else {
				fmt.Println(s.Name)
			}
		}
		return nil
	case "create":
		fs := flag.NewFlagSet("skills create", flag.ExitOnError)
		dir := fs.String("dir", defaultSkillsDir(), "skills directory")
		name := fs.String("name", "", "skill name")
		desc := fs.String("description", "", "skill description")
		fs.Parse(args[1:])
		skill, err := skills.CreateSkill(*dir, *name, *desc)
		if err != nil {
			return err
		}
		fmt.Printf("created %s at %s\n", skill.Name, skill.Dir)
		return nil
	case "install":
		fs := flag.NewFlagSet("skills install", flag.ExitOnError)
		dir := fs.String("dir", defaultSkillsDir(), "skills directory")
		localPath := fs.String("local", "", "local skill directory to copy")
		repo := fs.String("repo", "", "github repo (owner/repo)")
		path := fs.String("path", "", "path within repo")
		ref := fs.String("ref", "main", "git ref")
		name := fs.String("name", "", "override skill folder name")
		fs.Parse(args[1:])

		ctx := context.Background()
		if *localPath != "" {
			skill, err := skills.InstallFromLocal(*dir, *localPath, *name)
			if err != nil {
				return err
			}
			fmt.Printf("installed %s at %s\n", skill.Name, skill.Dir)
			return nil
		}
		if *repo == "" {
			return fmt.Errorf("--repo is required when not using --local")
		}
		skill, err := skills.InstallFromGitHub(ctx, *dir, *repo, *path, *ref, *name)
		if err != nil {
			return err
		}
		fmt.Printf("installed %s at %s\n", skill.Name, skill.Dir)
		return nil
	default:
		return fmt.Errorf("unknown skills subcommand: %s", args[0])
	}
}

func defaultSkillsDir() string {
	if dir := strings.TrimSpace(os.Getenv("SKILLS_DIR")); dir != "" {
		return dir
	}
	return "skills"
}
