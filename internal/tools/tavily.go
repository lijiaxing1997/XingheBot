package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
)

const (
	defaultTavilyBaseURL          = "https://api.tavily.com"
	defaultTavilyTimeout          = 60 * time.Second
	defaultTavilyMaxResponseBytes = 256 * 1024
)

type tavilyTool struct {
	ConfigPath string
	BaseURL    string
	HTTPClient *http.Client
}

func (t tavilyTool) resolvedBaseURL() string {
	base := strings.TrimSpace(t.BaseURL)
	if base == "" {
		return defaultTavilyBaseURL
	}
	return strings.TrimRight(base, "/")
}

func (t tavilyTool) resolvedHTTPClient() *http.Client {
	if t.HTTPClient != nil {
		return t.HTTPClient
	}
	return &http.Client{Timeout: defaultTavilyTimeout}
}

func (t tavilyTool) loadAPIKey() (string, error) {
	if v := strings.TrimSpace(os.Getenv("TAVILY_API_KEY")); v != "" {
		return v, nil
	}

	cfgPath := strings.TrimSpace(t.ConfigPath)
	if cfgPath == "" {
		cfgPath = "config.json"
	}
	cfg, err := llm.LoadConfig(cfgPath)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(cfg.TavilyAPIKey)
	if key == "" {
		return "", errors.New("tavily_api_key is required (set TAVILY_API_KEY or add tavily_api_key to config.json)")
	}
	return key, nil
}

func (t tavilyTool) doJSON(ctx context.Context, endpoint string, payload map[string]any, timeout time.Duration, maxResponseBytes int) (status int, respBody []byte, truncated bool, err error) {
	if strings.TrimSpace(endpoint) == "" {
		return 0, nil, false, errors.New("endpoint is required")
	}
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}

	apiKey, err := t.loadAPIKey()
	if err != nil {
		return 0, nil, false, err
	}

	if timeout <= 0 {
		timeout = defaultTavilyTimeout
	}
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultTavilyMaxResponseBytes
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, false, fmt.Errorf("marshal request: %w", err)
	}

	url := t.resolvedBaseURL() + endpoint
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.resolvedHTTPClient().Do(req)
	if err != nil {
		return 0, nil, false, err
	}
	defer resp.Body.Close()

	data, truncated, readErr := readLimited(resp.Body, maxResponseBytes)
	if readErr != nil {
		return resp.StatusCode, data, truncated, readErr
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(data))
		if len(snippet) > 2000 {
			snippet = snippet[:2000] + "…"
		}
		return resp.StatusCode, data, truncated, fmt.Errorf("tavily api error: %s: %s", resp.Status, snippet)
	}

	return resp.StatusCode, data, truncated, nil
}

func readLimited(r io.Reader, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		data, err := io.ReadAll(r)
		return data, false, err
	}

	limited := io.LimitReader(r, int64(maxBytes)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return data, false, err
	}
	if len(data) > maxBytes {
		return data[:maxBytes], true, nil
	}
	return data, false, nil
}

func formatTavilyResponse(status int, truncated bool, body []byte) string {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		raw = "(empty response)"
	}

	formatted := raw
	if !truncated {
		var v any
		if err := json.Unmarshal(body, &v); err == nil {
			if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
				formatted = string(pretty)
			}
		}
	}

	return fmt.Sprintf(
		"status_code: %d\ntruncated_response: %t\nresponse_bytes: %d\nresponse:\n%s",
		status,
		truncated,
		len(body),
		formatted,
	)
}

type TavilySearchTool struct {
	ConfigPath string
	BaseURL    string
	HTTPClient *http.Client
}

type tavilySearchArgs struct {
	Query            string         `json:"query"`
	SearchDepth      string         `json:"search_depth"`
	Params           map[string]any `json:"params"`
	TimeoutSeconds   int            `json:"timeout_seconds"`
	MaxResponseBytes int            `json:"max_response_bytes"`
}

func (t *TavilySearchTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "tavily_search",
			Description: "Search the web via Tavily Search API. Requires `tavily_api_key` in config.json or env `TAVILY_API_KEY`.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query (required).",
					},
					"search_depth": map[string]interface{}{
						"type":        "string",
						"description": "Search depth (default: advanced).",
					},
					"params": map[string]interface{}{
						"type":                 "object",
						"description":          "Extra Tavily parameters to merge into request body (optional).",
						"additionalProperties": true,
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Request timeout in seconds (default: %d).", int(defaultTavilyTimeout.Seconds())),
					},
					"max_response_bytes": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Max response bytes to read (default: %d).", defaultTavilyMaxResponseBytes),
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
	}
}

