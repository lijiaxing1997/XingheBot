package mcpclient

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Servers []ServerConfig `json:"mcp_servers"`
}

type ServerConfig struct {
	Name       string            `json:"name"`
	Transport  string            `json:"transport"`
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	Dir        string            `json:"dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	InheritEnv *bool             `json:"inherit_env,omitempty"`
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Disabled   bool              `json:"disabled,omitempty"`
}

func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		path = "mcp.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}
