package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/skills"
	"test_skill_agent/internal/tools"
)

type Agent struct {
	Client       *llm.Client
	Tools        *tools.Registry
	SkillsDir    string
	SkillIndex   []skills.Skill
	Temperature  float32
	SystemPrompt string
}

func New(client *llm.Client, registry *tools.Registry, skillsDir string) (*Agent, error) {
	a := &Agent{
		Client:    client,
		Tools:     registry,
		SkillsDir: skillsDir,
	}
	if err := a.ReloadSkills(); err != nil {
		return nil, err
	}
	a.SystemPrompt = a.buildSystemPrompt()
	return a, nil
}

func (a *Agent) ReloadSkills() error {
	list, err := skills.LoadSkills(a.SkillsDir)
	if err != nil {
		return err
	}
	a.SkillIndex = list
	return nil
}

func (a *Agent) buildSystemPrompt() string {
	var b strings.Builder
	b.WriteString("You are a local coding agent with tool access. Use tools for filesystem operations instead of guessing.\n")
	b.WriteString("When the user requests skill management, use skill_create or skill_install.\n")
	b.WriteString("If a user mentions a skill name or uses $SkillName, load it with skill_load before proceeding.\n")
	if len(a.SkillIndex) == 0 {
		b.WriteString("Available skills: (none).\n")
		return b.String()
	}
	b.WriteString("Available skills:\n")
	for _, s := range a.SkillIndex {
		if s.Description != "" {
			b.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
		} else {
			b.WriteString(fmt.Sprintf("- %s\n", s.Name))
		}
	}
	return b.String()
}

func (a *Agent) RunInteractive(ctx context.Context, in io.Reader, out io.Writer) error {
	if a.Client == nil {
		return fmt.Errorf("llm client is nil")
	}
	systemMsg := llm.Message{Role: "system", Content: a.SystemPrompt}
	history := make([]llm.Message, 0, 64)
	scanner := bufio.NewScanner(in)
	printer := newToolPrinter(out)

	for {
		fmt.Fprint(out, "> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == "/exit" || text == "/quit" {
			return nil
		}

		skillMsgs := a.skillMessagesForInput(text)

		turnHistory := []llm.Message{{Role: "user", Content: text}}
		reqMessages := append([]llm.Message{}, systemMsg)
		reqMessages = append(reqMessages, history...)
		reqMessages = append(reqMessages, skillMsgs...)
		reqMessages = append(reqMessages, turnHistory...)

		for {
			resp, err := a.Client.Chat(ctx, llm.ChatRequest{
				Messages:    reqMessages,
				Tools:       a.Tools.Definitions(),
				Temperature: a.Temperature,
			})
			if err != nil {
				return err
			}
			msg := resp.Choices[0].Message
			turnHistory = append(turnHistory, msg)
			reqMessages = append(reqMessages, msg)

			if len(msg.ToolCalls) == 0 {
				if msg.Content != "" {
					fmt.Fprintln(out, msg.Content)
				}
				history = append(history, turnHistory...)
				break
			}

			for _, call := range msg.ToolCalls {
				printer.printToolCall(call.Function.Name, call.Function.Arguments)
				start := time.Now()
				result, err := a.callTool(ctx, call)
				printer.printToolResult(call.Function.Name, result, err, time.Since(start))
				toolMsg := llm.Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Content:    result,
				}
				if err != nil {
					toolMsg.Content = "ERROR: " + err.Error()
				}
				turnHistory = append(turnHistory, toolMsg)
				reqMessages = append(reqMessages, toolMsg)

				if call.Function.Name == "skill_create" || call.Function.Name == "skill_install" {
					_ = a.ReloadSkills()
					a.SystemPrompt = a.buildSystemPrompt()
					systemMsg = llm.Message{Role: "system", Content: a.SystemPrompt}
				}
			}
		}
	}
}

func (a *Agent) callTool(ctx context.Context, call llm.ToolCall) (string, error) {
	args := json.RawMessage(call.Function.Arguments)
	return a.Tools.Call(ctx, call.Function.Name, args)
}

func (a *Agent) skillMessagesForInput(input string) []llm.Message {
	if len(a.SkillIndex) == 0 {
		return nil
	}
	lower := strings.ToLower(input)
	msgs := make([]llm.Message, 0, 2)
	for _, s := range a.SkillIndex {
		nameLower := strings.ToLower(s.Name)
		if strings.Contains(lower, "$"+nameLower) || strings.Contains(lower, nameLower) {
			body, err := skills.LoadSkillBody(s.SkillPath)
			if err != nil {
				continue
			}
			content := fmt.Sprintf("Skill: %s\n%s", s.Name, body)
			msgs = append(msgs, llm.Message{Role: "system", Content: content})
		}
	}
	return msgs
}
