package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
)

type EmailGateway struct {
	cfg EmailConfig
}

type EmailStatus struct {
	OK        bool
	Error     string
	CheckedAt time.Time
}

type EmailThreadContext struct {
	MessageID  string
	InReplyTo  string
	References []string
}

type EmailAttachment struct {
	Path        string
	Name        string
	ContentType string
}

type EmailInbound struct {
	MessageID  string
	InReplyTo  string
	References []string
	UID        uint32
	From       string
	FromName   string
	Subject    string
	Body       string
	Date       time.Time
}

func init() {
	// Decode RFC2047 headers for common charsets (GBK/GB2312/...), including
	// IMAP ENVELOPE decode paths.
	if message.CharsetReader != nil {
		imap.CharsetReader = message.CharsetReader
	}
}

func NewEmailGateway(cfg EmailConfig) *EmailGateway {
	cfg.applyDefaults()
	return &EmailGateway{cfg: cfg}
}

func (g *EmailGateway) Config() EmailConfig {
	if g == nil {
		return EmailConfig{}
	}
	return g.cfg
}

func (g *EmailGateway) Run(ctx context.Context, onStatus func(EmailStatus), onMessage func(EmailInbound)) error {
	if g == nil {
		return errors.New("email gateway is nil")
	}
	if err := g.cfg.Validate(); err != nil {
		return err
	}
	if onMessage == nil {
		return errors.New("onMessage callback is required")
	}
	interval := g.cfg.PollInterval()
	if interval <= 0 {
		interval = 5 * time.Second
	}

	allowedSenders := make(map[string]bool)
	for _, addr := range g.cfg.AllowedSendersList() {
		allowedSenders[strings.ToLower(addr)] = true
	}

	var (
		c              *client.Client
		lastStatusText string
		seenMessageIDs = make(map[string]bool, 2048)
	)
	defer func() {
		if c != nil {
			_ = c.Logout()
		}
	}()

	setStatus := func(ok bool, errText string) {
		now := time.Now().UTC()
		status := EmailStatus{OK: ok, Error: strings.TrimSpace(errText), CheckedAt: now}
		text := fmt.Sprintf("%t|%s", status.OK, status.Error)
		if text == lastStatusText {
			return
		}
		lastStatusText = text
		if onStatus != nil {
			onStatus(status)
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if c == nil {
			conn, err := g.connectIMAP()
			if err != nil {
				setStatus(false, err.Error())
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(minDuration(interval, 15*time.Second)):
					continue
				}
			}
			c = conn
			setStatus(true, "")
		}

		if err := g.pollOnce(ctx, c, allowedSenders, seenMessageIDs, onMessage); err != nil {
			setStatus(false, err.Error())
			_ = c.Logout()
			c = nil
		} else {
			setStatus(true, "")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (g *EmailGateway) connectIMAP() (*client.Client, error) {
	addr := fmt.Sprintf("%s:%d", strings.TrimSpace(g.cfg.IMAP.Server), g.cfg.IMAP.Port)
	var (
		c   *client.Client
		err error
	)
	if g.cfg.IMAP.UseSSL {
		tlsCfg := &tls.Config{ServerName: strings.TrimSpace(g.cfg.IMAP.Server)}
		c, err = client.DialTLS(addr, tlsCfg)
	} else {
		c, err = client.Dial(addr)
	}
	if err != nil {
		return nil, fmt.Errorf("imap dial failed: %w", err)
	}
	c.Timeout = 25 * time.Second

	if err := c.Login(strings.TrimSpace(g.cfg.EmailAddress), strings.TrimSpace(g.cfg.AuthorizationCode)); err != nil {
		_ = c.Logout()
		return nil, fmt.Errorf("imap login failed: %w", err)
	}

	// 126/NetEase may require IMAP ID before SELECT. Best-effort.
	_ = g.sendIMAPID(c)

	if _, err := c.Select("INBOX", false); err != nil {
		_ = c.Logout()
		return nil, fmt.Errorf("imap select INBOX failed: %w", err)
	}
	return c, nil
}

func (g *EmailGateway) sendIMAPID(c *client.Client) error {
	if c == nil {
		return errors.New("nil imap client")
	}
	cmd := &imap.Command{
		Name: "ID",
		Arguments: []interface{}{
			[]interface{}{
				"name", "xinghebot",
				"version", "1.0",
				"vendor", "test_skill_agent",
			},
		},
	}
	_, err := c.Execute(cmd, nil)
	return err
}

func (g *EmailGateway) pollOnce(ctx context.Context, c *client.Client, allowedSenders map[string]bool, seen map[string]bool, onMessage func(EmailInbound)) error {
	if c == nil {
		return errors.New("imap client is nil")
	}
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}

	ids, err := c.Search(criteria)
	if err != nil {
		return fmt.Errorf("imap search UNSEEN failed: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(ids...)

	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid, section.FetchItem()}

	msgCh := make(chan *imap.Message, min(16, len(ids)))
	fetchErrCh := make(chan error, 1)
	go func() {
		fetchErrCh <- c.Fetch(seqset, items, msgCh)
	}()

	for msg := range msgCh {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if msg == nil {
			continue
		}
		inbound, ok := parseInbound(msg, section)
		if !ok {
			continue
		}

		fromLower := strings.ToLower(strings.TrimSpace(inbound.From))
		fromNameLower := strings.ToLower(strings.TrimSpace(inbound.FromName))
		if strings.TrimSpace(inbound.From) == "" {
			continue
		}
		if strings.EqualFold(fromLower, strings.ToLower(strings.TrimSpace(g.cfg.EmailAddress))) {
			// Ignore our own outbound emails if they ever show up in INBOX.
			_ = g.markSeen(c, msg.SeqNum)
			continue
		}
		if len(allowedSenders) > 0 && !allowedSenders[fromLower] && (fromNameLower == "" || !allowedSenders[fromNameLower]) {
			_ = g.markSeen(c, msg.SeqNum)
			continue
		}
		if inbound.MessageID != "" && seen[inbound.MessageID] {
			_ = g.markSeen(c, msg.SeqNum)
			continue
		}

		if inbound.MessageID != "" {
			seen[inbound.MessageID] = true
		}
		onMessage(inbound)
		_ = g.markSeen(c, msg.SeqNum)
	}

	if err := <-fetchErrCh; err != nil {
		return fmt.Errorf("imap fetch failed: %w", err)
	}
	return nil
}

func parseInbound(msg *imap.Message, section *imap.BodySectionName) (EmailInbound, bool) {
	if msg == nil || section == nil {
		return EmailInbound{}, false
	}
	r := msg.GetBody(section)
	if r == nil {
		return EmailInbound{}, false
	}

	raw, err := io.ReadAll(r)
	if err != nil {
		return EmailInbound{}, false
	}

	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		// Fallback: use IMAP envelope for metadata, and raw split for body.
		subject := "(无主题)"
		if msg.Envelope != nil && strings.TrimSpace(msg.Envelope.Subject) != "" {
			subject = strings.TrimSpace(msg.Envelope.Subject)
		}
		fromAddr, fromName := envelopeSender(msg)
		messageID := ""
		inReplyTo := ""
		if msg.Envelope != nil {
			messageID = strings.TrimSpace(msg.Envelope.MessageId)
			inReplyTo = strings.TrimSpace(msg.Envelope.InReplyTo)
		}
		messageID = canonicalMessageID(messageID)
		inReplyTo = parseSingleMessageID(inReplyTo)
		if inReplyTo == "" {
			inReplyTo = parseSingleMessageID(extractHeaderValueFallback(raw, "In-Reply-To"))
		}
		date := time.Time{}
		if msg.Envelope != nil {
			date = msg.Envelope.Date
		}
		refs := parseMessageIDList(extractHeaderValueFallback(raw, "References"))
		body := extractBodyFallback(raw)
		return EmailInbound{
			MessageID:  messageID,
			InReplyTo:  inReplyTo,
			References: refs,
			UID:        msg.Uid,
			From:       fromAddr,
			FromName:   fromName,
			Subject:    subject,
			Body:       body,
			Date:       date,
		}, true
	}

	subject, _ := reader.Header.Subject()
	subject = strings.TrimSpace(subject)
	if subject == "" {
		if msg.Envelope != nil && strings.TrimSpace(msg.Envelope.Subject) != "" {
			subject = strings.TrimSpace(msg.Envelope.Subject)
		} else {
			subject = "(无主题)"
		}
	}

	fromAddr := ""
	fromName := ""
	if list, err := reader.Header.AddressList("From"); err == nil && len(list) > 0 {
		fromAddr = strings.TrimSpace(list[0].Address)
		fromName = strings.TrimSpace(list[0].Name)
	}
	if fromAddr == "" {
		envAddr, envName := envelopeSender(msg)
		if envAddr != "" {
			fromAddr = envAddr
		}
		if fromName == "" && envName != "" {
			fromName = envName
		}
	}

	messageID := strings.TrimSpace(reader.Header.Get("Message-ID"))
	if messageID == "" {
		messageID = strings.TrimSpace(reader.Header.Get("Message-Id"))
	}
	if messageID == "" && msg.Envelope != nil {
		messageID = strings.TrimSpace(msg.Envelope.MessageId)
	}
	messageID = canonicalMessageID(messageID)

	inReplyTo := parseSingleMessageID(reader.Header.Get("In-Reply-To"))
	if inReplyTo == "" && msg.Envelope != nil && strings.TrimSpace(msg.Envelope.InReplyTo) != "" {
		inReplyTo = parseSingleMessageID(msg.Envelope.InReplyTo)
	}

	refs := parseMessageIDList(reader.Header.Get("References"))

	date, _ := reader.Header.Date()
	if date.IsZero() && msg.Envelope != nil && !msg.Envelope.Date.IsZero() {
		date = msg.Envelope.Date
	}

	body := extractTextBody(reader)

	return EmailInbound{
		MessageID:  messageID,
		InReplyTo:  inReplyTo,
		References: refs,
		UID:        msg.Uid,
		From:       fromAddr,
		FromName:   fromName,
		Subject:    subject,
		Body:       body,
		Date:       date,
	}, true
}

func envelopeSender(msg *imap.Message) (addr string, name string) {
	if msg == nil || msg.Envelope == nil {
		return "", ""
	}
	candidates := [][]*imap.Address{
		msg.Envelope.ReplyTo,
		msg.Envelope.From,
		msg.Envelope.Sender,
	}
	for _, list := range candidates {
		if len(list) == 0 || list[0] == nil {
			continue
		}
		a := strings.TrimSpace(list[0].Address())
		n := strings.TrimSpace(list[0].PersonalName)
		if a != "" {
			return a, n
		}
		if n != "" && name == "" {
			name = n
		}
	}
	return "", strings.TrimSpace(name)
}

func extractTextBody(r *mail.Reader) string {
	if r == nil {
		return ""
	}
	var (
		plain string
		html  string
	)
	for {
		part, err := r.NextPart()
		if err != nil {
			break
		}
		switch h := part.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			ct = strings.ToLower(strings.TrimSpace(ct))
			b, _ := io.ReadAll(part.Body)
			// NOTE: go-message automatically decodes transfer-encoding and charset
			// to UTF-8 for text/* entities (when charset support is enabled).
			text := strings.TrimSpace(string(b))
			if text == "" {
				continue
			}
			switch ct {
			case "text/plain":
				if plain == "" {
					plain = text
				}
			case "text/html":
				if html == "" {
					html = text
				}
			default:
				if plain == "" {
					plain = text
				}
			}
		default:
		}
	}
	if plain != "" {
		return plain
	}
	return html
}

func extractBodyFallback(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	text := string(raw)
	idx := strings.Index(text, "\r\n\r\n")
	sepLen := 4
	if idx < 0 {
		idx = strings.Index(text, "\n\n")
		sepLen = 2
	}
	if idx >= 0 && idx+sepLen < len(text) {
		text = text[idx+sepLen:]
	}
	return strings.TrimSpace(text)
}

func (g *EmailGateway) markSeen(c *client.Client, seqNum uint32) error {
	if c == nil || seqNum == 0 {
		return nil
	}
	seqset := new(imap.SeqSet)
	seqset.AddNum(seqNum)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.SeenFlag}
	return c.Store(seqset, item, flags, nil)
}

