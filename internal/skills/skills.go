package skills

import (
    "bufio"
    "context"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "test_skill_agent/internal/util"
)

type Skill struct {
    Name        string
    Description string
    Dir         string
    SkillPath   string
}

func LoadSkills(dir string) ([]Skill, error) {
    entries, err := os.ReadDir(dir)
    if err != nil {
        if os.IsNotExist(err) {
            return []Skill{}, nil
        }
        return nil, err
    }
    skills := make([]Skill, 0, len(entries))
    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }
        skillDir := filepath.Join(dir, entry.Name())
        skillPath := filepath.Join(skillDir, "SKILL.md")
        data, err := os.ReadFile(skillPath)
        if err != nil {
            continue
        }
        meta, _, _ := ParseFrontmatter(string(data))
        name := strings.TrimSpace(meta["name"])
        if name == "" {
            name = entry.Name()
        }
        desc := strings.TrimSpace(meta["description"])
        skills = append(skills, Skill{
            Name:        name,
            Description: desc,
            Dir:         skillDir,
            SkillPath:   skillPath,
        })
    }
    return skills, nil
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
    if name == "" {
        return nil, errors.New("name is required")
    }
    if description == "" {
        return nil, errors.New("description is required")
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

func ParseFrontmatter(content string) (map[string]string, string, error) {
    scanner := bufio.NewScanner(strings.NewReader(content))
    if !scanner.Scan() {
        return map[string]string{}, "", nil
    }
    first := strings.TrimSpace(scanner.Text())
    if first != "---" {
        return map[string]string{}, content, nil
    }
    metaLines := make([]string, 0, 16)
    for scanner.Scan() {
        line := scanner.Text()
        if strings.TrimSpace(line) == "---" {
            break
        }
        metaLines = append(metaLines, line)
    }
    meta := make(map[string]string)
    for _, line := range metaLines {
        line = strings.TrimSpace(line)
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }
        parts := strings.SplitN(line, ":", 2)
        if len(parts) != 2 {
            continue
        }
        key := strings.TrimSpace(parts[0])
        val := strings.TrimSpace(parts[1])
        meta[key] = val
    }

    bodyLines := make([]string, 0, 128)
    for scanner.Scan() {
        bodyLines = append(bodyLines, scanner.Text())
    }
    return meta, strings.Join(bodyLines, "\n"), nil
}

func NormalizeSkillName(name string) string {
    name = strings.ToLower(strings.TrimSpace(name))
    name = strings.ReplaceAll(name, " ", "-")
    var b strings.Builder
    for _, r := range name {
        if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
            b.WriteRune(r)
        }
    }
    if b.Len() == 0 {
        return "skill"
    }
    return b.String()
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