func (t *TavilySearchTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in tavilySearchArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return "", errors.New("query is required")
	}
	searchDepth := strings.TrimSpace(in.SearchDepth)
	if searchDepth == "" {
		searchDepth = "advanced"
	}

	payload := map[string]any{
		"query":        query,
		"search_depth": searchDepth,
	}
	for k, v := range in.Params {
		if strings.TrimSpace(k) == "" {
			continue
		}
		payload[k] = v
	}

	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	status, body, truncated, err := (tavilyTool{
		ConfigPath: t.ConfigPath,
		BaseURL:    t.BaseURL,
		HTTPClient: t.HTTPClient,
	}).doJSON(ctx, "/search", payload, timeout, in.MaxResponseBytes)
	if err != nil {
		return "", err
	}
	return formatTavilyResponse(status, truncated, body), nil
}

type TavilyExtractTool struct {
	ConfigPath string
	BaseURL    string
	HTTPClient *http.Client
}

type tavilyExtractArgs struct {
	URLs             []string       `json:"urls"`
	Params           map[string]any `json:"params"`
	TimeoutSeconds   int            `json:"timeout_seconds"`
	MaxResponseBytes int            `json:"max_response_bytes"`
}

func (t *TavilyExtractTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "tavily_extract",
			Description: "Batch extract content from URLs via Tavily Extract API. Requires `tavily_api_key` in config.json or env `TAVILY_API_KEY`.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"urls": map[string]interface{}{
						"type":        "array",
						"description": "List of URLs to extract (required).",
						"items":       map[string]interface{}{"type": "string"},
					},
					"params": map[string]interface{}{
						"type":                 "object",
						"description":          "Extra Tavily parameters to merge into request body (optional).",
						"additionalProperties": true,
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Request timeout in seconds (default: %d).", int(defaultTavilyTimeout.Seconds())),
					},
					"max_response_bytes": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Max response bytes to read (default: %d).", defaultTavilyMaxResponseBytes),
					},
				},
				"required":             []string{"urls"},
				"additionalProperties": false,
			},
		},
	}
}

func (t *TavilyExtractTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in tavilyExtractArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if len(in.URLs) == 0 {
		return "", errors.New("urls is required")
	}
	urls := make([]string, 0, len(in.URLs))
	for _, u := range in.URLs {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		urls = append(urls, u)
	}
	if len(urls) == 0 {
		return "", errors.New("urls is required")
	}

	payload := map[string]any{
		"urls": urls,
	}
	for k, v := range in.Params {
		if strings.TrimSpace(k) == "" {
			continue
		}
		payload[k] = v
	}

	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	status, body, truncated, err := (tavilyTool{
		ConfigPath: t.ConfigPath,
		BaseURL:    t.BaseURL,
		HTTPClient: t.HTTPClient,
	}).doJSON(ctx, "/extract", payload, timeout, in.MaxResponseBytes)
	if err != nil {
		return "", err
	}
	return formatTavilyResponse(status, truncated, body), nil
}

type TavilyCrawlTool struct {
	ConfigPath string
	BaseURL    string
	HTTPClient *http.Client
}

type tavilyCrawlArgs struct {
	URL              string         `json:"url"`
	ExtractDepth     string         `json:"extract_depth"`
	Params           map[string]any `json:"params"`
	TimeoutSeconds   int            `json:"timeout_seconds"`
	MaxResponseBytes int            `json:"max_response_bytes"`
}

func (t *TavilyCrawlTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Type: "function",
		Function: llm.ToolFunctionDef{
			Name:        "tavily_crawl",
			Description: "Crawl a URL via Tavily Crawl API. Requires `tavily_api_key` in config.json or env `TAVILY_API_KEY`.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL to crawl (required).",
					},
					"extract_depth": map[string]interface{}{
						"type":        "string",
						"description": "Extract depth (default: advanced).",
					},
					"params": map[string]interface{}{
						"type":                 "object",
						"description":          "Extra Tavily parameters to merge into request body (optional).",
						"additionalProperties": true,
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Request timeout in seconds (default: %d).", int(defaultTavilyTimeout.Seconds())),
					},
					"max_response_bytes": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Max response bytes to read (default: %d).", defaultTavilyMaxResponseBytes),
					},
				},
				"required":             []string{"url"},
				"additionalProperties": false,
			},
		},
	}
}

func (t *TavilyCrawlTool) Call(ctx context.Context, args json.RawMessage) (string, error) {
	var in tavilyCrawlArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	url := strings.TrimSpace(in.URL)
	if url == "" {
		return "", errors.New("url is required")
	}
	extractDepth := strings.TrimSpace(in.ExtractDepth)
	if extractDepth == "" {
		extractDepth = "advanced"
	}

	payload := map[string]any{
		"url":           url,
		"extract_depth": extractDepth,
	}
	for k, v := range in.Params {
		if strings.TrimSpace(k) == "" {
			continue
		}
		payload[k] = v
	}

	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	status, body, truncated, err := (tavilyTool{
		ConfigPath: t.ConfigPath,
		BaseURL:    t.BaseURL,
		HTTPClient: t.HTTPClient,
	}).doJSON(ctx, "/crawl", payload, timeout, in.MaxResponseBytes)
	if err != nil {
		return "", err
	}
	return formatTavilyResponse(status, truncated, body), nil
}
