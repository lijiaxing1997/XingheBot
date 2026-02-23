package llm

import (
	"regexp"
	"strings"
)

var (
	contextWindowTooSmallRe = regexp.MustCompile(`(?i)context window.*(too small|minimum is)`)
	contextOverflowHintRe   = regexp.MustCompile(`(?i)context.*overflow|context window.*(too (?:large|long)|exceed|over|limit|max(?:imum)?|requested|sent|tokens)|prompt.*(too (?:large|long)|exceed|over|limit|max(?:imum)?)|(?:request|input).*(?:context|window|length|token).*(too (?:large|long)|exceed|over|limit|max(?:imum)?)`)
	rateLimitHintRe         = regexp.MustCompile(`(?i)rate limit|too many requests|requests per (?:minute|hour|day)|quota|throttl|429\b|tpm\b|tpd\b`)
)

func IsLikelyContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	return IsLikelyContextOverflowText(err.Error())
}

func IsLikelyContextOverflowText(errorMessage string) bool {
	text := strings.TrimSpace(errorMessage)
	if text == "" {
		return false
	}
	if contextWindowTooSmallRe.MatchString(text) {
		return false
	}
	// Rate limit errors can match broad overflow heuristics (e.g. "request reached ... limit").
	if rateLimitHintRe.MatchString(text) {
		return false
	}
	lower := strings.ToLower(text)
	hasRequestSizeExceeds := strings.Contains(lower, "request size exceeds")
	hasContextWindow := strings.Contains(lower, "context window") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "maximum context length")
	if strings.Contains(lower, "request_too_large") ||
		strings.Contains(lower, "request exceeds the maximum size") ||
		strings.Contains(lower, "context length exceeded") ||
		strings.Contains(lower, "maximum context length") ||
		strings.Contains(lower, "prompt is too long") ||
		strings.Contains(lower, "exceeds model context window") ||
		(hasRequestSizeExceeds && hasContextWindow) ||
		strings.Contains(lower, "context overflow:") ||
		(strings.Contains(lower, "413") && strings.Contains(lower, "too large")) {
		return true
	}
	return contextOverflowHintRe.MatchString(text)
}
