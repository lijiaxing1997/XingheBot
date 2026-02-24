package slavelog

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

type Kind string

const (
	KindDebug  Kind = "DEBUG"
	KindInfo   Kind = "INFO"
	KindWarn   Kind = "WARN"
	KindError  Kind = "ERROR"
	KindWS     Kind = "WS"
	KindCmd    Kind = "CMD"
	KindTool   Kind = "TOOL"
	KindResult Kind = "RESULT"
	KindWorker Kind = "WORKER"
)

type Logger struct {
	mu sync.Mutex

	file io.Writer
	term io.Writer

	termEnabled bool
	termColor   bool
}

type Options struct {
	File io.Writer
	Term io.Writer

	TermEnabled bool
	TermColor   bool
}

func New(opts Options) *Logger {
	return &Logger{
		file:        opts.File,
		term:        opts.Term,
		termEnabled: opts.TermEnabled,
		termColor:   opts.TermColor,
	}
}

func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.file.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func TermColorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	termEnv := strings.TrimSpace(os.Getenv("TERM"))
	if termEnv == "" || termEnv == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func (l *Logger) Logf(kind Kind, format string, args ...any) {
	if l == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	l.Log(kind, msg)
}

func (l *Logger) Log(kind Kind, msg string) {
	if l == nil {
		return
	}
	text := strings.TrimRight(msg, "\n")
	if strings.TrimSpace(text) == "" {
		return
	}

	ts := time.Now().Format("2006-01-02 15:04:05.000")
	line := fmt.Sprintf("[%s] [%s] %s\n", ts, strings.TrimSpace(string(kind)), text)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		_, _ = io.WriteString(l.file, line)
	}
	if l.termEnabled && l.term != nil {
		if l.termColor {
			_, _ = io.WriteString(l.term, colorize(kind, line))
		} else {
			_, _ = io.WriteString(l.term, line)
		}
	}
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

func colorize(kind Kind, line string) string {
	code := ""
	switch kind {
	case KindDebug:
		code = ansiDim
	case KindInfo:
		code = ansiCyan
	case KindWarn:
		code = ansiYellow
	case KindError:
		code = ansiRed
	case KindWS:
		code = ansiMagenta
	case KindCmd:
		code = ansiBold + ansiGreen
	case KindTool:
		code = ansiBold + ansiCyan
	case KindResult:
		code = ansiBold + ansiYellow
	case KindWorker:
		code = ansiGreen
	default:
		return line
	}
	return code + line + ansiReset
}

func Preview(raw string, max int) string {
	if max <= 0 {
		return ""
	}
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= max {
		return text
	}
	if max < 16 {
		return text[:max]
	}
	return text[:max-14] + " ... (truncated)"
}
