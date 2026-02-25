package heartbeatrunner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	reMarkdownHeaderLine = regexp.MustCompile(`^#+(\s|$)`)
	reEmptyListItem      = regexp.MustCompile(`^[-*+]\s*(\[[\sXx]?\]\s*)?$`)
)

func IsHeartbeatContentEffectivelyEmpty(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if reMarkdownHeaderLine.MatchString(trimmed) {
			continue
		}
		if reEmptyListItem.MatchString(trimmed) {
			continue
		}
		return false
	}
	return true
}

func ResolveHeartbeatFilePath(configuredPath string, workDir string) (string, error) {
	p := strings.TrimSpace(configuredPath)
	if p == "" {
		p = "HEARTBEAT.md"
	}
	if !filepath.IsAbs(p) {
		base := strings.TrimSpace(workDir)
		if base == "" {
			cwd, _ := os.Getwd()
			base = cwd
		}
		p = filepath.Join(base, p)
	}
	abs, err := filepath.Abs(p)
	if err == nil {
		p = abs
	}
	return filepath.Clean(p), nil
}

func ReadHeartbeatFile(path string) (content string, exists bool, effectivelyEmpty bool, err error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", false, true, errors.New("path is empty")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, true, nil
		}
		return "", false, true, err
	}
	text := string(data)
	eff := IsHeartbeatContentEffectivelyEmpty(text)
	return text, true, eff, nil
}

func WriteHeartbeatFileAtomic(path string, content string) error {
	p := strings.TrimSpace(path)
	if p == "" {
		return errors.New("path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(p); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write symlink: %s", p)
		}
	}
	tmp := fmt.Sprintf("%s.tmp-%d", p, time.Now().UTC().UnixNano())
	data := []byte(ensureTrailingNewline(content))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
