package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type GatewayConfig struct {
	Enabled bool        `json:"enabled"`
	Email   EmailConfig `json:"email"`
}

type EmailConfig struct {
	Provider          string     `json:"provider"`
	EmailAddress      string     `json:"email_address"`
	AuthorizationCode string     `json:"authorization_code"`
	IMAP              IMAPConfig `json:"imap"`
	SMTP              SMTPConfig `json:"smtp"`

	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	AllowedSenders      string `json:"allowed_senders"`
}

type IMAPConfig struct {
	Server string `json:"server"`
	Port   int    `json:"port"`
	UseSSL bool   `json:"use_ssl"`
}

type SMTPConfig struct {
	Server string `json:"server"`
	Port   int    `json:"port"`
	UseSSL bool   `json:"use_ssl"`
}

func LoadGatewayConfig(path string) (GatewayConfig, error) {
	if strings.TrimSpace(path) == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GatewayConfig{}, err
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return GatewayConfig{}, fmt.Errorf("parse config.json: %w", err)
	}

	var cfg GatewayConfig
	if raw, ok := root["gateway"]; ok && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return GatewayConfig{}, fmt.Errorf("parse config.json.gateway: %w", err)
		}
	} else if raw, ok := root["网关配置"]; ok && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return GatewayConfig{}, fmt.Errorf("parse config.json.网关配置: %w", err)
		}
	}

	cfg.Email.applyDefaults()
	return cfg, nil
}

func (c *EmailConfig) applyDefaults() {
	if c == nil {
		return
	}
	if strings.TrimSpace(c.Provider) == "" {
		c.Provider = "126"
	}
	if c.PollIntervalSeconds <= 0 {
		c.PollIntervalSeconds = 5
	}
	if c.IMAP.Port <= 0 {
		c.IMAP.Port = 993
	}
	if strings.TrimSpace(c.IMAP.Server) == "" && strings.EqualFold(strings.TrimSpace(c.Provider), "126") {
		c.IMAP.Server = "imap.126.com"
	}
	if c.SMTP.Port <= 0 {
		c.SMTP.Port = 465
	}
	if strings.TrimSpace(c.SMTP.Server) == "" && strings.EqualFold(strings.TrimSpace(c.Provider), "126") {
		c.SMTP.Server = "smtp.126.com"
	}
}

func (c EmailConfig) PollInterval() time.Duration {
	sec := c.PollIntervalSeconds
	if sec <= 0 {
		sec = 5
	}
	return time.Duration(sec) * time.Second
}

func (c EmailConfig) AllowedSendersList() []string {
	return parseEmailList(c.AllowedSenders)
}

func (c EmailConfig) Validate() error {
	if strings.TrimSpace(c.EmailAddress) == "" {
		return errors.New("email_address is required")
	}
	if strings.TrimSpace(c.AuthorizationCode) == "" {
		return errors.New("authorization_code is required")
	}
	if strings.TrimSpace(c.IMAP.Server) == "" {
		return errors.New("imap.server is required")
	}
	if c.IMAP.Port <= 0 {
		return errors.New("imap.port is required")
	}
	if strings.TrimSpace(c.SMTP.Server) == "" {
		return errors.New("smtp.server is required")
	}
	if c.SMTP.Port <= 0 {
		return errors.New("smtp.port is required")
	}
	if c.PollIntervalSeconds <= 0 {
		return errors.New("poll_interval_seconds must be > 0")
	}
	return nil
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

func boolFromEnv(key string) (bool, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return false, false
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		return true, true
	case "0", "false", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}

func intFromEnv(key string) (int, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0, false
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return i, true
}
