package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempTavilyConfig(t *testing.T, apiKey string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	payload := map[string]any{
		"tavily_api_key": apiKey,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestTavilySearchToolSendsRequestAndFormatsResponse(t *testing.T) {
	cfgPath := writeTempTavilyConfig(t, "test-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/search" {
			t.Errorf("expected /search, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("expected Authorization Bearer test-key, got %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", got)
		}

		bodyBytes, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			t.Errorf("invalid JSON payload: %v", err)
		}
		if payload["query"] != "hello" {
			t.Errorf("expected query hello, got %#v", payload["query"])
		}
		if payload["search_depth"] != "advanced" {
			t.Errorf("expected search_depth advanced, got %#v", payload["search_depth"])
		}
		if payload["max_results"] != float64(3) {
			t.Errorf("expected max_results 3, got %#v", payload["max_results"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":"ok","results":[{"title":"t1"}]}`))
	}))
	defer server.Close()

	tool := &TavilySearchTool{
		ConfigPath: cfgPath,
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}

	args := map[string]any{
		"query": "hello",
		"params": map[string]any{
			"max_results": 3,
		},
	}
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	out, err := tool.Call(context.Background(), json.RawMessage(data))
	if err != nil {
		t.Fatalf("tavily_search returned error: %v", err)
	}

	if !strings.Contains(out, "status_code: 200") {
		t.Fatalf("expected status_code 200, got:\n%s", out)
	}
	if !strings.Contains(out, "truncated_response: false") {
		t.Fatalf("expected truncated_response false, got:\n%s", out)
	}
	if !strings.Contains(out, `"answer": "ok"`) {
		t.Fatalf("expected response to contain answer ok, got:\n%s", out)
	}
}

func TestTavilyCrawlToolDefaultsExtractDepth(t *testing.T) {
	cfgPath := writeTempTavilyConfig(t, "test-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/crawl" {
			t.Errorf("expected /crawl, got %s", r.URL.Path)
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			t.Errorf("invalid JSON payload: %v", err)
		}
		if payload["extract_depth"] != "advanced" {
			t.Errorf("expected extract_depth advanced, got %#v", payload["extract_depth"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	tool := &TavilyCrawlTool{
		ConfigPath: cfgPath,
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	data, _ := json.Marshal(map[string]any{"url": "https://example.com"})
	if _, err := tool.Call(context.Background(), json.RawMessage(data)); err != nil {
		t.Fatalf("tavily_crawl returned error: %v", err)
	}
}

func TestTavilyToolsRequireAPIKey(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("TAVILY_API_KEY", "")
	tool := &TavilySearchTool{ConfigPath: cfgPath, BaseURL: "http://example.invalid"}

	data, _ := json.Marshal(map[string]any{"query": "hello"})
	_, err := tool.Call(context.Background(), json.RawMessage(data))
	if err == nil {
		t.Fatalf("expected missing api key error")
	}
	if !strings.Contains(err.Error(), "tavily_api_key is required") {
		t.Fatalf("expected tavily_api_key is required, got: %v", err)
	}
}
