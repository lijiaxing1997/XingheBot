package cronrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"test_skill_agent/internal/agent"
	"test_skill_agent/internal/autonomy"
	"test_skill_agent/internal/autonomy/cron"
	"test_skill_agent/internal/gateway"
	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/tools"
)

type Runner struct {
	jobsPath string
	runsDir  string
	store    *cron.StoreManager

	defaultTimezone    string
	maxTimerDelay      time.Duration
	defaultTimeout     time.Duration
	stuckRun           time.Duration
	minRefireGap       time.Duration
	emailTo            []string
	emailSubjectPrefix string

	cronAgent    *agent.Agent
	emailGateway *gateway.EmailGateway

	wakeCh chan struct{}
	doneCh chan struct{}

	wakeMu sync.Mutex
}

type RunnerOptions struct {
	Client      *llm.Client
	ConfigPath  string
	SkillsDir   string
	WorkDir     string
	Temperature float32
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
	if cfg.Cron.Enabled != nil && !*cfg.Cron.Enabled {
		return nil, nil
	}

	jobsPath := strings.TrimSpace(cfg.Cron.StorePath)
	if jobsPath == "" {
		defaultPath, err := cron.ResolveDefaultJobsPath(opts.ConfigPath, opts.WorkDir)
		if err != nil {
			return nil, err
		}
		jobsPath = strings.TrimSpace(defaultPath)
	}
	if jobsPath == "" {
		return nil, errors.New("cron jobs store path is empty")
	}
	runsDir := filepath.Join(filepath.Dir(jobsPath), "runs")

	maxTimerDelay := parseDurationOrDefault(cfg.Cron.MaxTimerDelay, 60*time.Second)
	defaultTimeout := parseDurationOrDefault(cfg.Cron.DefaultTimeout, 10*time.Minute)
	stuckRun := parseDurationOrDefault(cfg.Cron.StuckRun, 2*time.Hour)
	minRefireGap := parseDurationOrDefault(cfg.Cron.MinRefireGap, 2*time.Second)
	if minRefireGap < 0 {
		minRefireGap = 2 * time.Second
	}

	recipientList := parseEmailList(cfg.Cron.EmailTo)
	subjectPrefix := strings.TrimSpace(cfg.Cron.EmailSubjectPrefix)
	if subjectPrefix == "" {
		subjectPrefix = "[Cron]"
	}

	toolReg := tools.NewRegistry()
	toolReg.Register(&tools.TavilySearchTool{ConfigPath: opts.ConfigPath})
	toolReg.Register(&tools.TavilyExtractTool{ConfigPath: opts.ConfigPath})
	toolReg.Register(&tools.TavilyCrawlTool{ConfigPath: opts.ConfigPath})
	cronAgent, err := agent.New(opts.Client, toolReg, opts.SkillsDir)
	if err != nil {
		return nil, err
	}
	cronAgent.ConfigPath = strings.TrimSpace(opts.ConfigPath)
	cronAgent.SetPromptMode(agent.PromptModeWorker)
	cronAgent.Temperature = opts.Temperature

	var emailGateway *gateway.EmailGateway
	if gcfg, err := gateway.LoadGatewayConfig(opts.ConfigPath); err == nil {
		if strings.TrimSpace(gcfg.Email.EmailAddress) != "" && strings.TrimSpace(gcfg.Email.AuthorizationCode) != "" {
			if err := gcfg.Email.Validate(); err == nil {
				emailGateway = gateway.NewEmailGateway(gcfg.Email)
			}
		}
	}

	r := &Runner{
		jobsPath:           jobsPath,
		runsDir:            runsDir,
		store:              cron.NewStoreManager(jobsPath),
		defaultTimezone:    strings.TrimSpace(cfg.Cron.DefaultTimezone),
		maxTimerDelay:      maxTimerDelay,
		defaultTimeout:     defaultTimeout,
		stuckRun:           stuckRun,
		minRefireGap:       minRefireGap,
		emailTo:            recipientList,
		emailSubjectPrefix: subjectPrefix,
		cronAgent:          cronAgent,
		emailGateway:       emailGateway,
		wakeCh:             make(chan struct{}, 1),
		doneCh:             make(chan struct{}),
	}
	go r.loop(ctx)
	return r, nil
}

func (r *Runner) JobsPath() string {
	if r == nil {
		return ""
	}
	return r.jobsPath
}

func (r *Runner) Done() <-chan struct{} {
	if r == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return r.doneCh
}

func (r *Runner) Wake() {
	if r == nil {
		return
	}
	r.wakeMu.Lock()
	select {
	case r.wakeCh <- struct{}{}:
	default:
	}
	r.wakeMu.Unlock()
}

func (r *Runner) loop(ctx context.Context) {
	defer close(r.doneCh)

	delay := 0 * time.Second
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if delay < 0 {
			delay = 0
		}
		if delay == 0 {
			delay = 250 * time.Millisecond
		}
		if delay > r.maxTimerDelay {
			delay = r.maxTimerDelay
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-r.wakeCh:
			timer.Stop()
		case <-timer.C:
		}

		nextDelay, _ := r.tick(ctx, time.Now().UTC())
		delay = nextDelay
	}
}

