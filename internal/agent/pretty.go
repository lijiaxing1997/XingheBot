package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
