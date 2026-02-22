package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/skills"
)

type SkillListTool struct {
	SkillsDir string
}

func (t *SkillListTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "skill_list",
			Description: "List installed skills.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *SkillListTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	list, err := skills.LoadSkills(t.SkillsDir)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "(no skills)", nil
	}
	lines := make([]string, 0, len(list))
	for _, s := range list {
		if s.Description != "" {
			lines = append(lines, fmt.Sprintf("%s - %s", s.Name, s.Description))
		} else {
			lines = append(lines, s.Name)
		}
	}
	return strings.Join(lines, "\n"), nil
}

type SkillLoadTool struct {
	SkillsDir string
}

type skillLoadArgs struct {
	Name string `json:"name"`
}

func (t *SkillLoadTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "skill_load",
			Description: "Load a skill's SKILL.md body by name.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
				},
				"required": []string{"name"},
			},
		},
	}
}

func (t *SkillLoadTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in skillLoadArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.Name == "" {
		return "", errors.New("name is required")
	}
	skill, err := skills.LoadSkillByName(t.SkillsDir, in.Name)
	if err != nil {
		return "", err
	}
	body, err := skills.LoadSkillBody(skill.SkillPath)
	if err != nil {
		return "", err
	}
	return body, nil
}

type SkillCreateTool struct {
	SkillsDir string
}

type skillCreateArgs struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (t *SkillCreateTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "skill_create",
			Description: "Create a new skill skeleton with SKILL.md.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string"},
					"description": map[string]any{"type": "string"},
				},
				"required": []string{"name", "description"},
			},
		},
	}
}

func (t *SkillCreateTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in skillCreateArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	skill, err := skills.CreateSkill(t.SkillsDir, in.Name, in.Description)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("created %s at %s", skill.Name, skill.Dir), nil
}

type SkillInstallTool struct {
	SkillsDir string
}

type skillInstallArgs struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Repo   string `json:"repo"`
	Ref    string `json:"ref"`
	Name   string `json:"name"`
}

func (t *SkillInstallTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "skill_install",
			Description: "Install a skill from local path or GitHub repo/path.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source": map[string]any{"type": "string", "description": "local or github"},
					"path":   map[string]any{"type": "string", "description": "local path or repo path"},
					"repo":   map[string]any{"type": "string", "description": "GitHub repo like owner/repo"},
					"ref":    map[string]any{"type": "string"},
					"name":   map[string]any{"type": "string"},
				},
			},
		},
	}
}

func (t *SkillInstallTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in skillInstallArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	source := strings.ToLower(strings.TrimSpace(in.Source))

	if source == "" {
		if in.Repo != "" {
			source = "github"
		} else {
			source = "local"
		}
	}

	switch source {
	case "local":
		skill, err := skills.InstallFromLocal(t.SkillsDir, in.Path, in.Name)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("installed %s at %s", skill.Name, skill.Dir), nil
	case "github":
		skill, err := skills.InstallFromGitHub(ctx, t.SkillsDir, in.Repo, in.Path, in.Ref, in.Name)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("installed %s at %s", skill.Name, skill.Dir), nil
	default:
		return "", fmt.Errorf("unknown source: %s", in.Source)
	}
}
