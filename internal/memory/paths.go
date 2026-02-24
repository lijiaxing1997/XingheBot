package memory

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type ResolvedPaths struct {
	RootDir    string
	ProjectKey string
}

func ResolvePaths(cfg Config, workDir string) (ResolvedPaths, error) {
	cfg = cfg.WithDefaults()
	root := strings.TrimSpace(cfg.RootDir)
	if root != "" {
		expanded, err := expandUser(root)
		if err != nil {
			return ResolvedPaths{}, err
		}
		abs, err := filepath.Abs(expanded)
		if err != nil {
			abs = filepath.Clean(expanded)
		}
		return ResolvedPaths{RootDir: abs, ProjectKey: strings.TrimSpace(cfg.ProjectKey)}, nil
	}

	workspace := strings.TrimSpace(cfg.WorkspaceDir)
	if workspace == "" {
		workspace = DefaultConfig().WorkspaceDir
	}
	workspaceExpanded, err := expandUser(workspace)
	if err != nil {
		return ResolvedPaths{}, err
	}
	workspaceAbs, err := filepath.Abs(workspaceExpanded)
	if err != nil {
		workspaceAbs = filepath.Clean(workspaceExpanded)
	}

	projectKey := strings.TrimSpace(cfg.ProjectKey)
	if projectKey == "" {
		projectKey = deriveProjectKey(workDir)
	}
	projectKey = sanitizeKey(projectKey)
	if projectKey == "" {
		projectKey = "project"
	}
	rootAbs := filepath.Join(workspaceAbs, projectKey, "memory")
	return ResolvedPaths{RootDir: rootAbs, ProjectKey: projectKey}, nil
}

func expandUser(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("path is empty")
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~"+string(os.PathSeparator)) || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		if trimmed == "~" {
			return home, nil
		}
		rest := strings.TrimPrefix(trimmed, "~")
		rest = strings.TrimPrefix(rest, "/")
		rest = strings.TrimPrefix(rest, "\\")
		return filepath.Join(home, rest), nil
	}
	return trimmed, nil
}

func sanitizeKey(key string) string {
	in := strings.TrimSpace(key)
	if in == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range in {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_'
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteRune('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "._-")
	out = strings.Trim(out, "_")
	return out
}

func deriveProjectKey(workDir string) string {
	dir := strings.TrimSpace(workDir)
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	abs := dir
	if absPath, err := filepath.Abs(dir); err == nil {
		abs = absPath
	}

	if remote := gitOriginRemoteURL(abs); remote != "" {
		if key := projectKeyFromRemote(remote); key != "" {
			return key
		}
	}

	base := filepath.Base(abs)
	if strings.TrimSpace(base) == "" || base == string(os.PathSeparator) || base == "." {
		base = "project"
	}
	h := sha1.Sum([]byte(abs))
	return fmt.Sprintf("%s_%s", base, hex.EncodeToString(h[:])[:8])
}

func projectKeyFromRemote(remote string) string {
	s := strings.TrimSpace(remote)
	if s == "" {
		return ""
	}
	s = strings.TrimSuffix(s, ".git")

	host := ""
	path := ""

	if strings.HasPrefix(s, "git@") {
		rest := strings.TrimPrefix(s, "git@")
		if idx := strings.Index(rest, ":"); idx >= 0 {
			host = rest[:idx]
			path = rest[idx+1:]
		} else if idx := strings.Index(rest, "/"); idx >= 0 {
			host = rest[:idx]
			path = rest[idx+1:]
		} else {
			host = rest
		}
	} else if strings.Contains(s, "://") {
		parts := strings.SplitN(s, "://", 2)
		rest := parts[1]
		rest = strings.TrimPrefix(rest, "git@")
		if idx := strings.Index(rest, "/"); idx >= 0 {
			host = rest[:idx]
			path = strings.TrimPrefix(rest[idx:], "/")
		} else {
			host = rest
		}
	} else {
		if idx := strings.Index(s, "/"); idx >= 0 {
			host = s[:idx]
			path = strings.TrimPrefix(s[idx:], "/")
		} else if idx := strings.Index(s, ":"); idx >= 0 {
			host = s[:idx]
			path = s[idx+1:]
		} else {
			host = s
		}
	}

	if host == "" {
		return ""
	}
	host = strings.TrimSpace(host)
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")

	key := host
	if path != "" {
		key = host + "_" + strings.ReplaceAll(path, "/", "_")
	}
	return sanitizeKey(key)
}

func gitOriginRemoteURL(startDir string) string {
	root := findGitRoot(startDir)
	if root == "" {
		return ""
	}

	gitPath := filepath.Join(root, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}

	gitDir := gitPath
	if !info.IsDir() {
		if resolved := resolveGitDirFile(root, gitPath); resolved != "" {
			gitDir = resolved
		} else {
			return ""
		}
	}

	cfgPath := filepath.Join(gitDir, "config")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	inOrigin := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
			section = strings.TrimSpace(section)
			inOrigin = section == `remote "origin"`
			continue
		}
		if !inOrigin {
			continue
		}
		if !strings.HasPrefix(trimmed, "url") {
			continue
		}
		kv := strings.SplitN(trimmed, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		if key != "url" {
			continue
		}
		value := strings.TrimSpace(kv[1])
		value = strings.Trim(value, `"'`)
		return value
	}
	return ""
}

func resolveGitDirFile(root string, gitFile string) string {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(data))
	lower := strings.ToLower(text)
	if !strings.HasPrefix(lower, "gitdir:") {
		return ""
	}
	rest := strings.TrimSpace(text[len("gitdir:"):])
	if rest == "" {
		return ""
	}
	if filepath.IsAbs(rest) {
		return filepath.Clean(rest)
	}
	return filepath.Clean(filepath.Join(root, rest))
}

func findGitRoot(startDir string) string {
	current := filepath.Clean(strings.TrimSpace(startDir))
	if current == "" {
		return ""
	}
	maxDepth := 12
	if runtime.GOOS == "windows" {
		maxDepth = 20
	}
	for i := 0; i < maxDepth; i++ {
		gitPath := filepath.Join(current, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return ""
}
