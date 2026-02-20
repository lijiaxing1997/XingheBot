package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type toolPrinter struct {
	out   io.Writer
	color bool
}

func newToolPrinter(out io.Writer) toolPrinter {
	return toolPrinter{out: out, color: colorEnabled()}
}

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := strings.TrimSpace(os.Getenv("TERM"))
	if term == "" || term == "dumb" {
		return false
	}
	return true
}

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiCyan    = "\x1b[36m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiRed     = "\x1b[31m"
	ansiMagenta = "\x1b[35m"
)

func (p toolPrinter) wrap(code, text string) string {
	if !p.color || code == "" {
		return text
	}
	return code + text + ansiReset
}

func (p toolPrinter) header(kind, name, color string) {
	if p.out == nil {
		return
	}
	label := fmt.Sprintf("[%s]", kind)
	fmt.Fprintf(p.out, "%s %s\n", p.wrap(ansiBold+color, label), p.wrap(ansiBold, name))
}

func (p toolPrinter) line(label, value, labelColor, valueColor string) {
	if p.out == nil || strings.TrimSpace(value) == "" {
		return
	}
	labelText := label + ":"
	fmt.Fprintf(p.out, "  %s %s\n", p.wrap(labelColor, labelText), p.wrap(valueColor, value))
}

func (p toolPrinter) block(label, content string) {
	if p.out == nil || strings.TrimSpace(content) == "" {
		return
	}
	fmt.Fprintf(p.out, "  %s\n", p.wrap(ansiDim, label+":"))
	for _, line := range strings.Split(content, "\n") {
		fmt.Fprintf(p.out, "    %s\n", line)
	}
}

func (p toolPrinter) printToolCall(name, args string) {
	p.header("TOOL", name, ansiCyan)
	if server, tool, ok := splitMCPToolName(name); ok {
		p.line("mcp_server", server, ansiDim, ansiMagenta)
		p.line("mcp_tool", tool, ansiDim, ansiMagenta)
	}
	trimmed := strings.TrimSpace(args)
	if trimmed != "" {
		p.line("args_bytes", fmt.Sprintf("%d", len(trimmed)), ansiDim, ansiDim)
		valid := json.Valid([]byte(trimmed))
		p.line("args_valid_json", fmt.Sprintf("%t", valid), ansiDim, ansiDim)
		if valid {
			if keys := topLevelJSONKeys(trimmed); keys != "" {
				p.line("arg_keys", keys, ansiDim, ansiDim)
			}
		}
		if !valid {
			switch {
			case strings.HasPrefix(trimmed, "#"):
				p.line("args_hint", "looks like raw file content (missing JSON wrapper {...})", ansiDim, ansiRed)
			case strings.HasPrefix(trimmed, "{"):
				p.line("args_hint", "looks like JSON but is invalid (often truncated by max_tokens)", ansiDim, ansiRed)
			}
		}
	}
	pretty := formatJSON(args)
	p.block("args", pretty)
	if name == "skill_install" {
		summary := formatSkillInstallSummary(args)
		if summary != "" {
			p.line("install", summary, ansiMagenta, "")
		}
	}
}

func (p toolPrinter) printToolResult(name, result string, err error, duration time.Duration) {
	status := "ok"
	statusColor := ansiGreen
	if err != nil {
		status = "error"
		statusColor = ansiRed
	}
	p.header("RESULT", name, ansiYellow)
	p.line("status", status, ansiDim, statusColor)
	if duration > 0 {
		p.line("time", duration.Truncate(time.Millisecond).String(), ansiDim, ansiDim)
	}
	if err != nil {
		p.line("error", err.Error(), ansiDim, ansiRed)
	}

	server, tool, isMCPTool := splitMCPToolName(name)
	if isMCPTool {
		p.line("mcp_server", server, ansiDim, ansiMagenta)
		p.line("mcp_tool", tool, ansiDim, ansiMagenta)
	}

	if parsed, ok := parseMCPResult(result); ok {
		if parsed.IsError != nil {
			mcpErrColor := ansiGreen
			if *parsed.IsError {
				mcpErrColor = ansiRed
			}
			p.line("mcp_is_error", fmt.Sprintf("%t", *parsed.IsError), ansiDim, mcpErrColor)
		}
		if parsed.ContentItems > 0 {
			p.line("mcp_content_items", fmt.Sprintf("%d", parsed.ContentItems), ansiDim, ansiDim)
		}
		if parsed.Text != "" {
			p.block("mcp_text", parsed.Text)
		}
		if parsed.Structured != "" {
			p.block("mcp_structured", parsed.Structured)
		}
		if parsed.RawJSON != "" {
			p.block("mcp_output_json", parsed.RawJSON)
		}
		fmt.Fprintln(p.out)
		return
	}

	if isMCPTool {
		trimmed := strings.TrimRight(result, "\n")
		if strings.TrimSpace(trimmed) == "" {
			p.line("mcp_output", "(empty)", ansiDim, ansiDim)
			fmt.Fprintln(p.out)
			return
		}
		p.block("mcp_output", trimmed)
		fmt.Fprintln(p.out)
		return
	}

	trimmed := strings.TrimRight(result, "\n")
	if strings.TrimSpace(trimmed) == "" {
		p.line("output", "(empty)", ansiDim, ansiDim)
		fmt.Fprintln(p.out)
		return
	}
	p.block("output", trimmed)
	fmt.Fprintln(p.out)
}

func formatJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var out bytes.Buffer
	if json.Valid([]byte(trimmed)) {
		if err := json.Indent(&out, []byte(trimmed), "", "  "); err == nil {
			return out.String()
		}
	}
	return trimmed
}

type skillInstallArgs struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Repo   string `json:"repo"`
	Ref    string `json:"ref"`
	Name   string `json:"name"`
	Dir    string `json:"dir"`
}

func formatSkillInstallSummary(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var in skillInstallArgs
	if err := json.Unmarshal([]byte(trimmed), &in); err != nil {
		return ""
	}
	source := strings.ToLower(strings.TrimSpace(in.Source))
	if source == "" {
		if strings.TrimSpace(in.Repo) != "" {
			source = "github"
		} else {
			source = "local"
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		if strings.TrimSpace(in.Path) != "" {
			name = filepath.Base(in.Path)
		} else if strings.TrimSpace(in.Repo) != "" {
			parts := strings.Split(in.Repo, "/")
			name = parts[len(parts)-1]
		}
	}
	switch source {
	case "github":
		ref := strings.TrimSpace(in.Ref)
		if ref == "" {
			ref = "main"
		}
		if name != "" {
			return fmt.Sprintf("source=github repo=%s ref=%s path=%s name=%s", in.Repo, ref, in.Path, name)
		}
		return fmt.Sprintf("source=github repo=%s ref=%s path=%s", in.Repo, ref, in.Path)
	case "local":
		if name != "" {
			return fmt.Sprintf("source=local path=%s name=%s", in.Path, name)
		}
		return fmt.Sprintf("source=local path=%s", in.Path)
	default:
		if name != "" {
			return fmt.Sprintf("source=%s name=%s", source, name)
		}
		return fmt.Sprintf("source=%s", source)
	}
}

func splitMCPToolName(name string) (server string, tool string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(name), "__", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	server = strings.TrimSpace(parts[0])
	tool = strings.TrimSpace(parts[1])
	if server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}

func topLevelJSONKeys(raw string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ""
	}
	if len(obj) == 0 {
		return ""
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

type mcpResultInfo struct {
	IsError      *bool
	ContentItems int
	Text         string
	Structured   string
	RawJSON      string
}

func parseMCPResult(raw string) (mcpResultInfo, bool) {
	var out mcpResultInfo
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return out, false
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return out, false
	}
	if len(obj) == 0 {
		return out, false
	}

	content, hasContent := obj["content"]
	structured, hasStructured := obj["structuredContent"]
	if !hasStructured {
		structured, hasStructured = obj["structured_content"]
	}
	isErrorRaw, hasIsError := obj["isError"]
	if !hasIsError {
		isErrorRaw, hasIsError = obj["is_error"]
	}
	if !hasContent && !hasStructured && !hasIsError {
		return out, false
	}

	if hasIsError {
		if v, ok := toBool(isErrorRaw); ok {
			out.IsError = &v
		}
	}

	if items, ok := content.([]any); ok {
		out.ContentItems = len(items)
		textParts := make([]string, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := entry["type"].(string)
			if typ != "text" {
				continue
			}
			text, _ := entry["text"].(string)
			text = strings.TrimSpace(text)
			if text != "" {
				textParts = append(textParts, text)
			}
		}
		out.Text = strings.Join(textParts, "\n")
	}

	if hasStructured && structured != nil {
		out.Structured = toPrettyJSON(structured)
	}

	if out.Text == "" && out.Structured == "" {
		out.RawJSON = formatJSON(trimmed)
	}

	return out, true
}

func toBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func toPrettyJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}
