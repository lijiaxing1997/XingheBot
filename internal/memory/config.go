package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type EmbeddingsConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

type RedactionConfig struct {
	Enabled  *bool    `json:"enabled"`
	Patterns []string `json:"patterns"`
}

type Config struct {
	Enabled *bool `json:"enabled"`

	WorkspaceDir string `json:"workspace_dir"`
	ProjectKey   string `json:"project_key"`
	RootDir      string `json:"root_dir"`

	Backend string `json:"backend"`

	DBPath                 string `json:"db_path"`
	FTSEnabled             *bool  `json:"fts_enabled"`
	VectorEnabled          *bool  `json:"vector_enabled"`
	SQLiteVecExtensionPath string `json:"sqlite_vec_extension_path"`

	HybridVectorWeight float64 `json:"hybrid_vector_weight"`
	HybridTextWeight   float64 `json:"hybrid_text_weight"`

	Embeddings EmbeddingsConfig `json:"embeddings"`

	AutoFlushOnCompaction     *bool `json:"auto_flush_on_compaction"`
	AutoCaptureOnNewSession   *bool `json:"auto_capture_on_new_session"`
	AutoFlushOnSessionCapture *bool `json:"auto_flush_on_session_capture"`
	IndexHistoryJSONL         *bool `json:"index_history_jsonl"`

	MaxResults int `json:"max_results"`

	Redaction RedactionConfig `json:"redaction"`
}

type configFile struct {
	Memory *Config `json:"memory"`
}

func DefaultConfig() Config {
	return Config{
		WorkspaceDir:       "~/.xinghebot/workspace",
		Backend:            "scan",
		MaxResults:         10,
		HybridVectorWeight: 0.7,
		HybridTextWeight:   0.3,
		Embeddings: EmbeddingsConfig{
			Model: "text-embedding-3-small",
		},
	}
}

func (c Config) WithDefaults() Config {
	out := c
	if out.Enabled == nil {
		enabled := true
		out.Enabled = &enabled
	}
	if strings.TrimSpace(out.WorkspaceDir) == "" {
		out.WorkspaceDir = DefaultConfig().WorkspaceDir
	}
	if strings.TrimSpace(out.Backend) == "" {
		out.Backend = DefaultConfig().Backend
	}
	if out.MaxResults <= 0 {
		out.MaxResults = DefaultConfig().MaxResults
	}
	if out.HybridVectorWeight <= 0 {
		out.HybridVectorWeight = DefaultConfig().HybridVectorWeight
	}
	if out.HybridTextWeight <= 0 {
		out.HybridTextWeight = DefaultConfig().HybridTextWeight
	}
	if strings.TrimSpace(out.Embeddings.Model) == "" {
		out.Embeddings.Model = DefaultConfig().Embeddings.Model
	}
	if out.FTSEnabled == nil {
		v := true
		out.FTSEnabled = &v
	}
	if out.VectorEnabled == nil {
		v := true
		out.VectorEnabled = &v
	}
	if out.AutoFlushOnCompaction == nil {
		v := true
		out.AutoFlushOnCompaction = &v
	}
	if out.AutoCaptureOnNewSession == nil {
		v := true
		out.AutoCaptureOnNewSession = &v
	}
	if out.AutoFlushOnSessionCapture == nil {
		v := true
		out.AutoFlushOnSessionCapture = &v
	}
	if out.IndexHistoryJSONL == nil {
		v := false
		out.IndexHistoryJSONL = &v
	}

	if out.Redaction.Enabled == nil {
		v := true
		out.Redaction.Enabled = &v
	}
	if out.Redaction.Patterns == nil {
		out.Redaction.Patterns = []string{"sk-", "tvly-", "AKIA", "-----BEGIN", "authorization_code"}
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
	if cfg.Memory == nil {
		return DefaultConfig().WithDefaults(), nil
	}
	return cfg.Memory.WithDefaults(), nil
}
