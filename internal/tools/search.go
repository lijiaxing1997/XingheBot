package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"test_skill_agent/internal/llm"
)

type SearchTool struct{}

type searchArgs struct {
	Path          string `json:"path"`
	Dir           string `json:"dir"`
	Query         string `json:"query"`
	Recursive     *bool  `json:"recursive"`
	CaseSensitive bool   `json:"case_sensitive"`
	IncludeHidden bool   `json:"include_hidden"`
	MaxResults    int    `json:"max_results"`
	MaxFileBytes  int64  `json:"max_file_bytes"`
}

type searchMatcher struct {
	groups        [][]string
	caseSensitive bool
}

type lineMatch struct {
	Line int
	Text string
}

func (t *SearchTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "search",
			Description: "Search text in files under a directory. Query supports logical operators: `term1|term2` (OR) and `term1&term2` (AND). AND has higher precedence than OR.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Root directory to search (default: .).",
					},
					"dir": map[string]interface{}{
						"type":        "string",
						"description": "Alias for path.",
					},
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query. Use `|` for OR and `&` for AND. Example: `error&timeout|panic`.",
					},
					"recursive": map[string]interface{}{
						"type":        "boolean",
						"description": "Search recursively (default: true).",
					},
					"case_sensitive": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether matching is case-sensitive (default: false).",
					},
					"include_hidden": map[string]interface{}{
						"type":        "boolean",
						"description": "Include hidden files/directories (default: false).",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum matched lines to return (default: 200).",
					},
					"max_file_bytes": map[string]interface{}{
						"type":        "integer",
						"description": "Skip files larger than this size in bytes (default: 1048576).",
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
	}
}

func (t *SearchTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in searchArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}

	query := strings.TrimSpace(in.Query)
	if query == "" {
		return "", errors.New("query is required")
	}
	matcher, err := parseSearchQuery(query, in.CaseSensitive)
	if err != nil {
		return "", err
	}

	root := strings.TrimSpace(in.Path)
	if root == "" {
		root = strings.TrimSpace(in.Dir)
	}
	if root == "" {
		root = "."
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", root)
	}

	recursive := true
	if in.Recursive != nil {
		recursive = *in.Recursive
	}
	if in.MaxResults <= 0 {
		in.MaxResults = 200
	}
	if in.MaxFileBytes <= 0 {
		in.MaxFileBytes = 1 * 1024 * 1024
	}

	type searchStats struct {
		scannedFiles     int
		skippedLarge     int
		skippedBinary    int
		skippedError     int
		matches          []string
		resultsTruncated bool
	}
	stats := searchStats{
		matches: make([]string, 0, minInt(in.MaxResults, 128)),
	}

	stopErr := errors.New("max results reached")

	processFile := func(path string, fileInfo fs.FileInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if fileInfo.IsDir() {
			return nil
		}

		stats.scannedFiles++
		if fileInfo.Size() > in.MaxFileBytes {
			stats.skippedLarge++
			return nil
		}

		lines, isBinary, err := searchFileLines(ctx, path, matcher)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			stats.skippedError++
			return nil
		}
		if isBinary {
			stats.skippedBinary++
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		for _, line := range lines {
			stats.matches = append(stats.matches, fmt.Sprintf("%s:%d:%s", rel, line.Line, line.Text))
			if len(stats.matches) >= in.MaxResults {
				stats.resultsTruncated = true
				return stopErr
			}
		}
		return nil
	}

	if recursive {
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				stats.skippedError++
				return nil
			}
			if path == root {
				return nil
			}
			if !in.IncludeHidden && strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			fileInfo, infoErr := d.Info()
			if infoErr != nil {
				stats.skippedError++
				return nil
			}
			return processFile(path, fileInfo)
		})
		if err != nil && !errors.Is(err, stopErr) {
			return "", err
		}
	} else {
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			return "", readErr
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			name := entry.Name()
			if !in.IncludeHidden && strings.HasPrefix(name, ".") {
				continue
			}
			if entry.IsDir() {
				continue
			}
			fileInfo, infoErr := entry.Info()
			if infoErr != nil {
				stats.skippedError++
				continue
			}
			if err := processFile(filepath.Join(root, name), fileInfo); err != nil {
				if errors.Is(err, stopErr) {
					break
				}
				return "", err
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "searched_root: %s\n", root)
	fmt.Fprintf(&b, "scanned_files: %d\n", stats.scannedFiles)
	fmt.Fprintf(&b, "matched_lines: %d\n", len(stats.matches))
	fmt.Fprintf(&b, "truncated: %t\n", stats.resultsTruncated)
	fmt.Fprintf(&b, "skipped_large_files: %d\n", stats.skippedLarge)
	fmt.Fprintf(&b, "skipped_binary_files: %d\n", stats.skippedBinary)
	fmt.Fprintf(&b, "skipped_error_files: %d\n", stats.skippedError)
	if len(stats.matches) == 0 {
		b.WriteString("(no matches)")
		return b.String(), nil
	}
	b.WriteString("results:\n")
	b.WriteString(strings.Join(stats.matches, "\n"))
	return b.String(), nil
}

func parseSearchQuery(raw string, caseSensitive bool) (searchMatcher, error) {
	query := strings.TrimSpace(raw)
	if query == "" {
		return searchMatcher{}, errors.New("query is required")
	}

	orParts := strings.Split(query, "|")
	groups := make([][]string, 0, len(orParts))
	for _, orPart := range orParts {
		orPart = strings.TrimSpace(orPart)
		if orPart == "" {
			return searchMatcher{}, errors.New("invalid query: empty term around '|'")
		}
		andParts := strings.Split(orPart, "&")
		group := make([]string, 0, len(andParts))
		for _, andPart := range andParts {
			term := strings.TrimSpace(andPart)
			term = trimQuotedTerm(term)
			if term == "" {
				return searchMatcher{}, errors.New("invalid query: empty term around '&'")
			}
			if !caseSensitive {
				term = strings.ToLower(term)
			}
			group = append(group, term)
		}
		groups = append(groups, group)
	}

	return searchMatcher{
		groups:        groups,
		caseSensitive: caseSensitive,
	}, nil
}

func trimQuotedTerm(term string) string {
	if len(term) < 2 {
		return term
	}
	first := term[0]
	last := term[len(term)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return strings.TrimSpace(term[1 : len(term)-1])
	}
	return term
}

func (m searchMatcher) Match(line string) bool {
	candidate := line
	if !m.caseSensitive {
		candidate = strings.ToLower(candidate)
	}
	for _, group := range m.groups {
		allMatched := true
		for _, term := range group {
			if !strings.Contains(candidate, term) {
				allMatched = false
				break
			}
		}
		if allMatched {
			return true
		}
	}
	return false
}

func searchFileLines(ctx context.Context, path string, matcher searchMatcher) ([]lineMatch, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 8192)
	sample, err := reader.Peek(8192)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, false, err
	}
	if isLikelyBinary(sample) {
		return nil, true, nil
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	results := make([]lineMatch, 0, 8)
	lineNumber := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		lineNumber++
		text := scanner.Text()
		if matcher.Match(text) {
			results = append(results, lineMatch{
				Line: lineNumber,
				Text: text,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return results, false, nil
}

func isLikelyBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	control := 0
	for _, b := range data {
		if b < 0x09 || (b > 0x0D && b < 0x20) {
			control++
		}
	}
	ratio := float64(control) / float64(len(data))
	return ratio > 0.2
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
