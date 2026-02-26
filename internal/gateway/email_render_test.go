package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderEmailHTML_Markdown(t *testing.T) {
	html, err := renderEmailHTML("Subject", "# Hello\n\n- a\n- b\n\n`code`")
	if err != nil {
		t.Fatalf("renderEmailHTML error: %v", err)
	}
	if !strings.Contains(html, "<!doctype html>") {
		t.Fatalf("expected doctype in rendered html")
	}
	if !strings.Contains(html, "<h1") || !strings.Contains(html, "Hello") {
		t.Fatalf("expected markdown heading in rendered html")
	}
	if !strings.Contains(html, "<ul>") || !strings.Contains(html, "<li>") {
		t.Fatalf("expected markdown list in rendered html")
	}
	if !strings.Contains(html, "<code>code</code>") {
		t.Fatalf("expected inline code in rendered html")
	}
}

func TestBuildAlternativeEmail_MultipartAlternative(t *testing.T) {
	msg := buildAlternativeEmail("from@example.com", "to@example.com", "Subject", "plain body", "<p>html body</p>", "", nil)
	if !strings.Contains(msg, "Content-Type: multipart/alternative") {
		t.Fatalf("expected multipart/alternative content-type")
	}
	if !strings.Contains(msg, "Content-Type: text/plain") || !strings.Contains(msg, "plain body") {
		t.Fatalf("expected text/plain part")
	}
	if !strings.Contains(msg, "Content-Type: text/html") || !strings.Contains(msg, "<p>html body</p>") {
		t.Fatalf("expected text/html part")
	}
}

func TestBuildMixedAlternativeEmail_Attachment(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write temp attachment: %v", err)
	}

	msg, err := buildMixedAlternativeEmail(
		"from@example.com",
		"to@example.com",
		"Subject",
		"plain body",
		"<p>html body</p>",
		"",
		nil,
		[]EmailAttachment{{Path: p, Name: "a.txt", ContentType: "text/plain"}},
	)
	if err != nil {
		t.Fatalf("buildMixedAlternativeEmail error: %v", err)
	}
	if !strings.Contains(msg, "Content-Type: multipart/mixed") {
		t.Fatalf("expected multipart/mixed content-type")
	}
	if !strings.Contains(msg, "Content-Type: multipart/alternative") {
		t.Fatalf("expected multipart/alternative nested part")
	}
	if !strings.Contains(msg, "Content-Disposition: attachment") || !strings.Contains(msg, "a.txt") {
		t.Fatalf("expected attachment headers")
	}
	if !strings.Contains(msg, "aGVsbG8=") {
		t.Fatalf("expected base64 payload for attachment content")
	}
}
