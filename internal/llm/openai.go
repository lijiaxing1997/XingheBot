package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Type     string          `json:"type"`
	Function ToolFunctionDef `json:"function"`
}

type ToolFunctionDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type ChatRequest struct {
	Model       string           `json:"model"`
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	ToolChoice  interface{}      `json:"tool_choice,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature float32          `json:"temperature,omitempty"`
}

type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Client struct {
	ModelType  ModelType
	BaseURL    string
	APIKey     string
	Model      string
	MaxTokens  int
	HTTPClient *http.Client

	sdk          openai.Client
	anthropicSDK anthropic.Client
}

type Config struct {
	ModelConfig *ModelConfig     `json:"model_config"`
	WebSearch   *WebSearchConfig `json:"web_search"`
	Assistant   AssistantConfig  `json:"assistant"`

	// Legacy flat keys (deprecated).
	APIKey       string `json:"api_key,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	Model        string `json:"model,omitempty"`
	MaxTokens    int    `json:"max_tokens,omitempty"`
	TavilyAPIKey string `json:"tavily_api_key,omitempty"`
}

type ModelConfig struct {
	ModelType string `json:"model_type"`
	APIKey    string `json:"api_key"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
}

type WebSearchConfig struct {
	TavilyAPIKey string `json:"tavily_api_key"`
}

type AssistantConfig struct {
	ReplyStyle     ReplyStyleConfig `json:"reply_style"`
	AutoCompaction json.RawMessage  `json:"auto_compaction,omitempty"`
}

type ReplyStyleConfig struct {
	Enabled *bool  `json:"enabled"`
	MDPath  string `json:"md_path"`
	Text    string `json:"text"`
}

func NewClientFromEnv() (*Client, error) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY is required")
	}
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	if model == "" {
		model = "gpt-4o-mini"
	}
	maxTokens := 0
	if raw := strings.TrimSpace(os.Getenv("OPENAI_MAX_TOKENS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxTokens = v
		}
	}
	c := &Client{
		ModelType: ModelTypeOpenAI,
		BaseURL:   strings.TrimRight(baseURL, "/"),
		APIKey:    apiKey,
		Model:     model,
		MaxTokens: maxTokens,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
	if err := c.initSDK(); err != nil {
		return nil, err
	}
	return c, nil
}

func NewClientFromConfig(path string) (*Client, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}
	mc := cfg.resolvedModelConfig()
	modelType, err := ParseModelType(mc.ModelType)
	if err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(mc.APIKey)
	if apiKey == "" {
		return nil, errors.New("model_config.api_key (or legacy api_key) is required in config.json")
	}
	baseURL := strings.TrimSpace(mc.BaseURL)
	if baseURL == "" {
		switch modelType {
		case ModelTypeAnthropics:
			baseURL = defaultAnthropicBaseURL
		default:
			baseURL = defaultBaseURL
		}
	}
	model := strings.TrimSpace(mc.Model)
	switch modelType {
	case ModelTypeAnthropics:
		if model == "" {
			return nil, errors.New("model_config.model is required when model_config.model_type is anthropics")
		}
	default:
		if model == "" {
			model = "gpt-4o-mini"
		}
	}
	c := &Client{
		ModelType: modelType,
		BaseURL:   strings.TrimRight(baseURL, "/"),
		APIKey:    apiKey,
		Model:     model,
		MaxTokens: mc.MaxTokens,
		HTTPClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
	switch modelType {
	case ModelTypeAnthropics:
		if err := c.initAnthropicSDK(); err != nil {
			return nil, err
		}
	default:
		if err := c.initSDK(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		path = "config.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("%s not found (hint: run `xinghebot chat --init` or `xinghebot master --init` / `xinghebot slave --init`): %w", strings.TrimSpace(path), err)
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config.json: %w", err)
	}
	return cfg, nil
}

func (c Config) resolvedModelConfig() ModelConfig {
	if c.ModelConfig != nil {
		return *c.ModelConfig
	}
	return ModelConfig{
		ModelType: "",
		APIKey:    c.APIKey,
		BaseURL:   c.BaseURL,
		Model:     c.Model,
		MaxTokens: c.MaxTokens,
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if c == nil {
		return nil, errors.New("nil client")
	}
	if c.ModelType == ModelTypeAnthropics {
		return c.chatAnthropics(ctx, req)
	}
	if err := c.ensureSDK(); err != nil {
		return nil, err
	}
	if req.Model == "" {
		req.Model = c.Model
	}
	if req.MaxTokens <= 0 && c.MaxTokens > 0 {
		req.MaxTokens = c.MaxTokens
	}

	msgs, err := toSDKMessages(req.Messages)
	if err != nil {
		return nil, err
	}

	params := openai.ChatCompletionNewParams{
		Messages: msgs,
		Model:    openai.ChatModel(req.Model),
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.Temperature != 0 {
		params.Temperature = openai.Float(float64(req.Temperature))
	}
	if len(req.Tools) > 0 {
		tools, err := toSDKTools(req.Tools)
		if err != nil {
			return nil, err
		}
		params.Tools = tools
	}

	resp, err := c.sdk.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return nil, errors.New("no choices in response")
	}
	return fromSDKChatCompletion(resp), nil
}

const defaultBaseURL = "https://api.openai.com"

func (c *Client) ensureSDK() error {
	if c == nil {
		return errors.New("nil client")
	}
	if len(c.sdk.Options) > 0 {
		return nil
	}
	return c.initSDK()
}

func (c *Client) initSDK() error {
	if c == nil {
		return errors.New("nil client")
	}
	apiKey := strings.TrimSpace(c.APIKey)
	if apiKey == "" {
		return errors.New("api key is required")
	}

	base := resolvedSDKBaseURL(c.BaseURL)
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(base),
	}
	if c.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(c.HTTPClient))
	}
	c.sdk = openai.NewClient(opts...)
	return nil
}

func resolvedSDKBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/"
	}
	return base + "/v1/"
}

func toSDKMessages(msgs []Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		role := strings.TrimSpace(strings.ToLower(m.Role))
		switch role {
		case "system":
			var sys openai.ChatCompletionSystemMessageParam
			sys.Content.OfString = openai.String(m.Content)
			if strings.TrimSpace(m.Name) != "" {
				sys.Name = openai.String(m.Name)
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfSystem: &sys})
		case "user":
			var user openai.ChatCompletionUserMessageParam
			user.Content.OfString = openai.String(m.Content)
			if strings.TrimSpace(m.Name) != "" {
				user.Name = openai.String(m.Name)
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfUser: &user})
		case "assistant":
			var asst openai.ChatCompletionAssistantMessageParam
			asst.Content.OfString = openai.String(m.Content)
			if strings.TrimSpace(m.Name) != "" {
				asst.Name = openai.String(m.Name)
			}
			if len(m.ToolCalls) > 0 {
				toolCalls, err := toSDKToolCalls(m.ToolCalls)
				if err != nil {
					return nil, err
				}
				asst.ToolCalls = toolCalls
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
		case "tool":
			toolCallID := strings.TrimSpace(m.ToolCallID)
			if toolCallID == "" {
				return nil, errors.New("tool message missing tool_call_id")
			}
			out = append(out, openai.ToolMessage(m.Content, toolCallID))
		default:
			if role == "" {
				return nil, errors.New("message role is required")
			}
			return nil, fmt.Errorf("unsupported message role: %q", m.Role)
		}
	}
	return out, nil
}

func toSDKToolCalls(calls []ToolCall) ([]openai.ChatCompletionMessageToolCallUnionParam, error) {
	out := make([]openai.ChatCompletionMessageToolCallUnionParam, 0, len(calls))
	for _, c := range calls {
		callType := strings.TrimSpace(strings.ToLower(c.Type))
		if callType == "" || callType == "function" {
			args := sanitizeToolCallArguments(c.Function.Arguments)
			out = append(out, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: c.ID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      c.Function.Name,
						Arguments: args,
					},
				},
			})
			continue
		}
		return nil, fmt.Errorf("unsupported tool call type: %q", c.Type)
	}
	return out, nil
}

const maxSanitizedToolCallRawArgsBytes = 4096

func sanitizeToolCallArguments(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "{}"
	}

	valid := json.Valid([]byte(trimmed))
	if valid && strings.HasPrefix(trimmed, "{") {
		return trimmed
	}

	payload := map[string]any{
		"__raw": clampUTF8(trimmed, maxSanitizedToolCallRawArgsBytes),
	}

	if valid {
		payload["__non_object_json"] = true
	} else {
		payload["__invalid_json"] = true
		var tmp any
		if err := json.Unmarshal([]byte(trimmed), &tmp); err != nil {
			payload["__error"] = err.Error()
		}
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(out)
}

func clampUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if s == "" || len(s) <= maxBytes {
		return s
	}

	cut := maxBytes
	if cut > len(s) {
		cut = len(s)
	}
	for cut > 0 && (s[cut-1]&0xC0) == 0x80 {
		cut--
	}
	if cut <= 0 {
		return ""
	}
	out := s[:cut]
	if !utf8.ValidString(out) {
		for cut > 0 && !utf8.ValidString(s[:cut]) {
			cut--
		}
		if cut <= 0 {
			return ""
		}
		out = s[:cut]
	}
	return strings.TrimRight(out, "\n")
}

func toSDKTools(tools []ToolDefinition) ([]openai.ChatCompletionToolUnionParam, error) {
	out := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, t := range tools {
		typ := strings.TrimSpace(strings.ToLower(t.Type))
		if typ == "" || typ == "function" {
			fn := openai.FunctionDefinitionParam{
				Name: t.Function.Name,
			}
			if desc := strings.TrimSpace(t.Function.Description); desc != "" {
				fn.Description = openai.String(desc)
			}
			if t.Function.Parameters != nil {
				params, err := toSDKFunctionParameters(t.Function.Parameters)
				if err != nil {
					return nil, err
				}
				fn.Parameters = params
			}
			out = append(out, openai.ChatCompletionFunctionTool(fn))
			continue
		}
		return nil, fmt.Errorf("unsupported tool type: %q", t.Type)
	}
	return out, nil
}

func toSDKFunctionParameters(v any) (openai.FunctionParameters, error) {
	switch p := v.(type) {
	case openai.FunctionParameters:
		return p, nil
	case map[string]any:
		return openai.FunctionParameters(p), nil
	case json.RawMessage:
		var m map[string]any
		if err := json.Unmarshal(p, &m); err != nil {
			return nil, fmt.Errorf("parse function parameters: %w", err)
		}
		return openai.FunctionParameters(m), nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal function parameters: %w", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("parse function parameters: %w", err)
		}
		return openai.FunctionParameters(m), nil
	}
}

func fromSDKChatCompletion(resp *openai.ChatCompletion) *ChatResponse {
	out := &ChatResponse{
		ID:      resp.ID,
		Object:  string(resp.Object),
		Created: resp.Created,
		Model:   resp.Model,
		Usage: Usage{
			PromptTokens:     int(resp.Usage.PromptTokens),
			CompletionTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
		},
	}

	out.Choices = make([]Choice, 0, len(resp.Choices))
	for _, choice := range resp.Choices {
		out.Choices = append(out.Choices, Choice{
			Index:        int(choice.Index),
			FinishReason: choice.FinishReason,
			Message:      fromSDKMessage(choice.Message),
		})
	}
	return out
}

func fromSDKMessage(msg openai.ChatCompletionMessage) Message {
	out := Message{
		Role:    string(msg.Role),
		Content: msg.Content,
	}
	if len(msg.ToolCalls) == 0 {
		return out
	}
	out.ToolCalls = make([]ToolCall, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		switch variant := call.AsAny().(type) {
		case openai.ChatCompletionMessageFunctionToolCall:
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:   variant.ID,
				Type: string(variant.Type),
				Function: ToolCallFunction{
					Name:      variant.Function.Name,
					Arguments: variant.Function.Arguments,
				},
			})
		default:
			// Ignore unknown tool call variants.
		}
	}
	return out
}
