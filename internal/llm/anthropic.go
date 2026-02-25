package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
)

const (
	defaultAnthropicBaseURL   = "https://api.anthropic.com"
	defaultAnthropicMaxTokens = 1024
)

func (c *Client) ensureAnthropicSDK() error {
	if c == nil {
		return errors.New("nil client")
	}
	if len(c.anthropicSDK.Options) > 0 {
		return nil
	}
	return c.initAnthropicSDK()
}

func (c *Client) initAnthropicSDK() error {
	if c == nil {
		return errors.New("nil client")
	}
	apiKey := strings.TrimSpace(c.APIKey)
	if apiKey == "" {
		return errors.New("api key is required")
	}

	base := resolvedAnthropicBaseURL(c.BaseURL)
	opts := []anthropicoption.RequestOption{
		anthropicoption.WithAPIKey(apiKey),
		anthropicoption.WithBaseURL(base),
	}
	if c.HTTPClient != nil {
		opts = append(opts, anthropicoption.WithHTTPClient(c.HTTPClient))
	}
	c.anthropicSDK = anthropic.NewClient(opts...)
	return nil
}

func resolvedAnthropicBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		base = defaultAnthropicBaseURL
	}

	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") {
		base = strings.TrimSuffix(base, "/v1")
	}
	base = strings.TrimRight(base, "/")
	return base + "/"
}

func (c *Client) chatAnthropics(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if c == nil {
		return nil, errors.New("nil client")
	}
	if err := c.ensureAnthropicSDK(); err != nil {
		return nil, err
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(c.Model)
	}
	if model == "" {
		return nil, errors.New("model is required")
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 && c.MaxTokens > 0 {
		maxTokens = c.MaxTokens
	}
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxTokens
	}

	system, messages, err := toAnthropicMessages(req.Messages)
	if err != nil {
		return nil, err
	}

	params := anthropic.MessageNewParams{
		MaxTokens: int64(maxTokens),
		Model:     anthropic.Model(model),
		Messages:  messages,
	}
	if len(system) > 0 {
		params.System = system
	}
	if req.Temperature != 0 {
		params.Temperature = anthropic.Float(float64(req.Temperature))
	}
	if len(req.Tools) > 0 {
		tools, err := toAnthropicTools(req.Tools)
		if err != nil {
			return nil, err
		}
		params.Tools = tools
	}

	start := time.Now()
	resp, err := c.anthropicSDK.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}
	return fromAnthropicMessage(resp, start), nil
}

func toAnthropicMessages(msgs []Message) ([]anthropic.TextBlockParam, []anthropic.MessageParam, error) {
	if len(msgs) == 0 {
		return nil, nil, nil
	}

	var (
		systemTexts []string
		cursor      int
	)
	for cursor < len(msgs) && strings.EqualFold(strings.TrimSpace(msgs[cursor].Role), "system") {
		if strings.TrimSpace(msgs[cursor].Content) != "" {
			systemTexts = append(systemTexts, msgs[cursor].Content)
		}
		cursor++
	}

	system := ([]anthropic.TextBlockParam)(nil)
	if len(systemTexts) > 0 {
		system = []anthropic.TextBlockParam{{Text: strings.Join(systemTexts, "\n\n")}}
	}

	out := make([]anthropic.MessageParam, 0, len(msgs)-cursor)
	pendingToolResults := ([]anthropic.ContentBlockParamUnion)(nil)
	flushToolResults := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		out = append(out, anthropic.NewUserMessage(pendingToolResults...))
		pendingToolResults = nil
	}

	for ; cursor < len(msgs); cursor++ {
		m := msgs[cursor]
		role := strings.TrimSpace(strings.ToLower(m.Role))
		switch role {
		case "tool":
			toolCallID := strings.TrimSpace(m.ToolCallID)
			if toolCallID == "" {
				return nil, nil, errors.New("tool message missing tool_call_id")
			}
			isError := strings.HasPrefix(m.Content, "ERROR:")
			pendingToolResults = append(pendingToolResults, anthropic.NewToolResultBlock(toolCallID, m.Content, isError))
		case "user":
			flushToolResults()
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case "assistant":
			flushToolResults()
			blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(m.ToolCalls))
			if strings.TrimSpace(m.Content) != "" || len(m.ToolCalls) == 0 {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			if len(m.ToolCalls) > 0 {
				toolBlocks, err := toAnthropicToolUseBlocks(m.ToolCalls)
				if err != nil {
					return nil, nil, err
				}
				blocks = append(blocks, toolBlocks...)
			}
			if len(blocks) == 0 {
				blocks = append(blocks, anthropic.NewTextBlock(""))
			}
			out = append(out, anthropic.NewAssistantMessage(blocks...))
		case "system":
			flushToolResults()
			// Anthropic doesn't support "system" role within messages; best-effort:
			// keep ordering by sending as a user message.
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		default:
			if role == "" {
				return nil, nil, errors.New("message role is required")
			}
			return nil, nil, fmt.Errorf("unsupported message role: %q", m.Role)
		}
	}
	flushToolResults()
	return system, out, nil
}