func (g *EmailGateway) SendReply(ctx context.Context, to string, subject string, body string, thread EmailThreadContext) error {
	return g.SendReplyWithAttachments(ctx, to, subject, body, thread, nil)
}

const (
	maxEmailAttachments      = 5
	maxEmailAttachmentBytes  = 20 << 20
	maxEmailAttachmentsBytes = 25 << 20
)

func (g *EmailGateway) SendReplyWithAttachments(ctx context.Context, to string, subject string, body string, thread EmailThreadContext, attachments []EmailAttachment) error {
	if g == nil {
		return errors.New("email gateway is nil")
	}
	if strings.TrimSpace(to) == "" {
		return errors.New("to is required")
	}
	if strings.TrimSpace(subject) == "" {
		subject = "Re: (no subject)"
	}
	from := strings.TrimSpace(g.cfg.EmailAddress)
	if from == "" {
		return errors.New("email_address is required")
	}

	addr := fmt.Sprintf("%s:%d", strings.TrimSpace(g.cfg.SMTP.Server), g.cfg.SMTP.Port)
	dialer := &net.Dialer{Timeout: 15 * time.Second}

	var conn net.Conn
	var err error
	if g.cfg.SMTP.UseSSL {
		tlsCfg := &tls.Config{ServerName: strings.TrimSpace(g.cfg.SMTP.Server)}
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial failed: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, strings.TrimSpace(g.cfg.SMTP.Server))
	if err != nil {
		return fmt.Errorf("smtp client failed: %w", err)
	}
	defer func() { _ = c.Quit() }()

	auth := smtp.PlainAuth("", from, strings.TrimSpace(g.cfg.AuthorizationCode), strings.TrimSpace(g.cfg.SMTP.Server))
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth failed: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL FROM failed: %w", err)
	}
	if err := c.Rcpt(strings.TrimSpace(to)); err != nil {
		return fmt.Errorf("smtp RCPT TO failed: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA failed: %w", err)
	}

	subjectEncoded := mime.QEncoding.Encode("utf-8", subject)
	threadInReplyTo := canonicalMessageID(thread.MessageID)
	baseRefs := append([]string(nil), thread.References...)
	if len(baseRefs) == 0 && strings.TrimSpace(thread.InReplyTo) != "" {
		baseRefs = append(baseRefs, canonicalMessageID(thread.InReplyTo))
	}
	threadRefs := buildReferencesForReply(baseRefs, threadInReplyTo)

	attachments = normalizeEmailAttachments(attachments)
	msg := ""
	if len(attachments) == 0 {
		msg = buildTextEmail(from, to, subjectEncoded, body, threadInReplyTo, threadRefs)
	} else {
		encoded, err := buildMixedEmail(from, to, subjectEncoded, body, threadInReplyTo, threadRefs, attachments)
		if err != nil {
			return err
		}
		msg = encoded
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("smtp write failed: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close failed: %w", err)
	}
	return nil
}

