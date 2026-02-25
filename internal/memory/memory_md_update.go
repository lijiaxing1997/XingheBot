package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
)

type ToolRecord struct {
	Name      string
	Arguments string
	Result    string
	Error     string
}

type MemoryMDUpdateInput struct {
	RunID          string
	RunTitle       string
	UserRequest    string
	AssistantFinal string
	ToolRecords    []ToolRecord
	Now            time.Time
}

type MemoryMDUpdateResponse struct {
	Path       string `json:"path"`
	Updated    bool   `json:"updated"`
	OldChars   int    `json:"old_chars"`
	NewChars   int    `json:"new_chars"`
	Compressed bool   `json:"compressed"`
}

type chatClient interface {
	Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

const defaultMemoryMDUpdateModelTimeout = 90 * time.Second

func UpdateMemoryMDFromTurn(ctx context.Context, client chatClient, cfg Config, root string, in MemoryMDUpdateInput) (MemoryMDUpdateResponse, error) {
	cfg = cfg.WithDefaults()
	if cfg.Enabled != nil && !*cfg.Enabled {
		return MemoryMDUpdateResponse{Path: "MEMORY.md", Updated: false}, nil
	}
	if cfg.AutoUpdateMemoryMD != nil && !*cfg.AutoUpdateMemoryMD {
		return MemoryMDUpdateResponse{Path: "MEMORY.md", Updated: false}, nil
	}
	if client == nil {
		return MemoryMDUpdateResponse{}, errors.New("llm client is nil")
	}
	if strings.TrimSpace(root) == "" {
		return MemoryMDUpdateResponse{}, errors.New("memory root is empty")
	}
	runID := strings.TrimSpace(in.RunID)
	if runID == "" {
		runID = "unknown"
	}
	if strings.TrimSpace(in.Now.Format(time.RFC3339Nano)) == "" || in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	loc, locErr := ResolveLocation(cfg.Timezone)
	if locErr != nil || loc == nil {
		loc = time.Local
	}
	turnNow := in.Now.In(loc)
	maxChars := cfg.MemoryMDMaxChars
	if maxChars <= 0 {
		maxChars = 1000
	}
	if maxChars < 200 {
		maxChars = 200
	}
	if maxChars > 8000 {
		maxChars = 8000
	}

	if err := EnsureLayout(root); err != nil {
		return MemoryMDUpdateResponse{}, err
	}
	memAbs, _, err := safeResolveMarkdownPath(root, "MEMORY.md", false)
	if err != nil {
		return MemoryMDUpdateResponse{}, err
	}

	oldRawBytes, err := os.ReadFile(memAbs)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return MemoryMDUpdateResponse{}, err
	}
	oldRaw := strings.TrimSpace(string(oldRawBytes))
	oldRedacted, _ := RedactText(cfg, oldRaw)
	oldRedacted = SanitizeMemoryTextForPrompt(oldRedacted)

	toolBlock := formatToolRecordsForMemoryUpdate(cfg, in.ToolRecords)
	runTitle := strings.TrimSpace(in.RunTitle)
	runTitle, _ = RedactText(cfg, runTitle)
	if runeLen(runTitle) > 200 {
		runTitle = truncateRunes(runTitle, 200) + "…"
	}
	userReq := strings.TrimSpace(in.UserRequest)
	userReq, _ = RedactText(cfg, userReq)
	if runeLen(userReq) > 2400 {
		userReq = truncateRunes(userReq, 2400) + "…"
	}
	assistantFinal := strings.TrimSpace(in.AssistantFinal)
	assistantFinal, _ = RedactText(cfg, assistantFinal)
	if runeLen(assistantFinal) > 2400 {
		assistantFinal = truncateRunes(assistantFinal, 2400) + "…"
	}

	prompt := buildMemoryMDUpdatePrompt(oldRedacted, runID, runTitle, turnNow, userReq, toolBlock, assistantFinal, maxChars)

	updated, err := callMemoryUpdateModel(ctx, client, prompt, maxChars)
	if err != nil {
		return MemoryMDUpdateResponse{}, err
	}

	newText := normalizeMemoryMDOutput(updated)
	newText, _ = RedactText(cfg, newText)
	newText = SanitizeMemoryTextForPrompt(newText)
	newText = ensureMemoryMDUpdateStamp(newText, runID, in.Now)
	if strings.TrimSpace(newText) == "" {
		return MemoryMDUpdateResponse{Path: "MEMORY.md", Updated: false, OldChars: runeLen(oldRaw)}, nil
	}

	resp := MemoryMDUpdateResponse{
		Path:     "MEMORY.md",
		Updated:  false,
		OldChars: runeLen(oldRaw),
		NewChars: runeLen(newText),
	}

	overLimit := runeLen(newText) > maxChars
	if overLimit {
		newText = hardTruncateMemoryMD(newText, maxChars)
	}
	newText = ensureMemoryMDUpdateStamp(newText, runID, in.Now)
	if runeLen(newText) > maxChars {
		newText = hardTruncateMemoryMD(newText, maxChars)
	}
	newText = ensureMemoryMDUpdateStamp(newText, runID, in.Now)
	if runeLen(newText) > maxChars {
		newText = hardTruncateMemoryMD(newText, maxChars)
	}
	resp.NewChars = runeLen(newText)
	resp.Compressed = overLimit

	lockPath := filepath.Join(root, "index", ".memory_md.lock")
	if ctx == nil {
		ctx = context.Background()
	}
	if err := withFileLock(ctx, lockPath, 10*time.Second, func() error {
		// Re-check layout and symlinks under lock.
		if err := EnsureLayout(root); err != nil {
			return err
		}
		memAbs, _, err := safeResolveMarkdownPath(root, "MEMORY.md", false)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(memAbs, []byte(ensureTrailingNewline(newText)), 0o644); err != nil {
			return err
		}
		resp.Updated = true
		return nil
	}); err != nil {
		return MemoryMDUpdateResponse{}, err
	}
	return resp, nil
}

