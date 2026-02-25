package memory

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultMaxGetLines    = 200
	defaultGetLines       = 50
	defaultSearchMaxPool  = 2000
	defaultSnippetMaxRune = 240
)

type SearchResult struct {
	Path      string  `json:"path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
}

type scoredResult struct {
	SearchResult
	mtime time.Time
}

type SearchResponse struct {
	Results    []SearchResult `json:"results"`
	Disabled   bool           `json:"disabled"`
	Backend    string         `json:"backend"`
	Root       string         `json:"root"`
	ProjectKey string         `json:"project_key,omitempty"`
}

type GetResponse struct {
	Path  string `json:"path"`
	From  int    `json:"from"`
	Lines int    `json:"lines"`
	Text  string `json:"text"`
}

type AppendResponse struct {
	Path      string `json:"path"`
	Line      string `json:"line"`
	Appended  bool   `json:"appended"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

func EnsureLayout(root string) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("memory root is empty")
	}
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("memory root must not be a symlink: %s", root)
		}
		if !info.IsDir() {
			return fmt.Errorf("memory root is not a directory: %s", root)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, dir := range []string{"daily", "sessions", "index"} {
		sub := filepath.Join(root, dir)
		if info, err := os.Lstat(sub); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("memory subdir must not be a symlink: %s", sub)
		}
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return err
		}
	}
	memPath := filepath.Join(root, "MEMORY.md")
	if info, err := os.Lstat(memPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("memory file must not be a symlink: %s", memPath)
	}
	if _, err := os.Stat(memPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		template := strings.Join([]string{
			"# MEMORY",
			"",
			"> This file is auto-loaded into the PRIMARY agent prompt and may be auto-updated.",
			"> Keep it compact (<= 1000 chars). Avoid secrets.",
			"",
			"## Preferences",
			"- (user preferences; add source=run_id when possible)",
			"",
			"## TODO",
			"- (open TODOs across sessions; include source=run_id)",
			"",
			"## Work Log",
			"- (brief work record summaries; include source=run_id)",
			"",
			"## Notes",
			"- (key facts/decisions/constraints; include source=run_id; avoid secrets)",
			"",
		}, "\n")
		if err := os.WriteFile(memPath, []byte(template), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func Get(ctx context.Context, root string, relPath string, from int, lines int) (GetResponse, error) {
	if strings.TrimSpace(relPath) == "" {
		return GetResponse{}, errors.New("path is required")
	}
	if from <= 0 {
		from = 1
	}
	if lines <= 0 {
		lines = defaultGetLines
	}
	if lines > defaultMaxGetLines {
		lines = defaultMaxGetLines
	}

	abs, cleanRel, err := safeResolveMarkdownPath(root, relPath, false)
	if err != nil {
		return GetResponse{}, err
	}
	text, err := readFileLines(ctx, abs, from, lines)
	if err != nil {
		return GetResponse{}, err
	}
	return GetResponse{Path: cleanRel, From: from, Lines: lines, Text: text}, nil
}

func AppendDaily(ctx context.Context, cfg Config, root string, kind string, text string, tags []string, source string, now time.Time) (AppendResponse, error) {
	cfg = cfg.WithDefaults()
	if cfg.Enabled != nil && !*cfg.Enabled {
		return AppendResponse{}, errors.New("memory is disabled by config")
	}

	if strings.TrimSpace(text) == "" {
		return AppendResponse{}, errors.New("text is required")
	}
	if err := EnsureLayout(root); err != nil {
		return AppendResponse{}, err
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}
	date := now.Format("2006-01-02")
	rel := path.Join("daily", date+".md")
	abs, cleanRel, err := safeResolveMarkdownPath(root, rel, true)
	if err != nil {
		return AppendResponse{}, err
	}

	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "note"
	}

	line := fmt.Sprintf("- %s [%s] %s", now.UTC().Format(time.RFC3339), kind, strings.TrimSpace(text))
	if tagsText := formatTags(tags); tagsText != "" {
		line += " " + tagsText
	}
	if strings.TrimSpace(source) != "" {
		line += fmt.Sprintf(" (source=%s)", strings.TrimSpace(source))
	}

	line, _ = RedactText(cfg, line)
	if runeLen(line) > 4000 {
		line = truncateRunes(line, 4000) + "…"
	}
	line = strings.TrimRight(line, "\r\n")
	line += "\n"

	lockPath := filepath.Join(root, "index", ".daily.lock")
	resp := AppendResponse{Path: cleanRel, Line: strings.TrimRight(line, "\n")}
	newKey, _ := dailyLineKey(resp.Line)
	if err := withFileLock(ctx, lockPath, 5*time.Second, func() error {
		if err := EnsureLayout(root); err != nil {
			return err
		}
		if newKey != "" {
			dup, err := dailyFileHasKey(ctx, abs, newKey)
			if err != nil {
				return err
			}
			if dup {
				resp.Appended = false
				resp.Duplicate = true
				return nil
			}
		}
		f, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.WriteString(f, line)
		if err == nil {
			resp.Appended = true
		}
		return err
	}); err != nil {
		return AppendResponse{}, err
	}

	return resp, nil
}

func dailyFileHasKey(ctx context.Context, path string, key string) (bool, error) {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(key) == "" {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		lineKey, ok := dailyLineKey(scanner.Text())
		if ok && lineKey == key {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func dailyLineKey(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "- ") {
		return "", false
	}
	rest := strings.TrimSpace(s[2:])
	if rest == "" {
		return "", false
	}

	open := strings.Index(rest, "[")
	close := -1
	if open >= 0 {
		if idx := strings.Index(rest[open:], "]"); idx >= 0 {
			close = open + idx
		}
	}
	if open < 0 || close < 0 || close <= open+1 {
		return "", false
	}

	kind := strings.ToLower(strings.TrimSpace(rest[open+1 : close]))
	if kind == "" {
		return "", false
	}

	content := strings.TrimSpace(rest[close+1:])
	if content == "" {
		return "", false
	}

	if idx := strings.LastIndex(content, " (source="); idx >= 0 && strings.HasSuffix(content, ")") {
		content = strings.TrimSpace(content[:idx])
	}

	fields := strings.Fields(content)
	if len(fields) == 0 {
		return "", false
	}

	tags := make([]string, 0, 4)
	end := len(fields)
	for end > 0 {
		f := strings.TrimSpace(fields[end-1])
		if strings.HasPrefix(f, "#") && len(f) > 1 {
			tags = append(tags, f)
			end--
			continue
		}
		break
	}

	text := strings.TrimSpace(strings.Join(fields[:end], " "))
	if text == "" {
		return "", false
	}

	key := kind + "|" + normalizeDedupeKey(text)
	if len(tags) > 0 {
		sort.Strings(tags)
		key += "|" + strings.Join(tags, ",")
	}
	return key, true
}

func Search(ctx context.Context, cfg Config, root string, query string, maxResults int) (SearchResponse, error) {
	cfg = cfg.WithDefaults()
	resp := SearchResponse{
		Results:  make([]SearchResult, 0),
		Disabled: false,
		Backend:  "scan",
		Root:     root,
	}
	if cfg.Enabled != nil && !*cfg.Enabled {
		resp.Disabled = true
		resp.Backend = "disabled"
		return resp, nil
	}

	q := strings.TrimSpace(query)
	if q == "" {
		return SearchResponse{}, errors.New("query is required")
	}
	if maxResults <= 0 {
		maxResults = cfg.MaxResults
	}
	if maxResults <= 0 {
		maxResults = DefaultConfig().MaxResults
	}
	if maxResults > 50 {
		maxResults = 50
	}

	files := listMemoryFiles(root)
	if len(files) == 0 {
		return resp, nil
	}

	matches := make([]scoredResult, 0, minInt(maxResults*4, 64))

	tokens := queryTokens(q)
	qLower := strings.ToLower(q)

	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return SearchResponse{}, err
		}
		info, err := os.Lstat(file)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		rel, _ := filepath.Rel(root, file)
		rel = filepath.ToSlash(rel)

		f, err := os.Open(file)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			score := lineScore(line, qLower, tokens)
			if score <= 0 {
				continue
			}
			snippet := strings.TrimSpace(line)
			if snippet == "" {
				continue
			}
			if runeLen(snippet) > defaultSnippetMaxRune {
				snippet = truncateRunes(snippet, defaultSnippetMaxRune) + "…"
			}
			matches = append(matches, scoredResult{
				SearchResult: SearchResult{
					Path:      rel,
					StartLine: lineNo,
					EndLine:   lineNo,
					Score:     score,
					Snippet:   snippet,
				},
				mtime: info.ModTime(),
			})
			if len(matches) >= defaultSearchMaxPool {
				break
			}
		}
		_ = f.Close()
		if len(matches) >= defaultSearchMaxPool {
			break
		}
	}

	if len(matches) == 0 {
		return resp, nil
	}

	sortScoredResults(matches)
	out := make([]SearchResult, 0, minInt(maxResults, len(matches)))
	for i := 0; i < len(matches) && len(out) < maxResults; i++ {
		out = append(out, matches[i].SearchResult)
	}
	resp.Results = out
	return resp, nil
}