func buildTextEmail(from string, to string, subject string, body string, inReplyTo string, references []string) string {
	if strings.TrimSpace(body) == "" {
		body = "(empty)"
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")

	headers := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + subject,
	}
	if v := formatMessageID(inReplyTo); v != "" {
		headers = append(headers, "In-Reply-To: "+v)
	}
	if refs := formatReferencesHeaderValue(references); refs != "" {
		headers = append(headers, "References: "+refs)
	}
	headers = append(headers,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"Date: "+time.Now().Format(time.RFC1123Z),
	)
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body + "\r\n"
}

func normalizeEmailAttachments(list []EmailAttachment) []EmailAttachment {
	out := make([]EmailAttachment, 0, len(list))
	seen := make(map[string]bool, len(list))
	var total int64
	for _, att := range list {
		p := strings.TrimSpace(att.Path)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() <= 0 {
			continue
		}
		if len(out) >= maxEmailAttachments {
			break
		}
		if info.Size() > maxEmailAttachmentBytes {
			continue
		}
		if total+info.Size() > maxEmailAttachmentsBytes {
			break
		}
		total += info.Size()

		name := strings.TrimSpace(att.Name)
		if name == "" {
			name = filepath.Base(p)
		}
		ct := strings.TrimSpace(att.ContentType)
		if ct == "" {
			ct = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		}
		if strings.TrimSpace(ct) == "" {
			ct = "application/octet-stream"
		}
		out = append(out, EmailAttachment{
			Path:        p,
			Name:        name,
			ContentType: ct,
		})
	}
	return out
}