func toAnthropicToolUseBlocks(calls []ToolCall) ([]anthropic.ContentBlockParamUnion, error) {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(calls))
	for _, call := range calls {
		callType := strings.TrimSpace(strings.ToLower(call.Type))
		if callType != "" && callType != "function" {
			return nil, fmt.Errorf("unsupported tool call type: %q", call.Type)
		}
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			return nil, errors.New("tool call missing function name")
		}

		var input any = map[string]any{}
		args := strings.TrimSpace(call.Function.Arguments)
		if args != "" {
			if err := json.Unmarshal([]byte(args), &input); err != nil {
				input = map[string]any{"__raw": args}
			}
		}

		out = append(out, anthropic.NewToolUseBlock(call.ID, input, name))
	}
	return out, nil
}

func toAnthropicTools(tools []ToolDefinition) ([]anthropic.ToolUnionParam, error) {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		typ := strings.TrimSpace(strings.ToLower(t.Type))
		if typ != "" && typ != "function" {
			return nil, fmt.Errorf("unsupported tool type: %q", t.Type)
		}

		schema, err := toAnthropicToolInputSchema(t.Function.Parameters)
		if err != nil {
			return nil, err
		}

		tool := anthropic.ToolParam{
			Name:        t.Function.Name,
			InputSchema: schema,
		}
		if desc := strings.TrimSpace(t.Function.Description); desc != "" {
			tool.Description = anthropic.String(desc)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return out, nil
}

func toAnthropicToolInputSchema(v any) (anthropic.ToolInputSchemaParam, error) {
	m, err := toJSONSchemaMap(v)
	if err != nil {
		return anthropic.ToolInputSchemaParam{}, err
	}

	out := anthropic.ToolInputSchemaParam{}
	out.Type = out.Type.Default()
	extras := make(map[string]any)
	for key, value := range m {
		switch key {
		case "properties":
			out.Properties = value
		case "required":
			out.Required = toStringSlice(value)
		case "type":
			// SDK defaults to "object" when omitted.
		default:
			extras[key] = value
		}
	}
	if len(extras) > 0 {
		out.ExtraFields = extras
	}
	return out, nil
}

func toJSONSchemaMap(v any) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	switch p := v.(type) {
	case map[string]any:
		return p, nil
	case json.RawMessage:
		if len(p) == 0 {
			return map[string]any{}, nil
		}
		var out map[string]any
		if err := json.Unmarshal(p, &out); err != nil {
			return nil, fmt.Errorf("parse tool schema: %w", err)
		}
		return out, nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal tool schema: %w", err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("parse tool schema: %w", err)
		}
		return out, nil
	}
}

func toStringSlice(v any) []string {
	switch raw := v.(type) {
	case []string:
		return append([]string{}, raw...)
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			s, ok := item.(string)
			if !ok {
				continue
			}
			out = append(out, s)
		}
		return out
	default:
		return nil
	}
}

func fromAnthropicMessage(msg *anthropic.Message, startedAt time.Time) *ChatResponse {
	if msg == nil {
		return &ChatResponse{
			Choices: []Choice{{Index: 0, Message: Message{Role: "assistant"}}},
		}
	}

	var (
		content   strings.Builder
		toolCalls []ToolCall
	)
	for _, block := range msg.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.TextBlock:
			if content.Len() > 0 {
				content.WriteString("\n")
			}
			content.WriteString(variant.Text)
		case anthropic.ToolUseBlock:
			toolCalls = append(toolCalls, ToolCall{
				ID:   variant.ID,
				Type: "function",
				Function: ToolCallFunction{
					Name:      variant.Name,
					Arguments: string(variant.Input),
				},
			})
		default:
			// Ignore unknown block variants.
		}
	}

	role := "assistant"
	if msg.Role != "" {
		role = string(msg.Role)
	}

	return &ChatResponse{
		ID:      msg.ID,
		Object:  string(msg.Type),
		Created: startedAt.Unix(),
		Model:   string(msg.Model),
		Choices: []Choice{{
			Index: int(0),
			Message: Message{
				Role:      role,
				Content:   content.String(),
				ToolCalls: toolCalls,
			},
			FinishReason: string(msg.StopReason),
		}},
		Usage: Usage{
			PromptTokens:     int(msg.Usage.InputTokens),
			CompletionTokens: int(msg.Usage.OutputTokens),
			TotalTokens:      int(msg.Usage.InputTokens + msg.Usage.OutputTokens),
		},
	}
}

func (c *Client) httpClient() *http.Client {
	if c == nil || c.HTTPClient == nil {
		return http.DefaultClient
	}
	return c.HTTPClient
}
