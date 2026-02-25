package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"test_skill_agent/internal/autonomy"
	"test_skill_agent/internal/autonomy/cron"
	"test_skill_agent/internal/llm"
)

type CronListTool struct {
	ConfigPath string
}

type cronListArgs struct {
	Enabled *bool `json:"enabled"`
}

func (t *CronListTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "cron_list",
			Description: "List scheduled cron jobs.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled": map[string]any{"type": "boolean"},
				},
			},
		},
	}
}

func (t *CronListTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	jobsPath, _, _, err := resolveCronStore(t.ConfigPath)
	if err != nil {
		return "", err
	}
	store := cron.NewStoreManager(jobsPath)
	jobs, err := store.List()
	if err != nil {
		return "", err
	}

	var in cronListArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", err
		}
	}
	if in.Enabled != nil {
		filtered := jobs[:0]
		for _, job := range jobs {
			if job.Enabled == *in.Enabled {
				filtered = append(filtered, job)
			}
		}
		jobs = filtered
	}

	return prettyJSON(map[string]any{
		"jobs_path": jobsPath,
		"count":     len(jobs),
		"jobs":      jobs,
	})
}

type CronGetTool struct {
	ConfigPath string
}

type cronGetArgs struct {
	ID string `json:"id"`
}

func (t *CronGetTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "cron_get",
			Description: "Get a scheduled cron job by id.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (t *CronGetTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in cronGetArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	jobsPath, _, _, err := resolveCronStore(t.ConfigPath)
	if err != nil {
		return "", err
	}
	store := cron.NewStoreManager(jobsPath)
	job, ok, err := store.Get(in.ID)
	if err != nil {
		return "", err
	}
	if !ok {
		return prettyJSON(map[string]any{
			"found": false,
			"id":    strings.TrimSpace(in.ID),
		})
	}
	return prettyJSON(map[string]any{
		"found":     true,
		"jobs_path": jobsPath,
		"job":       job,
	})
}

type CronUpsertTool struct {
	ConfigPath string
	Wake       func()
}

type cronUpsertArgs struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled"`

	Schedule cron.Schedule `json:"schedule"`
	Task     cron.Task     `json:"task"`

	Delivery struct {
		Type    string `json:"type"`
		To      string `json:"to"`
		Subject string `json:"subject"`
	} `json:"delivery"`
}

func (t *CronUpsertTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "cron_upsert",
			Description: "Create or update a scheduled cron job.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":      map[string]any{"type": "string", "description": "Optional. If omitted, a new job id is generated."},
					"name":    map[string]any{"type": "string"},
					"enabled": map[string]any{"type": "boolean"},
					"schedule": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type":     map[string]any{"type": "string", "description": "cron|every|at"},
							"expr":     map[string]any{"type": "string", "description": "Cron expression (5-field, e.g. \"0 9 * * *\")"},
							"every":    map[string]any{"type": "string", "description": "Duration (e.g. \"30m\")"},
							"at":       map[string]any{"type": "string", "description": "RFC3339 or local time string (e.g. \"2026-02-25 09:00\")"},
							"timezone": map[string]any{"type": "string", "description": "IANA timezone or \"Local\""},
						},
					},
					"task": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type":      map[string]any{"type": "string", "description": "llm"},
							"prompt":    map[string]any{"type": "string"},
							"max_turns": map[string]any{"type": "integer"},
						},
					},
					"delivery": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type":    map[string]any{"type": "string", "description": "email"},
							"to":      map[string]any{"type": "string", "description": "Comma-separated recipients (optional; defaults to autonomy.cron.email_to)"},
							"subject": map[string]any{"type": "string"},
						},
					},
				},
				"required": []string{"name", "schedule", "task"},
			},
		},
	}
}