func buildMixedEmail(from string, to string, subject string, body string, inReplyTo string, references []string, attachments []EmailAttachment) (string, error) {
	if strings.TrimSpace(body) == "" {
		body = "(empty)"
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")

	boundary := randomBoundary("mix")

	headers := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + subject,
	}
	if v := formatMessageID(inReplyTo); v != "" {
		headers = append(headers, "In-Reply-To: "+v)
	}
	if refs := formatReferencesHeaderValue(references); refs != "" {
		headers = append(headers, "References: "+refs)
	}
	headers = append(headers,
		"MIME-Version: 1.0",
		fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"", boundary),
		"Date: "+time.Now().Format(time.RFC1123Z),
	)

	var b strings.Builder
	b.Grow(2048)
	b.WriteString(strings.Join(headers, "\r\n"))
	b.WriteString("\r\n\r\n")

	// Body part
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")

	for _, att := range attachments {
		data, err := os.ReadFile(att.Path)
		if err != nil {
			return "", fmt.Errorf("read attachment %s: %w", strings.TrimSpace(att.Path), err)
		}
		filename := strings.TrimSpace(att.Name)
		if filename == "" {
			filename = filepath.Base(att.Path)
		}
		ct := strings.TrimSpace(att.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		ascii, escaped := encodeFilenameParams(filename)

		b.WriteString("--" + boundary + "\r\n")
		b.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"; name*=UTF-8''%s\r\n", ct, ascii, escaped))
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"; filename*=UTF-8''%s\r\n\r\n", ascii, escaped))
		b.WriteString(encodeBase64MIME(data))
	}

	b.WriteString("--" + boundary + "--\r\n")
	return b.String(), nil
}

