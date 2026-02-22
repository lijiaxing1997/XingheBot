package skills

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"test_skill_agent/internal/util"
)

type Skill struct {
	Name        string
	Description string
	Dir         string
	SkillPath   string
}

type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type LoadOptions struct {
	MaxCandidates    int
	MaxSkillFileSize int64
}

const (
	defaultMaxCandidates    = 500
	defaultMaxSkillFileSize = 256 * 1024 // 256 KiB
)

func LoadSkills(dir string) ([]Skill, error) {
	return LoadSkillsWithOptions(dir, LoadOptions{})
}

func LoadSkillByName(dir, name string) (*Skill, error) {
	skills, err := LoadSkills(dir)
	if err != nil {
		return nil, err
	}
	for _, skill := range skills {
		if strings.EqualFold(skill.Name, name) || strings.EqualFold(filepath.Base(skill.Dir), name) {
			return &skill, nil
		}
	}
	return nil, fmt.Errorf("skill not found: %s", name)
}

func LoadSkillBody(skillPath string) (string, error) {
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return "", err
	}
	_, body, err := ParseFrontmatter(string(data))
	if err != nil {
		return "", err
	}
	return body, nil
}

func CreateSkill(dir, name, description string) (*Skill, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return nil, errors.New("name is required")
	}
	if description == "" {
		return nil, errors.New("description is required")
	}
	dir = resolveSkillsCreateRoot(dir)
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("skills directory is required")
	}
	folder := NormalizeSkillName(name)
	skillDir := filepath.Join(dir, folder)
	if _, err := os.Stat(skillDir); err == nil {
		return nil, errors.New("skill directory already exists")
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return nil, err
	}

	title := titleCase(strings.ReplaceAll(strings.ReplaceAll(name, "-", " "), "_", " "))
	if title == "" {
		title = "Skill"
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s\n\nDescribe the skill workflow here.\n", name, description, title)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return &Skill{
		Name:        name,
		Description: description,
		Dir:         skillDir,
		SkillPath:   skillPath,
	}, nil
}

func InstallFromLocal(destDir, srcPath, nameOverride string) (*Skill, error) {
	if srcPath == "" {
		return nil, errors.New("source path is required")
	}
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return nil, err
	}
	if !srcInfo.IsDir() {
		return nil, errors.New("source path must be a directory")
	}
	name := nameOverride
	if name == "" {
		name = filepath.Base(srcPath)
	}
	skillDir := filepath.Join(destDir, name)
	if _, err := os.Stat(skillDir); err == nil {
		return nil, errors.New("destination skill already exists")
	}
	if err := util.CopyDir(srcPath, skillDir, false); err != nil {
		return nil, err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	return &Skill{
		Name:      name,
		Dir:       skillDir,
		SkillPath: skillPath,
	}, nil
}

func InstallFromGitHub(ctx context.Context, destDir, repo, path, ref, nameOverride string) (*Skill, error) {
	if repo == "" {
		return nil, errors.New("repo is required")
	}
	if ref == "" {
		ref = "main"
	}
	name := nameOverride
	if name == "" {
		if path != "" {
			name = filepath.Base(path)
		} else {
			parts := strings.Split(repo, "/")
			name = parts[len(parts)-1]
		}
	}
	skillDir := filepath.Join(destDir, name)
	if _, err := os.Stat(skillDir); err == nil {
		return nil, errors.New("destination skill already exists")
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return nil, err
	}
	if err := DownloadGitHubDir(ctx, repo, path, ref, skillDir); err != nil {
		return nil, err
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	return &Skill{
		Name:      name,
		Dir:       skillDir,
		SkillPath: skillPath,
	}, nil
}

func ParseFrontmatter(content string) (Frontmatter, string, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 1024*64), 1024*1024)

	if !scanner.Scan() {
		return Frontmatter{}, "", nil
	}
	first := strings.TrimSpace(scanner.Text())
	if first != "---" {
		return Frontmatter{}, content, nil
	}

	metaLines := make([]string, 0, 32)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		metaLines = append(metaLines, line)
	}

	bodyLines := make([]string, 0, 256)
	for scanner.Scan() {
		bodyLines = append(bodyLines, scanner.Text())
	}
	body := strings.Join(bodyLines, "\n")

	rawMeta := strings.Join(metaLines, "\n")
	fm := Frontmatter{}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(rawMeta), &parsed); err == nil {
		if v, ok := parsed["name"]; ok {
			if s, ok := v.(string); ok {
				fm.Name = strings.TrimSpace(s)
			}
		}
		if v, ok := parsed["description"]; ok {
			if s, ok := v.(string); ok {
				fm.Description = strings.TrimSpace(s)
			}
		}
		return fm, body, nil
	}

	// Back-compat: tolerate non-YAML "key: value" pairs.
	for _, line := range metaLines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])
		switch key {
		case "name":
			if fm.Name == "" {
				fm.Name = val
			}
		case "description":
			if fm.Description == "" {
				fm.Description = val
			}
		}
	}
	return fm, body, nil
}

