package agent

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"test_skill_agent/internal/llm"
)

type AutoCompactionConfig struct {
	Enabled bool

	// MaxAttempts is the number of compaction attempts after a context overflow.
	// Each attempt compacts the message history and retries the request once.
	MaxAttempts int

	// KeepLastUserTurns keeps the last N user turns verbatim when compacting.
	KeepLastUserTurns int

	// SummaryMaxTokens limits the model output tokens for the summary call.
	SummaryMaxTokens int

	// SummaryMaxChars hard-limits the injected summary size.
	SummaryMaxChars int

	// SummaryInputMaxChars limits the transcript text sent to the summarizer.
	SummaryInputMaxChars int

	// HardMaxToolResultChars always truncates extremely large tool results.
	HardMaxToolResultChars int

	// OverflowMaxToolResultChars truncates tool results more aggressively during overflow recovery.
	OverflowMaxToolResultChars int
}

type AutoCompactionConfigPatch struct {
	Enabled *bool `json:"enabled,omitempty"`

	MaxAttempts       *int `json:"max_attempts,omitempty"`
	KeepLastUserTurns *int `json:"keep_last_user_turns,omitempty"`

	SummaryMaxTokens     *int `json:"summary_max_tokens,omitempty"`
	SummaryMaxChars      *int `json:"summary_max_chars,omitempty"`
	SummaryInputMaxChars *int `json:"summary_input_max_chars,omitempty"`

	HardMaxToolResultChars     *int `json:"hard_max_tool_result_chars,omitempty"`
	OverflowMaxToolResultChars *int `json:"overflow_max_tool_result_chars,omitempty"`
}

func (p AutoCompactionConfigPatch) ApplyTo(cfg AutoCompactionConfig) AutoCompactionConfig {
	out := cfg
	if p.Enabled != nil {
		out.Enabled = *p.Enabled
	}
	if p.MaxAttempts != nil && *p.MaxAttempts >= 0 {
		out.MaxAttempts = *p.MaxAttempts
	}
	if p.KeepLastUserTurns != nil && *p.KeepLastUserTurns >= 1 {
		out.KeepLastUserTurns = *p.KeepLastUserTurns
	}
	if p.SummaryMaxTokens != nil && *p.SummaryMaxTokens >= 1 {
		out.SummaryMaxTokens = *p.SummaryMaxTokens
	}
	if p.SummaryMaxChars != nil && *p.SummaryMaxChars >= 1 {
		out.SummaryMaxChars = *p.SummaryMaxChars
	}
	if p.SummaryInputMaxChars != nil && *p.SummaryInputMaxChars >= 1 {
		out.SummaryInputMaxChars = *p.SummaryInputMaxChars
	}
	if p.HardMaxToolResultChars != nil && *p.HardMaxToolResultChars >= 1 {
		out.HardMaxToolResultChars = *p.HardMaxToolResultChars
	}
	if p.OverflowMaxToolResultChars != nil && *p.OverflowMaxToolResultChars >= 1 {
		out.OverflowMaxToolResultChars = *p.OverflowMaxToolResultChars
	}
	return out
}

func DefaultAutoCompactionConfig() AutoCompactionConfig {
	return AutoCompactionConfig{
		Enabled:                    true,
		MaxAttempts:                3,
		KeepLastUserTurns:          6,
		SummaryMaxTokens:           512,
		SummaryMaxChars:            6000,
		SummaryInputMaxChars:       16_000,
		HardMaxToolResultChars:     400_000,
		OverflowMaxToolResultChars: 50_000,
	}
}

