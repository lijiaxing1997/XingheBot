package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
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
	msg := buildTextEmail(from, to, subjectEncoded, body, threadInReplyTo, threadRefs)
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
