package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"test_skill_agent/internal/llm"
)

type turnToolPolicy struct {
	Mode              PromptMode
	ChatToolMode      ChatToolMode
	UserText          string
	AllowBlockingWait bool
	progressCalls     int
}

func newTurnToolPolicy(mode PromptMode, chatToolMode ChatToolMode, userText string) turnToolPolicy {
	p := turnToolPolicy{
		Mode:         mode,
		ChatToolMode: chatToolMode,
		UserText:     strings.TrimSpace(userText),
	}
	if mode != PromptModeChat {
		p.AllowBlockingWait = true
		return p
	}
	p.AllowBlockingWait = userExplicitlyRequestsBlockingWait(p.UserText) && !userExplicitlyDeclinesBlockingWait(p.UserText)
	return p
}

func (p turnToolPolicy) toolVisible(name string) bool {
	tool := strings.TrimSpace(name)
	if tool == "" {
		return false
	}
	if p.Mode != PromptModeChat {
		return true
	}

	if tool == "agent_wait" || tool == "agent_signal_wait" {
		return p.AllowBlockingWait
	}

	if p.ChatToolMode == ChatToolModeDispatcher {
		return strings.HasPrefix(tool, "agent_") || tool == "subagents"
	}

	return true
}

func (p *turnToolPolicy) allowTool(toolName string) error {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return errors.New("tool name is empty")
	}
	if p.Mode != PromptModeChat {
		return nil
	}

	if name == "agent_wait" || name == "agent_signal_wait" {
		if p.AllowBlockingWait {
			return nil
		}
		return errors.New("blocking waits are disabled in chat mode unless the user explicitly requests waiting")
	}

	// Dispatcher policy: primary agent should only operate the control-plane.
	if p.ChatToolMode == ChatToolModeDispatcher {
		if strings.HasPrefix(name, "agent_") || name == "subagents" {
			if !p.AllowBlockingWait && isProgressTool(name) {
				const maxProgressCalls = 3
				if p.progressCalls >= maxProgressCalls {
					return fmt.Errorf("too many progress polls in one turn (max=%d). Ask the user whether to wait/block, or use agent_progress once (omit agent_id to snapshot all agents)", maxProgressCalls)
				}
				p.progressCalls++
			}
			return nil
		}
		return fmt.Errorf("tool %q is disabled in chat/dispatcher mode; spawn a child agent to do real work", name)
	}

	return nil
}

func userExplicitlyRequestsBlockingWait(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	compact := strings.ReplaceAll(lower, " ", "")
	compact = strings.ReplaceAll(compact, "\t", "")

	// These phrases are intentionally conservative: only enable blocking when the
	// user clearly asks to wait until completion.
	phrases := []string{
		"等待完成", "等完成", "等结果", "等到完成", "等到结束", "等到跑完",
		"直到结束", "直到完成", "一直等", "一直等待", "持续等待",
		"等跑完", "等它结束", "等它完成", "完成后告诉我", "结束后告诉我",
		"block until", "wait until", "wait for completion", "until done", "until finished", "keep waiting", "keep checking until",
	}
	for _, phrase := range phrases {
		if phrase == "" {
			continue
		}
		if strings.Contains(lower, phrase) || strings.Contains(compact, phrase) {
			return true
		}
	}
	return false
}

func userExplicitlyDeclinesBlockingWait(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	compact := strings.ReplaceAll(lower, " ", "")
	compact = strings.ReplaceAll(compact, "\t", "")
	compact = strings.ReplaceAll(compact, "\n", "")
	compact = strings.ReplaceAll(compact, "\r", "")

	phrases := []string{
		"不要等待", "不用等待", "不必等待", "无需等待", "不需要等待", "别等待",
		"不要等", "不用等", "不必等", "无需等", "不需要等", "别等",
		"不用等完成", "不要等完成", "不用等结果", "不要等结果",
		"任务下发后就返回", "下发后就返回", "先直接返回", "先返回", "立即返回", "直接返回",
		"不要阻塞", "不必阻塞", "无需阻塞", "不阻塞", "异步", "asynchronous", "async",
		"don't wait", "do not wait", "no need to wait", "no need waiting",
		"don't block", "do not block", "non-blocking", "return immediately",
	}
	for _, phrase := range phrases {
		if phrase == "" {
			continue
		}
		if strings.Contains(lower, phrase) || strings.Contains(compact, phrase) {
			return true
		}
	}
	return false
}

func isProgressTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "agent_state", "agent_progress", "agent_events", "agent_inspect", "agent_result", "subagents":
		return true
	default:
		return false
	}
}

func (a *Agent) callToolWithPolicy(ctx context.Context, call llm.ToolCall, policy *turnToolPolicy) (string, error) {
	if policy == nil {
		return "", errors.New("nil tool policy")
	}
	if err := policy.allowTool(call.Function.Name); err != nil {
		return "", err
	}
	args := json.RawMessage(call.Function.Arguments)
	return a.Tools.Call(ctx, call.Function.Name, args)
}