func (c AutoCompactionConfig) withDefaults() AutoCompactionConfig {
	d := DefaultAutoCompactionConfig()
	out := c
	if !out.Enabled {
		// allow explicit disable; keep other fields as-is
		return out
	}
	if out.MaxAttempts < 0 {
		out.MaxAttempts = d.MaxAttempts
	}
	if out.KeepLastUserTurns <= 0 {
		out.KeepLastUserTurns = d.KeepLastUserTurns
	}
	if out.SummaryMaxTokens <= 0 {
		out.SummaryMaxTokens = d.SummaryMaxTokens
	}
	if out.SummaryMaxChars <= 0 {
		out.SummaryMaxChars = d.SummaryMaxChars
	}
	if out.SummaryInputMaxChars <= 0 {
		out.SummaryInputMaxChars = d.SummaryInputMaxChars
	}
	if out.HardMaxToolResultChars <= 0 {
		out.HardMaxToolResultChars = d.HardMaxToolResultChars
	}
	if out.OverflowMaxToolResultChars <= 0 {
		out.OverflowMaxToolResultChars = d.OverflowMaxToolResultChars
	}
	return out
}

type llmChatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

func chatWithAutoCompaction(
	ctx context.Context,
	client llmChatClient,
	req llm.ChatRequest,
	cfg AutoCompactionConfig,
	onCompaction func(summary llm.Message),
) (*llm.ChatResponse, []llm.Message, error) {
	if client == nil {
		return nil, req.Messages, fmt.Errorf("llm client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cfg = cfg.withDefaults()
	if !cfg.Enabled {
		resp, err := client.Chat(ctx, req)
		return resp, req.Messages, err
	}

	// Always apply a hard cap to prevent pathological tool results from poisoning the context.
	messages := truncateToolMessages(req.Messages, cfg.HardMaxToolResultChars, "hard-limit")

	req.Messages = messages
	for attempt := 0; attempt <= cfg.MaxAttempts; attempt++ {
		resp, err := client.Chat(ctx, req)
		if err == nil {
			return resp, req.Messages, nil
		}
		if !llm.IsLikelyContextOverflowError(err) {
			return nil, req.Messages, err
		}
		if attempt >= cfg.MaxAttempts {
			return nil, req.Messages, err
		}

		compacted, summaryMsg, ok := compactMessagesForOverflow(ctx, client, req.Messages, cfg)
		if !ok {
			return nil, req.Messages, err
		}
		req.Messages = compacted
		if summaryMsg != nil && onCompaction != nil {
			onCompaction(*summaryMsg)
		}
	}

	return nil, req.Messages, fmt.Errorf("unreachable: compaction loop")
}

func compactMessagesForOverflow(
	ctx context.Context,
	client llmChatClient,
	messages []llm.Message,
	cfg AutoCompactionConfig,
) ([]llm.Message, *llm.Message, bool) {
	cfg = cfg.withDefaults()
	if len(messages) == 0 {
		return messages, nil, false
	}

	// More aggressive tool truncation on overflow.
	messages = truncateToolMessages(messages, cfg.OverflowMaxToolResultChars, "overflow-recovery")

	// Keep the leading system preamble intact.
	sysPrefixLen := 0
	for sysPrefixLen < len(messages) && strings.EqualFold(strings.TrimSpace(messages[sysPrefixLen].Role), "system") {
		sysPrefixLen++
	}
	sysPrefix := append([]llm.Message{}, messages[:sysPrefixLen]...)
	rest := messages[sysPrefixLen:]
	if len(rest) == 0 {
		return messages, nil, false
	}

	tailStart := findTailStartByUserTurns(rest, cfg.KeepLastUserTurns)
	if tailStart <= 0 || tailStart >= len(rest) {
		return messages, nil, false
	}

	prefix := rest[:tailStart]
	tail := rest[tailStart:]

	summary, err := summarizeForCompaction(ctx, client, prefix, cfg)
	if err != nil || strings.TrimSpace(summary) == "" {
		// Fallback: drop the prefix without a model-generated summary.
		fallback := llm.Message{
			Role: "system",
			Content: "[System Message] Context compacted automatically due to context overflow. " +
				"Earlier messages were omitted; ask for specific details if needed.",
		}
		compacted := append(sysPrefix, append([]llm.Message{fallback}, tail...)...)
		return compacted, &fallback, true
	}

	summary = clampUTF8(summary, cfg.SummaryMaxChars)
	summaryMsg := llm.Message{
		Role: "system",
		Content: "[System Message] Context compacted automatically due to context overflow.\n\n" +
			"Summary of earlier conversation:\n" + summary,
	}
	compacted := append(sysPrefix, append([]llm.Message{summaryMsg}, tail...)...)
	return compacted, &summaryMsg, true
}

func summarizeForCompaction(
	ctx context.Context,
	client llmChatClient,
	messages []llm.Message,
	cfg AutoCompactionConfig,
) (string, error) {
	if client == nil {
		return "", fmt.Errorf("llm client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg = cfg.withDefaults()

	transcript := buildTranscriptForSummary(messages, cfg.SummaryInputMaxChars)
	if strings.TrimSpace(transcript) == "" {
		return "", fmt.Errorf("empty transcript")
	}

	sys := llm.Message{
		Role: "system",
		Content: strings.Join([]string{
			"You are compacting a conversation transcript to fit within an LLM context window.",
			"Write a concise, factual summary that preserves: user goal, constraints, key decisions, key tool outputs/results, and current TODOs/open questions.",
			"Prefer bullet points. Avoid fluff. Do not invent details.",
		}, "\n"),
	}
	user := llm.Message{
		Role:    "user",
		Content: "Transcript (older portion):\n\n" + transcript,
	}

	req := llm.ChatRequest{
		Messages:    []llm.Message{sys, user},
		MaxTokens:   cfg.SummaryMaxTokens,
		Temperature: 0.2,
	}
	resp, err := client.Chat(ctx, req)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in summary response")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func buildTranscriptForSummary(messages []llm.Message, maxChars int) string {
	if maxChars <= 0 || len(messages) == 0 {
		return ""
	}

	blocks := make([]string, 0, len(messages))
	for _, m := range messages {
		blocks = append(blocks, formatMessageForSummary(m))
	}
	return joinBlocksHeadTail(blocks, maxChars)
}

func formatMessageForSummary(m llm.Message) string {
	role := strings.ToLower(strings.TrimSpace(m.Role))
	switch role {
	case "system":
		return "[system]\n" + clampUTF8(strings.TrimSpace(m.Content), 800)
	case "user":
		return "[user]\n" + clampUTF8(strings.TrimSpace(m.Content), 1200)
	case "assistant":
		if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) > 0 {
			names := make([]string, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				if n := strings.TrimSpace(c.Function.Name); n != "" {
					names = append(names, n)
				}
			}
			if len(names) == 0 {
				return "[assistant]\n(tool_calls)"
			}
			return "[assistant]\n(tool_calls: " + strings.Join(names, ", ") + ")"
		}
		return "[assistant]\n" + clampUTF8(strings.TrimSpace(m.Content), 1600)
	case "tool":
		id := strings.TrimSpace(m.ToolCallID)
		head := "[tool]"
		if id != "" {
			head = "[tool tool_call_id=" + id + "]"
		}
		return head + "\n" + clampUTF8(strings.TrimSpace(m.Content), 2000)
	default:
		return "[" + role + "]\n" + clampUTF8(strings.TrimSpace(m.Content), 800)
	}
}

func joinBlocksHeadTail(blocks []string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	sep := "\n\n"
	total := 0
	for i := range blocks {
		if blocks[i] == "" {
			continue
		}
		if total > 0 {
			total += len(sep)
		}
		total += len(blocks[i])
	}
	if total <= maxChars {
		return strings.Join(blocks, sep)
	}

	headBudget := int(float64(maxChars) * 0.25)
	tailBudget := int(float64(maxChars) * 0.65)
	marker := "\n\n...[omitted for brevity]...\n\n"
	remain := maxChars - len(marker)
	if remain < 0 {
		remain = 0
	}
	if headBudget+tailBudget > remain {
		// Keep a little space for both sides.
		headBudget = remain / 3
		tailBudget = remain - headBudget
	}

	head := make([]string, 0, len(blocks))
	headLen := 0
	for _, b := range blocks {
		if strings.TrimSpace(b) == "" {
			continue
		}
		add := len(b)
		if headLen > 0 {
			add += len(sep)
		}
		if headLen+add > headBudget && headLen > 0 {
			break
		}
		head = append(head, b)
		headLen += add
	}

	tail := make([]string, 0, len(blocks))
	tailLen := 0
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		if strings.TrimSpace(b) == "" {
			continue
		}
		add := len(b)
		if tailLen > 0 {
			add += len(sep)
		}
		if tailLen+add > tailBudget && tailLen > 0 {
			break
		}
		tail = append(tail, b)
		tailLen += add
	}

	// Reverse tail to restore order.
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
	}

	// Avoid overlap when budgets are too small.
	if len(head) > 0 && len(tail) > 0 {
		lastHead := head[len(head)-1]
		firstTail := tail[0]
		if lastHead == firstTail {
			tail = tail[1:]
		}
	}

	if len(tail) == 0 {
		return strings.Join(head, sep)
	}
	if len(head) == 0 {
		return strings.Join(tail, sep)
	}
	return strings.Join(head, sep) + marker + strings.Join(tail, sep)
}

func findTailStartByUserTurns(messages []llm.Message, keepUserTurns int) int {
	if keepUserTurns <= 0 {
		return 0
	}
	seenUsers := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			seenUsers++
			if seenUsers >= keepUserTurns {
				return i
			}
		}
	}
	return 0
}

