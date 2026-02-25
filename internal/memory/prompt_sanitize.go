package memory

import "strings"

func SanitizeMemoryTextForPrompt(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if isLikelyPromptInjection(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
