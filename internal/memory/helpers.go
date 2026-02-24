package memory

import (
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if runeLen(s) <= maxRunes {
		return s
	}
	out := make([]rune, 0, maxRunes)
	for _, r := range s {
		out = append(out, r)
		if len(out) >= maxRunes {
			break
		}
	}
	return string(out)
}

func oneLine(text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func queryTokens(query string) []string {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil
	}
	fields := strings.Fields(trimmed)
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		t := strings.ToLower(strings.TrimSpace(f))
		t = strings.Trim(t, `"'`)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

func lineScore(line string, queryLower string, tokens []string) float64 {
	lower := strings.ToLower(line)
	if strings.TrimSpace(lower) == "" {
		return 0
	}
	matched := 0
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		if strings.Contains(lower, tok) {
			matched++
		}
	}
	if matched == 0 {
		return 0
	}
	score := float64(matched) / float64(maxInt(1, len(tokens)))
	if strings.Contains(lower, queryLower) {
		score += 0.5
	}
	if score > 1 {
		score = 1
	}
	return score
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sortFilesByMtimeDesc(files []string) {
	sort.Slice(files, func(i, j int) bool {
		ai, aErr := os.Stat(files[i])
		bi, bErr := os.Stat(files[j])
		if aErr == nil && bErr == nil {
			at := ai.ModTime()
			bt := bi.ModTime()
			if !at.Equal(bt) {
				return at.After(bt)
			}
			return files[i] < files[j]
		}
		if aErr == nil {
			return true
		}
		if bErr == nil {
			return false
		}
		return files[i] < files[j]
	})
}

func formatTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	out := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, raw := range tags {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "#") {
			t = "#" + t
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " ")
}