func encodeBase64MIME(data []byte) string {
	if len(data) == 0 {
		return "\r\n"
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	b.Grow(len(encoded) + (len(encoded)/76+2)*2)
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		b.WriteString(encoded[i:end])
		b.WriteString("\r\n")
	}
	return b.String()
}

func encodeFilenameParams(filename string) (ascii string, escaped string) {
	name := strings.TrimSpace(filename)
	if name == "" {
		name = "file"
	}
	escaped = url.PathEscape(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	ascii = strings.Trim(b.String(), "._-")
	if ascii == "" {
		ascii = "file"
	}
	return ascii, escaped
}

func randomBoundary(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])
	p := strings.TrimSpace(prefix)
	if p == "" {
		return suffix
	}
	return p + "-" + suffix
}

var messageIDAngleRe = regexp.MustCompile(`<([^>]+)>`)

func canonicalMessageID(messageID string) string {
	return strings.Trim(strings.TrimSpace(messageID), "<>")
}

func formatMessageID(messageID string) string {
	id := canonicalMessageID(messageID)
	if id == "" {
		return ""
	}
	return "<" + id + ">"
}

func parseSingleMessageID(headerValue string) string {
	list := parseMessageIDList(headerValue)
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

func parseMessageIDList(headerValue string) []string {
	v := strings.TrimSpace(headerValue)
	if v == "" {
		return nil
	}
	v = strings.ReplaceAll(v, "\r", " ")
	v = strings.ReplaceAll(v, "\n", " ")

	var out []string
	seen := make(map[string]bool)

	matches := messageIDAngleRe.FindAllStringSubmatch(v, -1)
	if len(matches) > 0 {
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			id := canonicalMessageID(m[1])
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
		return out
	}

	// Fallback: split on whitespace and trim common delimiters.
	for _, tok := range strings.Fields(v) {
		id := strings.TrimSpace(tok)
		id = strings.Trim(id, "<>,;")
		id = canonicalMessageID(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func buildReferencesForReply(inboundRefs []string, inboundMessageID string) []string {
	ids := make([]string, 0, len(inboundRefs)+1)
	ids = append(ids, inboundRefs...)
	if mid := canonicalMessageID(inboundMessageID); mid != "" {
		ids = append(ids, mid)
	}

	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = canonicalMessageID(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func formatReferencesHeaderValue(refs []string) string {
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, id := range refs {
		if v := formatMessageID(id); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

func extractHeaderValueFallback(raw []byte, name string) string {
	if len(raw) == 0 {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return ""
	}

	text := string(raw)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if idx := strings.Index(text, "\n\n"); idx >= 0 {
		text = text[:idx]
	}
	lines := strings.Split(text, "\n")

	var valueParts []string
	capturing := false
	for _, line := range lines {
		if capturing {
			if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				valueParts = append(valueParts, strings.TrimSpace(line))
				continue
			}
			break
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(line[:colon]))
		if k != key {
			continue
		}
		capturing = true
		valueParts = append(valueParts, strings.TrimSpace(line[colon+1:]))
	}
	return strings.TrimSpace(strings.Join(valueParts, " "))
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}
