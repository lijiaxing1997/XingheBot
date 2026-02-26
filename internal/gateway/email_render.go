package gateway

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"sync"
	"time"

	"test_skill_agent/internal/appinfo"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed email_template.html
var emailTemplateFS embed.FS

type emailTemplateData struct {
	AppDisplay string
	Title      string
	Preheader  string
	Body       template.HTML
	Footer     string
}

var (
	emailTemplateOnce sync.Once
	emailTemplate     *template.Template
	emailTemplateErr  error
)

func getEmailTemplate() (*template.Template, error) {
	emailTemplateOnce.Do(func() {
		b, err := emailTemplateFS.ReadFile("email_template.html")
		if err != nil {
			emailTemplateErr = err
			return
		}
		emailTemplate, emailTemplateErr = template.New("email_template.html").Parse(string(b))
	})
	return emailTemplate, emailTemplateErr
}

var emailMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Linkify),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(html.WithHardWraps(), html.WithXHTML()),
)

var emailMarkdownMu sync.Mutex

func renderEmailHTML(subject string, markdownBody string) (string, error) {
	body := strings.TrimSpace(markdownBody)
	if body == "" {
		body = "(empty)"
	}

	var content bytes.Buffer
	emailMarkdownMu.Lock()
	err := emailMarkdown.Convert([]byte(body), &content)
	emailMarkdownMu.Unlock()
	if err != nil {
		escaped := template.HTMLEscapeString(body)
		content.Reset()
		content.WriteString("<pre>")
		content.WriteString(escaped)
		content.WriteString("</pre>")
	}

	data := emailTemplateData{
		AppDisplay: appinfo.Display(),
		Title:      strings.TrimSpace(subject),
		Preheader:  buildEmailPreheader(body),
		Body:       template.HTML(content.String()),
		Footer:     fmt.Sprintf("%s • %s", appinfo.Name, time.Now().UTC().Format(time.RFC3339)),
	}

	tmpl, err := getEmailTemplate()
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

func buildEmailPreheader(body string) string {
	s := strings.TrimSpace(body)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	const max = 160
	if len(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return strings.TrimSpace(s[:i]) + "…"
		}
		n++
	}
	return s
}