func NormalizeSkillName(name string) string {
	original := strings.TrimSpace(name)
	name = strings.ToLower(original)
	name = strings.ReplaceAll(name, " ", "-")
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "skill-" + shortHash(original)
	}
	if out == "skill" && strings.ToLower(original) != "skill" {
		return "skill-" + shortHash(original)
	}
	const maxLen = 64
	if len(out) > maxLen {
		out = out[:maxLen]
		out = strings.TrimRight(out, "-_")
	}
	if out == "" {
		return "skill-" + shortHash(original)
	}
	return out
}

func titleCase(s string) string {
	parts := strings.Fields(s)
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	sum := fmt.Sprintf("%08x", h.Sum32())
	if len(sum) > 6 {
		sum = sum[:6]
	}
	return sum
}

func resolveSkillsCreateRoot(dir string) string {
	root := strings.TrimSpace(dir)
	if root == "" {
		return ""
	}
	root = filepath.Clean(root)

	// If the provided dir already looks like a skills root, use it.
	if strings.EqualFold(filepath.Base(root), "skills") {
		return root
	}

	// If it contains a `skills/` directory, treat that as the real root.
	nested := filepath.Join(root, "skills")
	if info, err := os.Stat(nested); err == nil && info.IsDir() {
		return nested
	}
	return root
}

func LoadSkillsWithOptions(dir string, opts LoadOptions) ([]Skill, error) {
	root := resolveNestedSkillsRoot(dir, opts)
	if root == "" {
		root = dir
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []Skill{}, nil
		}
		return nil, err
	}

	maxCandidates := opts.MaxCandidates
	if maxCandidates <= 0 {
		maxCandidates = defaultMaxCandidates
	}
	maxSize := opts.MaxSkillFileSize
	if maxSize <= 0 {
		maxSize = defaultMaxSkillFileSize
	}

	skillsList := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if len(skillsList) >= maxCandidates {
			break
		}
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		if name == "node_modules" {
			continue
		}

		skillDir := filepath.Join(root, entry.Name())
		skillPath := filepath.Join(skillDir, "SKILL.md")
		info, err := os.Stat(skillPath)
		if err != nil || info.IsDir() {
			continue
		}
		if maxSize > 0 && info.Size() > maxSize {
			continue
		}
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		meta, _, _ := ParseFrontmatter(string(data))

		displayName := strings.TrimSpace(meta.Name)
		if displayName == "" {
			displayName = entry.Name()
		}
		desc := strings.TrimSpace(meta.Description)
		skillsList = append(skillsList, Skill{
			Name:        displayName,
			Description: desc,
			Dir:         skillDir,
			SkillPath:   skillPath,
		})
	}

	sort.Slice(skillsList, func(i, j int) bool {
		return strings.ToLower(skillsList[i].Name) < strings.ToLower(skillsList[j].Name)
	})
	return skillsList, nil
}

func resolveNestedSkillsRoot(dir string, opts LoadOptions) string {
	root := strings.TrimSpace(dir)
	if root == "" {
		return root
	}
	nested := filepath.Join(root, "skills")
	info, err := os.Stat(nested)
	if err != nil || !info.IsDir() {
		return root
	}

	maxCandidates := opts.MaxCandidates
	if maxCandidates <= 0 {
		maxCandidates = defaultMaxCandidates
	}

	entries, err := os.ReadDir(nested)
	if err != nil {
		return root
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Name() == "node_modules" {
			continue
		}
		skillMd := filepath.Join(nested, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillMd); err == nil {
			return nested
		}
		maxCandidates--
		if maxCandidates <= 0 {
			break
		}
	}
	return root
}

func FormatSkillsForPrompt(skillsList []Skill) string {
	if len(skillsList) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_skills>\n")
	for _, s := range skillsList {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		b.WriteString("  <skill>\n")
		b.WriteString("    <name>")
		b.WriteString(escapePromptXML(name))
		b.WriteString("</name>\n")
		if desc := strings.TrimSpace(s.Description); desc != "" {
			b.WriteString("    <description>")
			b.WriteString(escapePromptXML(desc))
			b.WriteString("</description>\n")
		}
		if loc := strings.TrimSpace(s.SkillPath); loc != "" {
			b.WriteString("    <location>")
			b.WriteString(escapePromptXML(loc))
			b.WriteString("</location>\n")
		}
		b.WriteString("  </skill>\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

func escapePromptXML(s string) string {
	out := strings.ReplaceAll(s, "&", "&amp;")
	out = strings.ReplaceAll(out, "<", "&lt;")
	out = strings.ReplaceAll(out, ">", "&gt;")
	return out
}