func buildMemoryMDUpdatePrompt(oldMemory string, runID string, runTitle string, now time.Time, userReq string, toolBlock string, assistantFinal string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 1000
	}
	at := now.Format(time.RFC3339)
	atMinute := now.Format("2006-01-02 15:04")
	var b strings.Builder
	b.WriteString("你是一个“长期记忆编辑器”。你要把已有的 MEMORY.md 和本次 session 的增量信息合并，输出更新后的 MEMORY.md。\n\n")
	b.WriteString("目标：让未来的主 Agent 通过这份 MEMORY.md 快速了解用户偏好、当前 TODO、关键决策/约束、近期工作记录摘要。\n")
	b.WriteString(fmt.Sprintf("硬性限制：输出总长度必须 <= %d 个字符（Unicode 字符，含换行）。\n\n", maxChars))
	b.WriteString("硬性规则：\n")
	b.WriteString("- 只输出完整的 MEMORY.md 内容（Markdown），不要额外解释，不要代码块围栏。\n")
	b.WriteString("- 必须以 \"# MEMORY\" 开头。\n")
	b.WriteString("- 严禁写入任何 secrets（API key、token、密码、邮箱授权码、私钥、cookie 等）。输入里如果出现敏感信息，必须丢弃或脱敏。\n")
	b.WriteString("- 允许主动遗忘：删除已完成 TODO、过期细节、重复项、临时寒暄。\n")
	b.WriteString(fmt.Sprintf("- 新增/更新的 bullet（以 \"- \" 开头）尽量带上来源 \"(source=%s)\"；如已知 run_title，也请写上 \"title=\\\"...\\\"\"。\n", runID))
	b.WriteString("- Work Log（工作记录）每条 bullet 尽量以 \"YYYY-MM-DD HH:MM\" 开头（精确到分钟，24 小时制），例如 \"2026-02-25 03:16\"。\n")
	b.WriteString("- 对于本次 turn 的 Work Log 记录，优先使用 at_minute 作为时间；不要为旧记录编造分钟。\n")
	b.WriteString("- 保留旧条目中已有的 source；如果实在无法判断来源，用 (source=unknown)。\n")
	b.WriteString("- 优先保留：用户偏好、未完成 TODO、关键决策/约束、重要路径/命令约定、近期工作摘要。\n\n")
	b.WriteString("建议结构（可按需精简，但尽量保留这些标题）：\n")
	b.WriteString("# MEMORY\n")
	b.WriteString("## Preferences\n")
	b.WriteString("## TODO\n")
	b.WriteString("## Work Log\n")
	b.WriteString("## Notes\n\n")
	b.WriteString("[Existing MEMORY.md]\n")
	if strings.TrimSpace(oldMemory) == "" {
		b.WriteString("(empty)\n")
	} else {
		b.WriteString(oldMemory)
		if !strings.HasSuffix(oldMemory, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n[This Turn]\n")
	b.WriteString("- run_id: " + strings.TrimSpace(runID) + "\n")
	if strings.TrimSpace(runTitle) != "" {
		b.WriteString("- run_title: " + oneLine(runTitle) + "\n")
	}
	b.WriteString("- at: " + at + "\n")
	b.WriteString("- at_minute: " + atMinute + "\n")
	if strings.TrimSpace(userReq) != "" {
		b.WriteString("- user_request: " + oneLine(userReq) + "\n")
	}
	if strings.TrimSpace(toolBlock) != "" {
		b.WriteString("- tool_results:\n")
		for _, line := range strings.Split(toolBlock, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			b.WriteString("  " + line + "\n")
		}
	}
	if strings.TrimSpace(assistantFinal) != "" {
		b.WriteString("- assistant_final: " + oneLine(assistantFinal) + "\n")
	}
	return b.String()
}

func buildMemoryMDCompressPrompt(current string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 1000
	}
	var b strings.Builder
	b.WriteString("请把下面这份 MEMORY.md 进一步压缩，保持关键信息，但必须满足：\n")
	b.WriteString(fmt.Sprintf("- 总长度 <= %d 个字符（Unicode 字符，含换行）\n", maxChars))
	b.WriteString("- 只输出完整的 MEMORY.md 内容（Markdown），不要解释，不要代码块围栏\n")
	b.WriteString("- 必须以 \"# MEMORY\" 开头\n")
	b.WriteString("- 删除重复/已完成/过期信息；不要写 secrets\n\n")
	b.WriteString(current)
	return b.String()
}