func (t *CronUpsertTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in cronUpsertArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	jobsPath, defaultTZ, minRefireGap, err := resolveCronStore(t.ConfigPath)
	if err != nil {
		return "", err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	scheduleType := strings.TrimSpace(in.Schedule.Type)
	if scheduleType == "" {
		switch {
		case strings.TrimSpace(in.Schedule.Every) != "":
			scheduleType = "every"
		case strings.TrimSpace(in.Schedule.At) != "":
			scheduleType = "at"
		default:
			scheduleType = "cron"
		}
	}
	job := cron.Job{
		ID:      strings.TrimSpace(in.ID),
		Name:    strings.TrimSpace(in.Name),
		Enabled: enabled,
		Schedule: cron.Schedule{
			Type:     scheduleType,
			Expr:     strings.TrimSpace(in.Schedule.Expr),
			Every:    strings.TrimSpace(in.Schedule.Every),
			At:       strings.TrimSpace(in.Schedule.At),
			Timezone: strings.TrimSpace(in.Schedule.Timezone),
		},
		Task: cron.Task{
			Type:     strings.TrimSpace(in.Task.Type),
			Prompt:   strings.TrimSpace(in.Task.Prompt),
			MaxTurns: in.Task.MaxTurns,
		},
		Delivery: cron.Delivery{
			Type:    strings.TrimSpace(in.Delivery.Type),
			To:      parseEmailList(in.Delivery.To),
			Subject: strings.TrimSpace(in.Delivery.Subject),
		},
	}
	out, err := cron.NewStoreManager(jobsPath).Upsert(job, time.Now().UTC(), defaultTZ, minRefireGap)
	if err != nil {
		return "", err
	}
	if t.Wake != nil {
		t.Wake()
	}
	return prettyJSON(map[string]any{
		"status":    "ok",
		"jobs_path": jobsPath,
		"job":       out,
	})
}

type CronDeleteTool struct {
	ConfigPath string
	Wake       func()
}

type cronDeleteArgs struct {
	ID string `json:"id"`
}

func (t *CronDeleteTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "cron_delete",
			Description: "Delete a scheduled cron job by id.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (t *CronDeleteTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in cronDeleteArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	jobsPath, _, _, err := resolveCronStore(t.ConfigPath)
	if err != nil {
		return "", err
	}
	if err := cron.NewStoreManager(jobsPath).Delete(in.ID, time.Now().UTC()); err != nil {
		return "", err
	}
	if t.Wake != nil {
		t.Wake()
	}
	return prettyJSON(map[string]any{
		"status":    "ok",
		"jobs_path": jobsPath,
		"deleted":   strings.TrimSpace(in.ID),
	})
}

type CronEnableTool struct {
	ConfigPath string
	Enabled    bool
	Wake       func()
}

type cronEnableArgs struct {
	ID string `json:"id"`
}

func (t *CronEnableTool) Definition() llm.ToolDefinition {
	name := "cron_enable"
	desc := "Enable a scheduled cron job by id."
	if !t.Enabled {
		name = "cron_disable"
		desc = "Disable a scheduled cron job by id."
	}
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        name,
			Description: desc,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (t *CronEnableTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in cronEnableArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	jobsPath, defaultTZ, minRefireGap, err := resolveCronStore(t.ConfigPath)
	if err != nil {
		return "", err
	}
	job, err := cron.NewStoreManager(jobsPath).SetEnabled(in.ID, t.Enabled, time.Now().UTC(), defaultTZ, minRefireGap)
	if err != nil {
		return "", err
	}
	if t.Wake != nil {
		t.Wake()
	}
	return prettyJSON(map[string]any{
		"status":    "ok",
		"jobs_path": jobsPath,
		"job":       job,
	})
}

type CronRunNowTool struct {
	ConfigPath string
	Wake       func()
}

type cronRunNowArgs struct {
	ID string `json:"id"`
}

func (t *CronRunNowTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "cron_run_now",
			Description: "Request a job to run as soon as possible (sets next_run_at=now).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func (t *CronRunNowTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var in cronRunNowArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	jobsPath, _, _, err := resolveCronStore(t.ConfigPath)
	if err != nil {
		return "", err
	}
	job, err := cron.NewStoreManager(jobsPath).RunNow(in.ID, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if t.Wake != nil {
		t.Wake()
	}
	return prettyJSON(map[string]any{
		"status":    "ok",
		"jobs_path": jobsPath,
		"job":       job,
	})
}

func resolveCronStore(configPath string) (jobsPath string, defaultTimezone string, minRefireGap time.Duration, err error) {
	cfg, err := autonomy.LoadConfig(configPath)
	if err != nil {
		return "", "", 0, err
	}
	workDir, _ := os.Getwd()
	jobsPath = strings.TrimSpace(cfg.Cron.StorePath)
	if jobsPath == "" {
		jobsPath, err = cron.ResolveDefaultJobsPath(configPath, workDir)
		if err != nil {
			return "", "", 0, err
		}
	}
	if strings.TrimSpace(jobsPath) == "" {
		return "", "", 0, errors.New("cron jobs store path is empty")
	}
	defaultTimezone = strings.TrimSpace(cfg.Cron.DefaultTimezone)
	minRefireGap, err = time.ParseDuration(strings.TrimSpace(cfg.Cron.MinRefireGap))
	if err != nil || minRefireGap < 0 {
		minRefireGap = 2 * time.Second
	}
	return jobsPath, defaultTimezone, minRefireGap, nil
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