func listMemoryFiles(root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}

	files := make([]string, 0, 64)
	addFile := func(path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return
		}
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return
		}
		files = append(files, path)
	}

	mem := filepath.Join(root, "MEMORY.md")
	if _, err := os.Stat(mem); err == nil {
		addFile(mem)
	}

	for _, dir := range []string{"daily", "sessions"} {
		subdir := filepath.Join(root, dir)
		if info, err := os.Lstat(subdir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		entries, err := os.ReadDir(subdir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			name := ent.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".md") {
				continue
			}
			p := filepath.Join(subdir, name)
			if ent.Type()&os.ModeSymlink != 0 {
				continue
			}
			addFile(p)
		}
	}

	sortFilesByMtimeDesc(files)
	return files
}

func sortScoredResults(matches []scoredResult) {
	sort.Slice(matches, func(i, j int) bool {
		a := matches[i]
		b := matches[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if !a.mtime.Equal(b.mtime) {
			return a.mtime.After(b.mtime)
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.StartLine > b.StartLine
	})
}

func safeResolveMarkdownPath(root string, rel string, allowMissing bool) (abs string, cleanRel string, err error) {
	rootAbs := strings.TrimSpace(root)
	if rootAbs == "" {
		return "", "", errors.New("memory root is empty")
	}
	rootAbs, err = filepath.Abs(rootAbs)
	if err != nil {
		rootAbs = filepath.Clean(rootAbs)
	}
	if info, err := os.Lstat(rootAbs); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("memory root must not be a symlink: %s", rootAbs)
		}
		if !info.IsDir() {
			return "", "", fmt.Errorf("memory root is not a directory: %s", rootAbs)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	} else if !allowMissing {
		return "", "", err
	}

	trimmed := strings.TrimSpace(rel)
	if trimmed == "" {
		return "", "", errors.New("path is empty")
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "~") {
		return "", "", errors.New("path must be relative to memory root")
	}

	clean := path.Clean(filepath.ToSlash(trimmed))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", "", errors.New("invalid relative path")
	}
	if !strings.HasSuffix(strings.ToLower(clean), ".md") {
		return "", "", errors.New("only .md files are allowed")
	}

	targetAbs := filepath.Join(rootAbs, filepath.FromSlash(clean))

	relCheck, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", "", errors.New("invalid path")
	}
	relCheck = filepath.Clean(relCheck)
	if relCheck == "." || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", "", errors.New("path escapes memory root")
	}

	if err := assertNoSymlinkPath(rootAbs, targetAbs, allowMissing); err != nil {
		return "", "", err
	}
	if !allowMissing {
		if _, err := os.Stat(targetAbs); err != nil {
			return "", "", err
		}
	}
	return targetAbs, clean, nil
}