func truncateToolMessages(messages []llm.Message, maxChars int, reason string) []llm.Message {
	if maxChars <= 0 || len(messages) == 0 {
		return messages
	}
	out := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		if !strings.EqualFold(strings.TrimSpace(m.Role), "tool") {
			out = append(out, m)
			continue
		}
		content := strings.TrimSpace(m.Content)
		if len(content) <= maxChars {
			out = append(out, m)
			continue
		}
		truncated := truncateToolResultText(content, maxChars, reason)
		m.Content = truncated
		out = append(out, m)
	}
	return out
}

func truncateToolResultText(text string, maxChars int, reason string) string {
	if maxChars <= 0 {
		return ""
	}
	const minKeep = 2000
	suffix := "\n\n⚠️ [Tool result truncated (" + reason + ") — original was too large for the model context. " +
		"Request a narrower query or add paging/limits to the tool call.]"

	if len(text) <= maxChars {
		return text
	}
	keep := maxChars - len(suffix)
	if keep < minKeep {
		keep = minKeep
	}
	head := clampUTF8(text, keep)
	if idx := strings.LastIndex(head, "\n"); idx > 0 && idx > int(float64(len(head))*0.8) {
		head = head[:idx]
	}
	return head + suffix
}

func pruneHistoryAfterLastAutoCompaction(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(messages[i].Role), "system") {
			continue
		}
		if strings.Contains(messages[i].Content, "Context compacted automatically") {
			return messages[i:]
		}
	}
	return messages
}

func clampUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if s == "" {
		return s
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	if cut > len(s) {
		cut = len(s)
	}
	if cut <= 0 {
		return ""
	}
	// Move back to a UTF-8 rune boundary.
	for cut > 0 && (s[cut-1]&0xC0) == 0x80 {
		cut--
	}
	if cut <= 0 {
		return ""
	}
	out := s[:cut]
	if !utf8.ValidString(out) {
		// Extremely defensive fallback: walk back until valid.
		for cut > 0 && !utf8.ValidString(s[:cut]) {
			cut--
		}
		if cut <= 0 {
			return ""
		}
		out = s[:cut]
	}
	return strings.TrimRight(out, "\n")
}
