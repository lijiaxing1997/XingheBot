package multiagent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type WorkerConfig struct {
	MaxTurns *int `json:"max_turns"`
}

type rootWorkerConfig struct {
	MultiAgent struct {
		Worker WorkerConfig `json:"worker"`
	} `json:"multi_agent"`
}

func LoadWorkerConfig(configPath string) (WorkerConfig, error) {
	path := strings.TrimSpace(configPath)
	if path == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkerConfig{}, err
	}
	var cfg rootWorkerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return WorkerConfig{}, fmt.Errorf("parse config.json: %w", err)
	}
	return cfg.MultiAgent.Worker, nil
}