func assertNoSymlinkPath(rootAbs string, targetAbs string, allowMissing bool) error {
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return errors.New("invalid path")
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return errors.New("invalid path")
	}
	parts := strings.Split(rel, string(filepath.Separator))
	cur := rootAbs
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && allowMissing {
				return nil
			}
			if errors.Is(err, os.ErrNotExist) {
				return err
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path is not allowed: %s", cur)
		}
	}
	return nil
}

func readFileLines(ctx context.Context, path string, from int, lines int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	start := from
	end := from + lines - 1
	cur := 0
	var b strings.Builder
	wrote := 0

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		cur++
		if cur < start {
			continue
		}
		if cur > end {
			break
		}
		if wrote > 0 {
			b.WriteString("\n")
		}
		b.WriteString(scanner.Text())
		wrote++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return b.String(), nil
}

func withFileLock(ctx context.Context, lockPath string, timeout time.Duration, fn func() error) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	staleAfter := 5 * time.Minute

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = io.WriteString(f, fmt.Sprintf("pid=%d time=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)))
			_ = f.Close()
			defer func() { _ = os.Remove(lockPath) }()
			return fn()
		}

		if !errors.Is(err, os.ErrExist) {
			return err
		}

		if info, statErr := os.Stat(lockPath); statErr == nil {
			if time.Since(info.ModTime()) > staleAfter {
				_ = os.Remove(lockPath)
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for memory lock: %s", lockPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
