package agent

import "testing"

func TestTurnToolPolicy_DispatcherBlocksNonAgentTools(t *testing.T) {
	p := newTurnToolPolicy(PromptModeChat, ChatToolModeDispatcher, "查看子agent进度")

	if err := p.allowTool("agent_state"); err != nil {
		t.Fatalf("expected agent_state allowed, got error: %v", err)
	}
	if err := p.allowTool("subagents"); err != nil {
		t.Fatalf("expected subagents allowed, got error: %v", err)
	}
	if err := p.allowTool("list_files"); err == nil {
		t.Fatalf("expected list_files blocked in dispatcher mode")
	}
}

func TestTurnToolPolicy_BlocksWaitUnlessExplicit(t *testing.T) {
	p := newTurnToolPolicy(PromptModeChat, ChatToolModeDispatcher, "看一下子agent状态")
	if err := p.allowTool("agent_wait"); err == nil {
		t.Fatalf("expected agent_wait blocked without explicit waiting request")
	}
}

func TestTurnToolPolicy_AllowsWaitWhenExplicit(t *testing.T) {
	p := newTurnToolPolicy(PromptModeChat, ChatToolModeDispatcher, "等完成再告诉我结果")
	if err := p.allowTool("agent_wait"); err != nil {
		t.Fatalf("expected agent_wait allowed with explicit waiting request, got: %v", err)
	}
}

func TestTurnToolPolicy_DontWaitOverridesWaitPhrase(t *testing.T) {
	p := newTurnToolPolicy(PromptModeChat, ChatToolModeDispatcher, "不用等完成再告诉我结果")
	if err := p.allowTool("agent_wait"); err == nil {
		t.Fatalf("expected agent_wait blocked when user explicitly says not to wait")
	}
}

func TestTurnToolPolicy_LimitsProgressPollingWhenNonBlocking(t *testing.T) {
	p := newTurnToolPolicy(PromptModeChat, ChatToolModeDispatcher, "看一下进度")
	for i := 0; i < 3; i++ {
		if err := p.allowTool("agent_progress"); err != nil {
			t.Fatalf("expected agent_progress allowed at i=%d, got: %v", i, err)
		}
	}
	if err := p.allowTool("agent_progress"); err == nil {
		t.Fatalf("expected agent_progress blocked after max polls")
	}
}