func (r *Runner) tick(ctx context.Context, now time.Time) (time.Duration, error) {
	if r == nil || r.store == nil {
		return 0, errors.New("runner is not configured")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	due, next, err := r.store.ClaimDueJobs(now, cron.ClaimOptions{
		DefaultTimezone: r.defaultTimezone,
		StuckRun:        r.stuckRun,
		MinRefireGap:    r.minRefireGap,
	})
	if err != nil {
		return r.maxTimerDelay, err
	}

	for _, job := range due {
		r.runJob(ctx, job, now)
	}

	if next.IsZero() {
		return r.maxTimerDelay, nil
	}
	delay := next.Sub(now)
	if delay < 0 {
		delay = 0
	}
	if delay > r.maxTimerDelay {
		delay = r.maxTimerDelay
	}
	return delay, nil
}

func (r *Runner) runJob(ctx context.Context, job cron.Job, claimedAt time.Time) {
	startedAt := claimedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}

	output := ""
	runErr := error(nil)

	timeout := r.defaultTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch strings.ToLower(strings.TrimSpace(job.Task.Type)) {
	case "", "llm":
		output, runErr = r.execLLMTask(taskCtx, job, startedAt)
	default:
		runErr = fmt.Errorf("unsupported task.type: %s", strings.TrimSpace(job.Task.Type))
	}

	finishedAt := time.Now().UTC()

	outputFile := ""
	if strings.TrimSpace(output) != "" {
		runDir := filepath.Join(r.runsDir, safeName(job.ID))
		if err := os.MkdirAll(runDir, 0o755); err == nil {
			name := startedAt.UTC().Format("20060102-150405.000000000Z") + ".md"
			path := filepath.Join(runDir, name)
			if err := os.WriteFile(path, []byte(output), 0o644); err == nil {
				if rel, err := filepath.Rel(r.runsDir, path); err == nil {
					outputFile = filepath.ToSlash(rel)
				}
			}
		}
	}

	delivered := false
	deliveryErr := ""
	if runErr == nil {
		subject := r.defaultSubject(job, startedAt)
		if strings.TrimSpace(job.Delivery.Subject) != "" {
			subject = strings.TrimSpace(job.Delivery.Subject)
		}
		if err := r.deliverEmail(taskCtx, job, subject, output); err != nil {
			deliveryErr = err.Error()
			runErr = fmt.Errorf("delivery failed: %w", err)
		} else {
			delivered = true
		}
	}

	status := "ok"
	errText := ""
	if runErr != nil {
		status = "error"
		errText = runErr.Error()
	}
	preview := strings.TrimSpace(output)
	const maxPreview = 800
	if len(preview) > maxPreview {
		preview = preview[:maxPreview] + "…"
	}
	_ = cron.AppendRunRecord(filepath.Join(r.runsDir, safeName(job.ID)+".jsonl"), cron.RunRecord{
		JobID:         strings.TrimSpace(job.ID),
		StartedAt:     startedAt,
		FinishedAt:    finishedAt,
		Status:        status,
		Error:         errText,
		Delivered:     delivered,
		DeliveryErr:   strings.TrimSpace(deliveryErr),
		OutputPreview: preview,
		OutputFile:    strings.TrimSpace(outputFile),
	})

	_ = r.store.FinishJob(job.ID, claimedAt, finishedAt, runErr, cron.FinishOptions{
		DefaultTimezone: r.defaultTimezone,
		MinRefireGap:    r.minRefireGap,
	})
	r.Wake()
}

func (r *Runner) execLLMTask(ctx context.Context, job cron.Job, now time.Time) (string, error) {
	if r == nil || r.cronAgent == nil {
		return "", errors.New("cron agent is not configured")
	}
	maxTurns := job.Task.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 20
	}
	tz := strings.TrimSpace(job.Schedule.Timezone)
	if tz == "" {
		tz = strings.TrimSpace(r.defaultTimezone)
	}
	loc, _ := cronLocation(tz)
	if loc == nil {
		loc = time.Local
	}

	task := strings.TrimSpace(job.Task.Prompt)
	header := strings.Join([]string{
		"You are running an automated scheduled task.",
		fmt.Sprintf("Job: id=%s name=%s", strings.TrimSpace(job.ID), strings.TrimSpace(job.Name)),
		"Current time (Local): " + now.In(loc).Format(time.RFC3339),
		"Current time (UTC): " + now.UTC().Format(time.RFC3339),
		"",
	}, "\n")
	return r.cronAgent.RunTask(ctx, header+task, agent.TaskOptions{MaxTurns: maxTurns})
}

func (r *Runner) deliverEmail(ctx context.Context, job cron.Job, subject string, body string) error {
	if r == nil || r.emailGateway == nil {
		return errors.New("email gateway is not configured (check config.json.gateway.email)")
	}
	to := append([]string(nil), job.Delivery.To...)
	if len(to) == 0 {
		to = append(to, r.emailTo...)
	}
	if len(to) == 0 {
		return errors.New("no recipients configured (set autonomy.cron.email_to or job.delivery.to)")
	}
	for _, addr := range to {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if err := r.emailGateway.SendReply(ctx, addr, subject, body, gateway.EmailThreadContext{}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) defaultSubject(job cron.Job, now time.Time) string {
	tz := strings.TrimSpace(job.Schedule.Timezone)
	if tz == "" {
		tz = strings.TrimSpace(r.defaultTimezone)
	}
	loc, _ := cronLocation(tz)
	if loc == nil {
		loc = time.Local
	}
	prefix := strings.TrimSpace(r.emailSubjectPrefix)
	if prefix == "" {
		prefix = "[Cron]"
	}
	name := strings.TrimSpace(job.Name)
	if name == "" {
		name = strings.TrimSpace(job.ID)
	}
	date := now.In(loc).Format("2006-01-02")
	return fmt.Sprintf("%s %s (%s)", prefix, name, date)
}

func cronLocation(name string) (*time.Location, error) {
	if strings.TrimSpace(name) == "" || strings.EqualFold(strings.TrimSpace(name), "local") {
		return time.Local, nil
	}
	return time.LoadLocation(strings.TrimSpace(name))
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

func safeName(id string) string {
	in := strings.TrimSpace(id)
	if in == "" {
		return "job"
	}
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "job"
	}
	return out
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