func callMemoryUpdateModel(ctx context.Context, client chatClient, prompt string, maxChars int) (string, error) {
	if client == nil {
		return "", errors.New("llm client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	callCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, defaultMemoryMDUpdateModelTimeout)
		defer cancel()
	}

	sys := llm.Message{
		Role: "system",
		Content: strings.Join([]string{
			"You update a compact MEMORY.md file for a coding agent.",
			"Output ONLY the updated MEMORY.md markdown content.",
			"Do not include secrets.",
		}, "\n"),
	}
	user := llm.Message{Role: "user", Content: prompt}
	resp, err := client.Chat(callCtx, llm.ChatRequest{
		Messages:    []llm.Message{sys, user},
		Temperature: 0.2,
		MaxTokens:   900,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("empty llm response")
	}
	out := strings.TrimSpace(resp.Choices[0].Message.Content)
	if out == "" {
		return "", errors.New("empty llm output")
	}
	return out, nil
}

func formatToolRecordsForMemoryUpdate(cfg Config, records []ToolRecord) string {
	if len(records) == 0 {
		return ""
	}
	limit := 10
	if len(records) > limit {
		records = records[len(records)-limit:]
	}
	lines := make([]string, 0, len(records))
	for _, r := range records {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = "tool"
		}
		args := oneLine(r.Arguments)
		args, _ = RedactText(cfg, args)
		if runeLen(args) > 320 {
			args = truncateRunes(args, 320) + "…"
		}

		res := oneLine(r.Result)
		res, _ = RedactText(cfg, res)
		if runeLen(res) > 700 {
			res = truncateRunes(res, 700) + "…"
		}
		errText := oneLine(r.Error)
		errText, _ = RedactText(cfg, errText)
		if runeLen(errText) > 240 {
			errText = truncateRunes(errText, 240) + "…"
		}

		line := fmt.Sprintf("- tool=%s", name)
		if args != "" {
			line += " args=" + args
		}
		if res != "" {
			line += " result=" + res
		}
		if errText != "" {
			line += " error=" + errText
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func normalizeMemoryMDOutput(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	// Strip common code-fence wrappers.
	raw = strings.TrimPrefix(raw, "```markdown")
	raw = strings.TrimPrefix(raw, "```md")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	if !strings.HasPrefix(raw, "#") {
		raw = "# MEMORY\n\n" + raw
	}
	if !strings.HasPrefix(strings.TrimSpace(raw), "# MEMORY") {
		raw = "# MEMORY\n\n" + raw
	}
	return strings.TrimSpace(raw)
}

func hardTruncateMemoryMD(text string, maxChars int) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	if runeLen(raw) <= maxChars {
		return raw
	}
	truncated := truncateRunes(raw, maxChars)
	if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
		truncated = strings.TrimSpace(truncated[:idx])
	}
	if strings.TrimSpace(truncated) == "" {
		truncated = "# MEMORY"
	}
	if !strings.HasPrefix(strings.TrimSpace(truncated), "# MEMORY") {
		truncated = "# MEMORY\n\n" + strings.TrimSpace(truncated)
	}
	return strings.TrimSpace(truncated)
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func ensureMemoryMDUpdateStamp(text string, runID string, now time.Time) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return raw
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		id = "unknown"
	}
	t := now.UTC()
	if t.IsZero() {
		t = time.Now().UTC()
	}
	stamp := fmt.Sprintf("<!-- memory_md_update: at=%s source=%s -->", t.Format(time.RFC3339), id)

	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "<!-- memory_md_update:") {
			lines[i] = stamp
			out := make([]string, 0, len(lines))
			wroteStamp := false
			for _, l := range lines {
				if strings.HasPrefix(strings.TrimSpace(l), "<!-- memory_md_update:") {
					if wroteStamp {
						continue
					}
					wroteStamp = true
				}
				out = append(out, l)
			}
			return strings.TrimSpace(strings.Join(out, "\n"))
		}
	}

	insertAt := 0
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "# MEMORY") {
			insertAt = i + 1
			break
		}
	}
	if insertAt < 0 {
		insertAt = 0
	}
	if insertAt > len(lines) {
		insertAt = len(lines)
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, stamp)
	out = append(out, lines[insertAt:]...)
	return strings.TrimSpace(strings.Join(out, "\n"))
}
