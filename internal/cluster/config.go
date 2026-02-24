package cluster

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ClusterConfig struct {
	Secret string      `json:"secret"`
	TLS    TLSConfig   `json:"tls"`
	Files  FilesConfig `json:"files"`
}

type TLSConfig struct {
	Enabled            bool   `json:"enabled"`
	CertFile           string `json:"cert_file"`
	KeyFile            string `json:"key_file"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}

type FilesConfig struct {
	RootDir           string `json:"root_dir"`
	MaxFileBytes      int64  `json:"max_file_bytes"`
	MaxTotalBytes     int64  `json:"max_total_bytes"`
	RetentionDays     int    `json:"retention_days"`
	ChunkSizeBytes    int    `json:"chunk_size_bytes"`
	MaxInflightChunks int    `json:"max_inflight_chunks"`
}

func LoadClusterConfig(path string) (ClusterConfig, error) {
	if strings.TrimSpace(path) == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ClusterConfig{}, fmt.Errorf("%s not found (hint: run `xinghebot master --init` or `xinghebot slave --init`): %w", strings.TrimSpace(path), err)
		}
		return ClusterConfig{}, err
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return ClusterConfig{}, fmt.Errorf("parse config.json: %w", err)
	}

	var cfg ClusterConfig
	if raw, ok := root["cluster"]; ok && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return ClusterConfig{}, fmt.Errorf("parse config.json.cluster: %w", err)
		}
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *ClusterConfig) applyDefaults() {
	if c == nil {
		return
	}
	if strings.TrimSpace(c.Files.RootDir) == "" {
		c.Files.RootDir = ".cluster/files"
	}
	if c.Files.MaxFileBytes <= 0 {
		c.Files.MaxFileBytes = 2 << 30
	}
	if c.Files.MaxTotalBytes <= 0 {
		c.Files.MaxTotalBytes = 20 << 30
	}
	if c.Files.RetentionDays <= 0 {
		c.Files.RetentionDays = 7
	}
	if c.Files.ChunkSizeBytes <= 0 {
		c.Files.ChunkSizeBytes = 256 << 10
	}
	if c.Files.MaxInflightChunks <= 0 {
		c.Files.MaxInflightChunks = 8
	}
}

func DecodeSecretBase64(secret string) ([]byte, error) {
	raw := strings.TrimSpace(secret)
	if raw == "" {
		return nil, errors.New("cluster.secret is empty")
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode cluster.secret: %w", err)
	}
	if len(decoded) < 16 {
		return nil, errors.New("cluster.secret is too short")
	}
	return decoded, nil
}

func EnsureClusterSecret(configPath string) (secret string, generated bool, err error) {
	path := strings.TrimSpace(configPath)
	if path == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, fmt.Errorf("%s not found (hint: run `xinghebot master --init`): %w", strings.TrimSpace(path), err)
		}
		return "", false, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return "", false, fmt.Errorf("parse config.json: %w", err)
	}
	clusterObj, _ := root["cluster"].(map[string]any)
	if clusterObj == nil {
		clusterObj = make(map[string]any)
		root["cluster"] = clusterObj
	}
	if v, ok := clusterObj["secret"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v), false, nil
	}

	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", false, err
	}
	secret = base64.StdEncoding.EncodeToString(b[:])
	clusterObj["secret"] = secret
	generated = true

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", false, err
	}
	out = append(out, '\n')

	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf(".config.json.tmp.%d", time.Now().UTC().UnixNano()))
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", false, err
	}
	return secret, true, nil
}
