package memory

import (
	"regexp"
	"strings"
)

var (
	reOpenAIKey = regexp.MustCompile(`\bsk-[A-Za-z0-9]{10,}\b`)
	reTavilyKey = regexp.MustCompile(`\btvly-[A-Za-z0-9]{10,}\b`)
	reAWSKey    = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
)

func RedactText(cfg Config, text string) (string, bool) {
	cfg = cfg.WithDefaults()
	if cfg.Redaction.Enabled != nil && !*cfg.Redaction.Enabled {
		return text, false
	}

	original := text
	out := text

	out = reOpenAIKey.ReplaceAllStringFunc(out, redactToken)
	out = reTavilyKey.ReplaceAllStringFunc(out, redactToken)
	out = reAWSKey.ReplaceAllStringFunc(out, redactToken)

	// Generic substring-triggered token masking.
	for _, pat := range cfg.Redaction.Patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		out = redactBySubstring(out, pat)
	}

	// Private key blocks.
	if strings.Contains(out, "-----BEGIN") {
		out = redactPEMBlocks(out)
	}

	return out, out != original
}

func redactToken(token string) string {
	t := strings.TrimSpace(token)
	if len(t) <= 8 {
		return "***"
	}
	prefix := t[:4]
	suffix := t[len(t)-4:]
	return prefix + "***" + suffix
}

func redactBySubstring(text string, substr string) string {
	if substr == "" {
		return text
	}
	lower := strings.ToLower(text)
	needle := strings.ToLower(substr)
	if !strings.Contains(lower, needle) {
		return text
	}

	var b strings.Builder
	b.Grow(len(text))

	i := 0
	for {
		idx := strings.Index(strings.ToLower(text[i:]), needle)
		if idx < 0 {
			b.WriteString(text[i:])
			break
		}
		idx += i
		b.WriteString(text[i:idx])

		// Expand to token boundary.
		start := idx
		end := idx + len(substr)
		for end < len(text) {
			c := text[end]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '"' || c == '\'' || c == ',' || c == ';' || c == ')' || c == ']' || c == '}' {
				break
			}
			end++
		}
		token := text[start:end]
		b.WriteString(redactToken(token))
		i = end
	}
	return b.String()
}

func redactPEMBlocks(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		if strings.HasPrefix(line, "-----BEGIN") {
			inBlock = true
			out = append(out, "-----BEGIN [REDACTED]-----")
			continue
		}
		if strings.HasPrefix(line, "-----END") {
			inBlock = false
			out = append(out, "-----END [REDACTED]-----")
			continue
		}
		if inBlock {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
