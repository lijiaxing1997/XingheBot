package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type startParams struct {
	Master startParamsMaster `json:"master"`
	Slave  startParamsSlave  `json:"slave"`
}

type startParamsMaster struct {
	Listen       *string `json:"listen,omitempty"`
	WSPath       *string `json:"ws_path,omitempty"`
	UI           *string `json:"ui,omitempty"`
	RedisURL     *string `json:"redis_url,omitempty"`
	Heartbeat    *string `json:"heartbeat,omitempty"`
	ChatToolMode *string `json:"chat_tool_mode,omitempty"`
}

type startParamsSlave struct {
	MasterURL          *string `json:"master,omitempty"`
	Name               *string `json:"name,omitempty"`
	SlaveID            *string `json:"id,omitempty"`
	Tags               *string `json:"tags,omitempty"`
	Heartbeat          *string `json:"heartbeat,omitempty"`
	MaxInflightRuns    *int    `json:"max_inflight_runs,omitempty"`
	InsecureSkipVerify *bool   `json:"insecure_skip_verify,omitempty"`
}

func loadStartParams(configPath string) (startParams, bool, error) {
	path := strings.TrimSpace(configPath)
	if path == "" {
		path = "config.json"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return startParams{}, false, nil
		}
		return startParams{}, false, err
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return startParams{}, false, fmt.Errorf("parse config.json: %w", err)
	}

	raw, ok := root["start_params"]
	if !ok || len(bytes.TrimSpace(raw)) == 0 {
		return startParams{}, false, nil
	}

	var params startParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return startParams{}, false, fmt.Errorf("parse config.json.start_params: %w", err)
	}
	return params, true, nil
}

