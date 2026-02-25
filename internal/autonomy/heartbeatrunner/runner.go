package heartbeatrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"test_skill_agent/internal/autonomy"
	"test_skill_agent/internal/gateway"
	"test_skill_agent/internal/llm"
)

const (
	defaultHeartbeatTimeout    = 2 * time.Minute
	defaultHeartbeatMaxChars   = 12000
	defaultHeartbeatAckMaxChar = 300
)

type Runner struct {
	client      *llm.Client
	configPath  string
	workDir     string
	temperature float32

	initialDelay time.Duration

	statePath string
	runsPath  string

	emailTo      []string
	emailGateway *gateway.EmailGateway

	wakeCh chan struct{}
	doneCh chan struct{}

	wakeMu        sync.Mutex
	pendingWake   bool
	pendingReason string
}

type RunnerOptions struct {
	Client      *llm.Client
	ConfigPath  string
	WorkDir     string
	Temperature float32
}

type Status struct {
	Enabled          bool      `json:"enabled"`
	HeartbeatEnabled bool      `json:"heartbeat_enabled"`
	Every            string    `json:"every"`
	HeartbeatPath    string    `json:"heartbeat_path"`
	OkToken          string    `json:"ok_token"`
	DedupeHours      int       `json:"dedupe_hours"`
	EmailTo          []string  `json:"email_to,omitempty"`
	StatePath        string    `json:"state_path,omitempty"`
	RunsPath         string    `json:"runs_path,omitempty"`
	LastRunAt        time.Time `json:"last_run_at,omitempty"`
	LastSentAt       time.Time `json:"last_sent_at,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	LastReason       string    `json:"last_reason,omitempty"`
}

func Start(ctx context.Context, opts RunnerOptions) (*Runner, error) {
	if opts.Client == nil {
		return nil, errors.New("llm client is nil")
	}
	cfg, err := autonomy.LoadConfig(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	if cfg.Enabled != nil && !*cfg.Enabled {
		return nil, nil
	}

	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == "" {
		cwd, _ := osGetwd()
		workDir = cwd
	}

	every := parseDurationOrDefault(cfg.Heartbeat.Every, 30*time.Minute)
	if every <= 0 {
		every = 30 * time.Minute
	}

	statePath, err := ResolveDefaultStatePath(opts.ConfigPath, workDir)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(statePath) == "" {
		statePath = filepath.Join(workDir, ".multi_agent", "autonomy", "heartbeat_state.json")
	}
	runsPath := filepath.Join(filepath.Dir(statePath), "runs.jsonl")

	emailTo := parseEmailList(cfg.Cron.EmailTo)
	var emailGateway *gateway.EmailGateway
	if gcfg, err := gateway.LoadGatewayConfig(opts.ConfigPath); err == nil {
		if strings.TrimSpace(gcfg.Email.EmailAddress) != "" && strings.TrimSpace(gcfg.Email.AuthorizationCode) != "" {
			if err := gcfg.Email.Validate(); err == nil {
				emailGateway = gateway.NewEmailGateway(gcfg.Email)
			}
		}
	}

	r := &Runner{
		client:       opts.Client,
		configPath:   strings.TrimSpace(opts.ConfigPath),
		workDir:      workDir,
		temperature:  opts.Temperature,
		initialDelay: every,
		statePath:    statePath,
		runsPath:     runsPath,
		emailTo:      emailTo,
		emailGateway: emailGateway,
		wakeCh:       make(chan struct{}, 1),
		doneCh:       make(chan struct{}),
	}
	go r.loop(ctx)
	return r, nil
}

func (r *Runner) Done() <-chan struct{} {
	if r == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return r.doneCh
}

func (r *Runner) Wake(reason string) {
	if r == nil {
		return
	}
	r.wakeMu.Lock()
	r.pendingWake = true
	if reasonPriority(strings.TrimSpace(reason)) >= reasonPriority(r.pendingReason) {
		r.pendingReason = strings.TrimSpace(reason)
	}
	select {
	case r.wakeCh <- struct{}{}:
	default:
	}
	r.wakeMu.Unlock()
}

func (r *Runner) Status() (Status, error) {
	if r == nil {
		return Status{}, errors.New("runner is nil")
	}
	cfg, err := autonomy.LoadConfig(r.configPath)
	if err != nil {
		return Status{}, err
	}
	st, _ := r.readStateLocked()
	hbPath, _ := ResolveHeartbeatFilePath(cfg.Heartbeat.Path, r.workDir)
	out := Status{
		Enabled:          cfg.Enabled == nil || *cfg.Enabled,
		HeartbeatEnabled: cfg.Heartbeat.Enabled != nil && *cfg.Heartbeat.Enabled,
		Every:            strings.TrimSpace(cfg.Heartbeat.Every),
		HeartbeatPath:    hbPath,
		OkToken:          strings.TrimSpace(cfg.Heartbeat.OkToken),
		DedupeHours:      cfg.Heartbeat.DedupeHours,
		EmailTo:          append([]string(nil), r.emailTo...),
		StatePath:        r.statePath,
		RunsPath:         r.runsPath,
		LastRunAt:        st.LastRunAt,
		LastSentAt:       st.LastSentAt,
		LastError:        strings.TrimSpace(st.LastError),
		LastReason:       strings.TrimSpace(st.LastReason),
	}
	return out, nil
}

func (r *Runner) loop(ctx context.Context) {
	defer close(r.doneCh)
	if ctx == nil {
		ctx = context.Background()
	}

	delay := r.initialDelay
	if delay <= 0 {
		delay = 30 * time.Minute
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}

		timer := time.NewTimer(delay)
		woke := false
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-r.wakeCh:
			woke = true
			timer.Stop()
		case <-timer.C:
		}

		reason := "interval"
		if woke {
			reason = r.consumeWakeReason("wake")
		}
		nextDelay, _ := r.tick(ctx, time.Now(), reason)
		delay = nextDelay

		if r.hasPendingWake() {
			delay = 0
		}

		if delay <= 0 {
			delay = 250 * time.Millisecond
		}
	}
}

func (r *Runner) tick(ctx context.Context, now time.Time, reason string) (time.Duration, error) {
	cfg, err := autonomy.LoadConfig(r.configPath)
	if err != nil {
		return 60 * time.Second, err
	}
	if cfg.Enabled != nil && !*cfg.Enabled {
		return 60 * time.Second, nil
	}
	if reason == "interval" && cfg.Heartbeat.Enabled != nil && !*cfg.Heartbeat.Enabled {
		return 60 * time.Second, nil
	}

	every := parseDurationOrDefault(cfg.Heartbeat.Every, 30*time.Minute)
	if every <= 0 {
		every = 30 * time.Minute
	}

	okToken := strings.TrimSpace(cfg.Heartbeat.OkToken)
	if okToken == "" {
		okToken = "HEARTBEAT_OK"
	}

	emailTo := parseEmailList(cfg.Cron.EmailTo)
	if len(emailTo) == 0 {
		emailTo = append([]string(nil), r.emailTo...)
	}
	emailGW := r.emailGateway
	if emailGW == nil {
		if gcfg, err := gateway.LoadGatewayConfig(r.configPath); err == nil {
			if strings.TrimSpace(gcfg.Email.EmailAddress) != "" && strings.TrimSpace(gcfg.Email.AuthorizationCode) != "" {
				if err := gcfg.Email.Validate(); err == nil {
					emailGW = gateway.NewEmailGateway(gcfg.Email)
				}
			}
		}
	}
	if emailGW == nil || len(emailTo) == 0 {
		err := errors.New("heartbeat delivery is not configured (set config.json.gateway.email + autonomy.cron.email_to)")
		_ = r.updateState(reason, time.Now(), err, false, "")
		return every, nil
	}

	coalesce := time.Duration(cfg.Heartbeat.CoalesceMs) * time.Millisecond
	if coalesce < 0 {
		coalesce = 0
	}
	if reason != "interval" && coalesce > 0 {
		select {
		case <-ctx.Done():
			return every, ctx.Err()
		case <-time.After(coalesce):
		}
	}

	if now.IsZero() {
		now = time.Now()
	}

	hbPath, _ := ResolveHeartbeatFilePath(cfg.Heartbeat.Path, r.workDir)
	content, exists, effectivelyEmpty, readErr := ReadHeartbeatFile(hbPath)
	if readErr != nil {
		_ = r.updateState(reason, now, readErr, false, "")
		return every, readErr
	}

	shouldForce := reason != "interval"
	if !shouldForce {
		if !exists || effectivelyEmpty {
			_ = r.updateState(reason, now, nil, false, "")
			return every, nil
		}
	}

	out, runErr := r.runOnce(ctx, now, reason, hbPath, content, okToken)
	if runErr != nil {
		_ = r.updateState(reason, now, runErr, false, "")
		return every, runErr
	}

	cleaned, shouldSkip := stripHeartbeatOK(out, okToken)
	if shouldSkip || strings.TrimSpace(cleaned) == "" {
		_ = r.updateState(reason, now, nil, false, "")
		return every, nil
	}

	dedupeHours := cfg.Heartbeat.DedupeHours
	if dedupeHours <= 0 {
		dedupeHours = 24
	}
	hash := hashHeartbeatText(cleaned)
	if ok, _ := r.shouldSkipDedupe(now, hash, time.Duration(dedupeHours)*time.Hour); ok {
		_ = r.updateState(reason, now, nil, false, "")
		return every, nil
	}

	delivered := false
	if err := r.deliver(emailGW, emailTo, cleaned, now, reason); err != nil {
		_ = r.updateState(reason, now, err, false, "")
		return every, err
	}
	delivered = true

	_ = r.appendRunLog(now, reason, hbPath, delivered, "")
	_ = r.updateState(reason, now, nil, delivered, cleaned)
	return every, nil
}

func (r *Runner) runOnce(ctx context.Context, now time.Time, reason string, hbPath string, heartbeatContent string, okToken string) (string, error) {
	if r == nil || r.client == nil {
		return "", errors.New("runner is not configured")
	}
	if now.IsZero() {
		now = time.Now()
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultHeartbeatTimeout)
	defer cancel()

	loc := time.Local
	localNow := now.In(loc).Format(time.RFC3339)
	utcNow := now.UTC().Format(time.RFC3339)

	trimmedFile := strings.TrimSpace(heartbeatContent)
	if len(trimmedFile) > defaultHeartbeatMaxChars {
		trimmedFile = trimmedFile[:defaultHeartbeatMaxChars] + "…"
	}

	userPrompt := strings.Join([]string{
		"You are running an automated heartbeat turn.",
		"Follow HEARTBEAT.md strictly. Do not invent new tasks. Do not repeat old tasks from prior chats unless HEARTBEAT.md asks you to.",
		"If nothing needs attention, reply exactly: " + okToken,
		"",
		"Reason: " + strings.TrimSpace(reason),
		"Current time (Local): " + localNow,
		"Current time (UTC): " + utcNow,
		"HEARTBEAT.md path: " + strings.TrimSpace(hbPath),
		"",
		"[HEARTBEAT.md]",
		trimmedFile,
		"[/HEARTBEAT.md]",
		"",
		"Output rules:",
		"- If no action items: output only " + okToken,
		"- Otherwise: output concise, actionable bullets (5-15 lines).",
		"- Do not include any secrets.",
	}, "\n")

	resp, err := r.client.Chat(callCtx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: userPrompt},
		},
		Temperature: r.temperature,
	})
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", errors.New("heartbeat: no choices")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func (r *Runner) deliver(gw *gateway.EmailGateway, to []string, text string, now time.Time, reason string) error {
	to = append([]string(nil), to...)
	if len(to) == 0 {
		return errors.New("heartbeat delivery: no recipients configured (set autonomy.cron.email_to)")
	}
	if gw == nil {
		return errors.New("heartbeat delivery: email gateway is not configured (check config.json.gateway.email)")
	}

	loc := time.Local
	subject := fmt.Sprintf("[Heartbeat] %s", now.In(loc).Format("2006-01-02"))
	body := strings.TrimSpace(text)
	if body == "" {
		return nil
	}
	if strings.TrimSpace(reason) != "" && !strings.EqualFold(reason, "interval") {
		body = "Reason: " + strings.TrimSpace(reason) + "\n\n" + body
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, addr := range to {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if err := gw.SendReply(ctx, addr, subject, body, gateway.EmailThreadContext{}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) shouldSkipDedupe(now time.Time, hash string, window time.Duration) (bool, error) {
	if r == nil {
		return false, errors.New("runner is nil")
	}
	if strings.TrimSpace(r.statePath) == "" {
		return false, nil
	}
	lockPath := r.statePath + ".lock"
	var skip bool
	err := withFileLock(lockPath, 5*time.Second, func() error {
		st, err := loadHeartbeatState(r.statePath)
		if err != nil {
			return err
		}
		if strings.TrimSpace(st.LastSentHash) == "" || strings.TrimSpace(hash) == "" {
			skip = false
			return nil
		}
		if !strings.EqualFold(strings.TrimSpace(st.LastSentHash), strings.TrimSpace(hash)) {
			skip = false
			return nil
		}
		if window <= 0 {
			window = 24 * time.Hour
		}
		if st.LastSentAt.IsZero() {
			skip = false
			return nil
		}
		skip = now.Sub(st.LastSentAt) < window
		return nil
	})
	return skip, err
}

func (r *Runner) updateState(reason string, now time.Time, runErr error, delivered bool, deliveredText string) error {
	if r == nil {
		return nil
	}
	if strings.TrimSpace(r.statePath) == "" {
		return nil
	}
	lockPath := r.statePath + ".lock"
	return withFileLock(lockPath, 5*time.Second, func() error {
		st, err := loadHeartbeatState(r.statePath)
		if err != nil {
			return err
		}
		st.LastRunAt = now.UTC()
		st.LastReason = strings.TrimSpace(reason)
		if runErr != nil {
			st.LastError = strings.TrimSpace(runErr.Error())
		} else {
			st.LastError = ""
		}
		if delivered {
			st.LastSentAt = now.UTC()
			st.LastSentHash = hashHeartbeatText(deliveredText)
			st.LastSentPreview = previewHeartbeatText(deliveredText, 800)
		}
		return saveHeartbeatState(r.statePath, st)
	})
}

func (r *Runner) readStateLocked() (heartbeatState, error) {
	if r == nil {
		return heartbeatState{}, errors.New("runner is nil")
	}
	if strings.TrimSpace(r.statePath) == "" {
		return heartbeatState{Version: heartbeatStateVersion}, nil
	}
	lockPath := r.statePath + ".lock"
	var st heartbeatState
	err := withFileLock(lockPath, 2*time.Second, func() error {
		loaded, err := loadHeartbeatState(r.statePath)
		if err != nil {
			return err
		}
		st = loaded
		return nil
	})
	return st, err
}

func (r *Runner) appendRunLog(now time.Time, reason string, hbPath string, delivered bool, errText string) error {
	p := strings.TrimSpace(r.runsPath)
	if p == "" {
		return nil
	}
	lockPath := p + ".lock"
	type runLine struct {
		At        time.Time `json:"at"`
		Reason    string    `json:"reason"`
		Path      string    `json:"heartbeat_path"`
		Delivered bool      `json:"delivered"`
		Error     string    `json:"error,omitempty"`
	}
	line := runLine{
		At:        now.UTC(),
		Reason:    strings.TrimSpace(reason),
		Path:      strings.TrimSpace(hbPath),
		Delivered: delivered,
		Error:     strings.TrimSpace(errText),
	}
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return withFileLock(lockPath, 5*time.Second, func() error {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.Write(append(data, '\n'))
		return err
	})
}

func (r *Runner) consumeWakeReason(fallback string) string {
	r.wakeMu.Lock()
	defer r.wakeMu.Unlock()
	reason := strings.TrimSpace(r.pendingReason)
	r.pendingWake = false
	r.pendingReason = ""
	if reason == "" {
		reason = strings.TrimSpace(fallback)
	}
	return reason
}

func (r *Runner) hasPendingWake() bool {
	r.wakeMu.Lock()
	defer r.wakeMu.Unlock()
	return r.pendingWake
}

func reasonPriority(reason string) int {
	r := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case r == "manual" || r == "run-now" || r == "wake":
		return 3
	case strings.HasPrefix(r, "cron:") || strings.HasPrefix(r, "hook:") || r == "exec-event":
		return 2
	case r == "interval":
		return 1
	default:
		return 1
	}
}

func parseDurationOrDefault(raw string, fallback time.Duration) time.Duration {
	text := strings.TrimSpace(raw)
	if text == "" {
		return fallback
	}
	d, err := time.ParseDuration(text)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func stripHeartbeatOK(raw string, okToken string) (cleaned string, shouldSkip bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", true
	}
	token := strings.TrimSpace(okToken)
	if token == "" {
		token = "HEARTBEAT_OK"
	}

	stripMarkup := func(s string) string {
		out := strings.TrimSpace(s)
		out = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(out, " ")
		out = strings.TrimSpace(out)
		out = strings.Trim(out, "*`~_")
		return strings.TrimSpace(out)
	}

	normalized := stripMarkup(text)
	if normalized == "" {
		return "", true
	}

	isOkOnly := func(s string) bool {
		t := strings.TrimSpace(s)
		if strings.EqualFold(t, token) {
			return true
		}
		re := regexp.MustCompile("^" + regexp.QuoteMeta(token) + `[^\\w]{0,4}$`)
		return re.MatchString(t)
	}

	if isOkOnly(normalized) {
		return "", true
	}

	// If token appears at edges, strip it.
	reEdge := regexp.MustCompile(`(?i)^\s*` + regexp.QuoteMeta(token) + `\s*|` + regexp.QuoteMeta(token) + `[^\\w]{0,4}\s*$`)
	stripped := strings.TrimSpace(reEdge.ReplaceAllString(normalized, ""))
	if stripped == "" {
		return "", true
	}
	if utf8.RuneCountInString(stripped) <= defaultHeartbeatAckMaxChar && strings.Contains(normalized, token) {
		// Treat tiny acknowledgements like "done" as skip when token is present.
		return "", true
	}
	return stripped, false
}

func parseEmailList(raw string) []string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\t', ' ':
			return true
		default:
			return false
		}
	})
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		addr := strings.ToLower(strings.TrimSpace(p))
		if addr == "" {
			continue
		}
		if strings.HasPrefix(addr, "<") && strings.HasSuffix(addr, ">") {
			addr = strings.TrimSuffix(strings.TrimPrefix(addr, "<"), ">")
			addr = strings.ToLower(strings.TrimSpace(addr))
		}
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		out = append(out, addr)
	}
	return out
}

// osGetwd is a var for tests.
var osGetwd = func() (string, error) { return os.Getwd() }
