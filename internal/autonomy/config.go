package autonomy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Enabled   *bool           `json:"enabled"`
	Heartbeat HeartbeatConfig `json:"heartbeat"`
	Cron      CronConfig      `json:"cron"`
}

type HeartbeatConfig struct {
	Enabled     *bool  `json:"enabled"`
	Every       string `json:"every"`
	CoalesceMs  int    `json:"coalesce_ms"`
	RetryMs     int    `json:"retry_ms"`
	Path        string `json:"path"`
	OkToken     string `json:"ok_token"`
	DedupeHours int    `json:"dedupe_hours"`
}

type CronConfig struct {
	Enabled         *bool  `json:"enabled"`
	StorePath       string `json:"store_path"`
	DefaultTimezone string `json:"default_timezone"`
	MaxTimerDelay   string `json:"max_timer_delay"`
	DefaultTimeout  string `json:"default_timeout"`
	StuckRun        string `json:"stuck_run"`
	MinRefireGap    string `json:"min_refire_gap"`

	EmailTo            string `json:"email_to"`
	EmailSubjectPrefix string `json:"email_subject_prefix"`
}

type configFile struct {
	Autonomy *Config `json:"autonomy"`
}

func DefaultConfig() Config {
	return Config{
		Heartbeat: HeartbeatConfig{
			Every:       "30m",
			CoalesceMs:  250,
			RetryMs:     1000,
			Path:        "HEARTBEAT.md",
			OkToken:     "HEARTBEAT_OK",
			DedupeHours: 24,
		},
		Cron: CronConfig{
			DefaultTimezone:    "Local",
			MaxTimerDelay:      "60s",
			DefaultTimeout:     "10m",
			StuckRun:           "2h",
			MinRefireGap:       "2s",
			EmailSubjectPrefix: "[Cron]",
		},
	}
}

func (c Config) WithDefaults() Config {
	out := c
	def := DefaultConfig()
	if out.Enabled == nil {
		v := true
		out.Enabled = &v
	}

	if out.Heartbeat.Enabled == nil {
		v := false
		out.Heartbeat.Enabled = &v
	}
	if strings.TrimSpace(out.Heartbeat.Every) == "" {
		out.Heartbeat.Every = def.Heartbeat.Every
	}
	if out.Heartbeat.CoalesceMs <= 0 {
		out.Heartbeat.CoalesceMs = def.Heartbeat.CoalesceMs
	}
	if out.Heartbeat.RetryMs <= 0 {
		out.Heartbeat.RetryMs = def.Heartbeat.RetryMs
	}
	if strings.TrimSpace(out.Heartbeat.Path) == "" {
		out.Heartbeat.Path = def.Heartbeat.Path
	}
	if strings.TrimSpace(out.Heartbeat.OkToken) == "" {
		out.Heartbeat.OkToken = def.Heartbeat.OkToken
	}
	if out.Heartbeat.DedupeHours <= 0 {
		out.Heartbeat.DedupeHours = def.Heartbeat.DedupeHours
	}

	if out.Cron.Enabled == nil {
		v := true
		out.Cron.Enabled = &v
	}
	if strings.TrimSpace(out.Cron.DefaultTimezone) == "" {
		out.Cron.DefaultTimezone = def.Cron.DefaultTimezone
	}
	if strings.TrimSpace(out.Cron.MaxTimerDelay) == "" {
		out.Cron.MaxTimerDelay = def.Cron.MaxTimerDelay
	}
	if strings.TrimSpace(out.Cron.DefaultTimeout) == "" {
		out.Cron.DefaultTimeout = def.Cron.DefaultTimeout
	}
	if strings.TrimSpace(out.Cron.StuckRun) == "" {
		out.Cron.StuckRun = def.Cron.StuckRun
	}
	if strings.TrimSpace(out.Cron.MinRefireGap) == "" {
		out.Cron.MinRefireGap = def.Cron.MinRefireGap
	}
	if strings.TrimSpace(out.Cron.EmailSubjectPrefix) == "" {
		out.Cron.EmailSubjectPrefix = def.Cron.EmailSubjectPrefix
	}
	return out
}

func LoadConfig(configPath string) (Config, error) {
	path := strings.TrimSpace(configPath)
	if path == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultConfig().WithDefaults(), nil
		}
		return Config{}, err
	}

	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Autonomy == nil {
		return DefaultConfig().WithDefaults(), nil
	}
	return cfg.Autonomy.WithDefaults(), nil
}
