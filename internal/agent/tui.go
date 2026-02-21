package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
)

const (
	tuiPrimaryAgentID          = "primary"
	tuiPrimaryAgentTaskDefault = "primary chat session"
	tuiHistoryFileName         = "history.jsonl"
)

type TUIMode string

const (
	TUIModeTUI   TUIMode = "tui"
	TUIModePlain TUIMode = "plain"
)

type TUIOptions struct {
	Coordinator *multiagent.Coordinator
}

func (a *Agent) RunInteractiveTUI(ctx context.Context, in io.Reader, out io.Writer, opts TUIOptions) error {
	if a == nil || a.Client == nil {
		return errors.New("agent is not configured")
	}
	if opts.Coordinator == nil {
		return errors.New("tui requires a multi-agent coordinator")
	}

	if f, ok := out.(*os.File); ok {
		if !term.IsTerminal(int(f.Fd())) {
			return fmt.Errorf("stdout is not a TTY; use --ui=plain")
		}
	}

	model := newTUIModel(ctx, a, opts.Coordinator)
	prog := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithInput(in),
		tea.WithOutput(out),
	)
	_, err := prog.Run()
	return err
}

type tuiFocusMode int

const (
	tuiFocusInput tuiFocusMode = iota
	tuiFocusBrowse
)

type tuiModel struct {
	ctx   context.Context
	agent *Agent
	coord *multiagent.Coordinator

	width  int
	height int

	focus tuiFocusMode

	sessions     []multiagent.RunManifest
	sessionIndex int

	sessionData map[string]*tuiSessionData

	agentIDs   []string
	agentIndex int

	input    textinput.Model
	viewport viewport.Model

	toolKeys       []string
	toolLineOffset []int
	toolCursor     int
	expandedTools  map[string]bool

	loading bool
	busy    bool
	notice  string
	fatal   error
}

type tuiSessionData struct {
	RunID       string
	HistoryPath string
	History     []llm.Message
	Busy        bool
}

type tuiInitMsg struct{}

type tuiSessionsLoadedMsg struct {
	Runs []multiagent.RunManifest
	Err  error
}

type tuiSessionSelectedMsg struct {
	RunID string
}

type tuiRefreshMsg struct{}

type tuiSessionCreatedMsg struct {
	Run multiagent.RunManifest
	Err error
}

type tuiTurnResultMsg struct {
	RunID    string
	Messages []llm.Message
	Err      error
}

func newTUIModel(ctx context.Context, a *Agent, coord *multiagent.Coordinator) tuiModel {
	inp := textinput.New()
	inp.Placeholder = "Type a message…"
	inp.Prompt = "› "
	inp.CharLimit = 0
	inp.Focus()

	vp := viewport.New(0, 0)
	vp.SetContent("")

	return tuiModel{
		ctx:           ctx,
		agent:         a,
		coord:         coord,
		focus:         tuiFocusInput,
		input:         inp,
		viewport:      vp,
		sessionData:   make(map[string]*tuiSessionData),
		expandedTools: make(map[string]bool),
		toolCursor:    -1,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return tuiInitMsg{} },
		tuiLoadSessionsCmd(m.coord),
		tuiTickCmd(),
	)
}

func tuiTickCmd() tea.Cmd {
	return tea.Tick(600*time.Millisecond, func(time.Time) tea.Msg { return tuiRefreshMsg{} })
}

func tuiLoadSessionsCmd(coord *multiagent.Coordinator) tea.Cmd {
	return func() tea.Msg {
		runs, err := coord.ListRuns()
		return tuiSessionsLoadedMsg{Runs: runs, Err: err}
	}
}

func tuiCreateSessionCmd(coord *multiagent.Coordinator) tea.Cmd {
	return func() tea.Msg {
		run, err := coord.CreateRun("", map[string]any{"source": "tui"})
		return tuiSessionCreatedMsg{Run: run, Err: err}
	}
}

func tuiMCPReloadCmd(ctx context.Context, a *Agent, runID string) tea.Cmd {
	return func() tea.Msg {
		msg, err := a.reloadMCP(ctx)
		content := msg
		if err != nil {
			content = "MCP reload failed: " + err.Error()
		} else if strings.TrimSpace(content) == "" {
			content = "mcp reload complete"
		}
		return tuiTurnResultMsg{
			RunID: runID,
			Messages: []llm.Message{
				{Role: "system", Content: content},
			},
			Err: err,
		}
	}
}

func tuiRunTurnCmd(ctx context.Context, a *Agent, runID string, userText string, baseHistory []llm.Message) tea.Cmd {
	return func() tea.Msg {
		msgs, err := runTUITurn(ctx, a, runID, userText, baseHistory)
		return tuiTurnResultMsg{RunID: runID, Messages: msgs, Err: err}
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.rerender()
		return m, nil
	case tuiInitMsg:
		m.loading = true
		return m, nil
	case tuiSessionsLoadedMsg:
		m.loading = false
		if msg.Err != nil {
			m.fatal = msg.Err
			return m, nil
		}
		selected := m.currentRunID()
		m.sessions = msg.Runs
		if len(m.sessions) == 0 {
			run, err := m.coord.CreateRun("", map[string]any{"source": "tui"})
			if err != nil {
				m.fatal = err
				return m, nil
			}
			m.sessions = []multiagent.RunManifest{run}
		}

		m.sessionIndex = 0
		if selected != "" {
			for i, run := range m.sessions {
				if run.ID == selected {
					m.sessionIndex = i
					break
				}
			}
		}
		m.ensureSessionLoaded(m.currentRunID())
		m.ensurePrimaryAgent(m.currentRunID())
		m.refreshAgentIDs()
		m.rerender()
		return m, nil
	case tuiRefreshMsg:
		if m.currentRunID() == "" {
			return m, tuiTickCmd()
		}
		m.refreshAgentIDs()
		m.rerender()
		return m, tuiTickCmd()
	case tuiSessionCreatedMsg:
		if msg.Err != nil {
			m.notice = msg.Err.Error()
			return m, nil
		}
		m.sessions = append([]multiagent.RunManifest{msg.Run}, m.sessions...)
		m.sessionIndex = 0
		m.ensureSessionLoaded(m.currentRunID())
		m.ensurePrimaryAgent(m.currentRunID())
		m.refreshAgentIDs()
		m.notice = ""
		m.rerender()
		return m, nil
	case tuiSessionSelectedMsg:
		if msg.RunID == "" {
			return m, nil
		}
		for i, run := range m.sessions {
			if run.ID == msg.RunID {
				m.sessionIndex = i
				m.ensureSessionLoaded(msg.RunID)
				m.ensurePrimaryAgent(msg.RunID)
				m.refreshAgentIDs()
				m.rerender()
				break
			}
		}
		return m, nil
	case tuiTurnResultMsg:
		m.ensureSessionLoaded(msg.RunID)
		sess := m.sessionData[msg.RunID]
		for _, item := range msg.Messages {
			sess.History = append(sess.History, item)
			_ = appendJSONL(sess.HistoryPath, item)
		}
		m.busy = false
		sess.Busy = false
		if msg.Err != nil {
			m.notice = msg.Err.Error()
		} else {
			m.notice = ""
		}
		m.rerender()
		return m, nil
	case tea.KeyMsg:
		handled, cmd := m.handleKey(msg)
		if handled {
			return m, cmd
		}
		if m.focus == tuiFocusBrowse {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		if m.focus == tuiFocusInput && !m.busy {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		return m, nil
	default:
		return m, nil
	}
}

func (m *tuiModel) handleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c":
		return true, tea.Quit
	case "ctrl+r":
		return true, tuiLoadSessionsCmd(m.coord)
	case "ctrl+n":
		return true, tuiCreateSessionCmd(m.coord)
	case "esc":
		if m.focus == tuiFocusInput {
			m.focus = tuiFocusBrowse
		} else {
			m.focus = tuiFocusInput
		}
		m.rerender()
		return true, nil
	case "tab":
		if len(m.agentIDs) > 0 {
			m.agentIndex = (m.agentIndex + 1) % len(m.agentIDs)
		}
		m.toolCursor = 0
		m.rerender()
		return true, nil
	case "shift+up", "alt+up":
		m.selectSession(-1)
		return true, nil
	case "shift+down", "alt+down":
		m.selectSession(1)
		return true, nil
	case "up", "k":
		if m.focus == tuiFocusBrowse {
			m.moveToolCursor(-1)
			return true, nil
		}
	case "down", "j":
		if m.focus == tuiFocusBrowse {
			m.moveToolCursor(1)
			return true, nil
		}
	case "enter":
		if m.focus == tuiFocusBrowse {
			m.toggleTool()
			return true, nil
		}
		if m.focus == tuiFocusInput {
			cmd := m.submitInput()
			return true, cmd
		}
	case "pgup":
		m.viewport.PageUp()
		return true, nil
	case "pgdown":
		m.viewport.PageDown()
		return true, nil
	}
	return false, nil
}

func (m tuiModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	if m.fatal != nil {
		return lipgloss.NewStyle().Padding(1, 2).Render("fatal: " + m.fatal.Error())
	}
	if m.loading {
		return lipgloss.NewStyle().Padding(1, 2).Render("loading…")
	}

	leftW, rightW := m.leftRightWidths()
	midW := max(0, m.width-leftW-rightW)

	left := m.renderSessions(leftW, m.height)
	center := m.renderCenter(midW, m.height)
	right := m.renderStatus(rightW, m.height)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
}

func (m *tuiModel) resize() {
	leftW, rightW := m.leftRightWidths()
	midW := max(0, m.width-leftW-rightW)

	headerH := 3
	inputH := 1
	vpH := max(0, m.height-headerH-inputH)
	m.viewport.Width = max(0, midW-2)
	m.viewport.Height = vpH
}

func (m *tuiModel) leftRightWidths() (leftW int, rightW int) {
	leftW = clamp(20, m.width/5, 34)
	rightW = clamp(22, m.width/4, 42)
	return leftW, rightW
}

func (m *tuiModel) currentRunID() string {
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessions) {
		return ""
	}
	return m.sessions[m.sessionIndex].ID
}

func (m *tuiModel) currentAgentID() string {
	if m.agentIndex < 0 || m.agentIndex >= len(m.agentIDs) {
		return tuiPrimaryAgentID
	}
	return m.agentIDs[m.agentIndex]
}

func (m *tuiModel) ensureSessionLoaded(runID string) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	if _, ok := m.sessionData[runID]; ok {
		return
	}
	agentDir := m.coord.AgentDir(runID, tuiPrimaryAgentID)
	historyPath := filepath.Join(agentDir, tuiHistoryFileName)
	history, _ := readJSONL[llm.Message](historyPath, 2000)
	m.sessionData[runID] = &tuiSessionData{
		RunID:       runID,
		HistoryPath: historyPath,
		History:     history,
	}
}

func (m *tuiModel) ensurePrimaryAgent(runID string) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	state, stateErr := m.coord.ReadAgentState(runID, tuiPrimaryAgentID)
	if stateErr != nil {
		return
	}
	now := time.Now().UTC()
	state.Status = multiagent.StatusRunning
	state.PID = os.Getpid()
	if state.StartedAt.IsZero() {
		state.StartedAt = now
	}
	state.UpdatedAt = now
	_ = m.coord.UpdateAgentState(runID, state)
}

func (m *tuiModel) createPrimaryAgent(runID string) {
	if strings.TrimSpace(runID) == "" {
		return
	}
	if _, err := m.coord.ReadAgentState(runID, tuiPrimaryAgentID); err == nil {
		m.ensurePrimaryAgent(runID)
		return
	}
	_, _, createErr := m.coord.CreateAgent(runID, multiagent.AgentSpec{
		ID:   tuiPrimaryAgentID,
		Task: tuiPrimaryAgentTaskDefault,
		Metadata: map[string]any{
			"type": "primary",
		},
	})
	if createErr != nil && !strings.Contains(strings.ToLower(createErr.Error()), "already exists") {
		return
	}
	m.ensurePrimaryAgent(runID)
}

func (m *tuiModel) refreshAgentIDs() {
	runID := m.currentRunID()
	if runID == "" {
		return
	}
	states, err := m.coord.ListAgentStates(runID)
	if err != nil {
		return
	}
	ids := make([]string, 0, len(states)+1)
	ids = append(ids, tuiPrimaryAgentID)
	for _, st := range states {
		if st.AgentID == tuiPrimaryAgentID {
			continue
		}
		ids = append(ids, st.AgentID)
	}
	sort.Strings(ids[1:])

	current := m.currentAgentID()
	m.agentIDs = ids
	m.agentIndex = 0
	for i, id := range m.agentIDs {
		if id == current {
			m.agentIndex = i
			break
		}
	}
}

func (m *tuiModel) selectSession(delta int) {
	if len(m.sessions) == 0 || delta == 0 {
		return
	}
	next := m.sessionIndex + delta
	for next < 0 {
		next += len(m.sessions)
	}
	next = next % len(m.sessions)
	if next == m.sessionIndex {
		return
	}
	m.sessionIndex = next
	m.ensureSessionLoaded(m.currentRunID())
	m.ensurePrimaryAgent(m.currentRunID())
	m.refreshAgentIDs()
	m.toolCursor = 0
	m.rerender()
}

func (m *tuiModel) moveToolCursor(delta int) {
	if len(m.toolKeys) == 0 || delta == 0 {
		m.toolCursor = -1
		return
	}
	if m.toolCursor < 0 {
		m.toolCursor = 0
	}
	next := m.toolCursor + delta
	for next < 0 {
		next += len(m.toolKeys)
	}
	next = next % len(m.toolKeys)
	m.toolCursor = next
	m.rerender()
}

func (m *tuiModel) toggleTool() {
	if m.toolCursor < 0 || m.toolCursor >= len(m.toolKeys) {
		return
	}
	key := m.toolKeys[m.toolCursor]
	if key == "" {
		return
	}
	m.expandedTools[key] = !m.expandedTools[key]
	m.rerender()
}

func (m *tuiModel) submitInput() tea.Cmd {
	if m.busy {
		return nil
	}
	runID := m.currentRunID()
	if strings.TrimSpace(runID) == "" {
		return nil
	}
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	m.input.SetValue("")

	switch text {
	case "/exit", "/quit":
		return tea.Quit
	case "/mcp reload", "/mcp-reload":
		m.busy = true
		m.ensureSessionLoaded(runID)
		m.sessionData[runID].Busy = true
		m.rerender()
		return tuiMCPReloadCmd(m.ctx, m.agent, runID)
	}
	if m.agent.shouldTriggerNaturalLanguageMCPReload(text) {
		m.busy = true
		m.ensureSessionLoaded(runID)
		m.sessionData[runID].Busy = true
		m.rerender()
		return tuiMCPReloadCmd(m.ctx, m.agent, runID)
	}

	m.ensureSessionLoaded(runID)
	m.createPrimaryAgent(runID)
	sess := m.sessionData[runID]
	base := append([]llm.Message(nil), sess.History...)

	userMsg := llm.Message{Role: "user", Content: text}
	sess.History = append(sess.History, userMsg)
	_ = appendJSONL(sess.HistoryPath, userMsg)

	sess.Busy = true
	m.busy = true
	m.notice = ""
	m.rerender()
	return tuiRunTurnCmd(m.ctx, m.agent, runID, text, base)
}

func runTUITurn(ctx context.Context, a *Agent, runID string, userText string, baseHistory []llm.Message) ([]llm.Message, error) {
	if a == nil || a.Client == nil {
		return nil, errors.New("agent is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return nil, nil
	}

	systemMsg := llm.Message{Role: "system", Content: a.SystemPrompt}
	sessionMsg := llm.Message{
		Role: "system",
		Content: fmt.Sprintf(
			"Session context: run_id=%s. Use this run_id for agent_* tools unless the user specifies otherwise.",
			strings.TrimSpace(runID),
		),
	}

	skillMsgs := a.skillMessagesForInput(userText)
	policy := newTurnToolPolicy(a.PromptMode, a.ChatToolMode, userText)

	toolDefs := a.Tools.Definitions()
	if a.PromptMode == PromptModeChat {
		filtered := make([]llm.ToolDefinition, 0, len(toolDefs))
		for _, def := range toolDefs {
			if policy.toolVisible(def.Function.Name) {
				filtered = append(filtered, def)
			}
		}
		toolDefs = filtered
	}

	turnHistory := []llm.Message{{Role: "user", Content: userText}}
	reqMessages := append([]llm.Message{}, systemMsg, sessionMsg)
	reqMessages = append(reqMessages, baseHistory...)
	reqMessages = append(reqMessages, skillMsgs...)
	reqMessages = append(reqMessages, turnHistory...)

	for {
		resp, err := a.Client.Chat(ctx, llm.ChatRequest{
			Messages:    reqMessages,
			Tools:       toolDefs,
			Temperature: a.Temperature,
		})
		if err != nil {
			return nil, err
		}
		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) > 0 {
			for i := range msg.ToolCalls {
				msg.ToolCalls[i].Function.Arguments = injectRunIDForTool(
					msg.ToolCalls[i].Function.Name,
					msg.ToolCalls[i].Function.Arguments,
					runID,
				)
			}
		}
		turnHistory = append(turnHistory, msg)
		reqMessages = append(reqMessages, msg)

		if len(msg.ToolCalls) == 0 {
			break
		}

		needsAutoMCPReload := false
		for _, call := range msg.ToolCalls {
			start := time.Now()
			result, callErr := a.callToolWithPolicy(ctx, call, &policy)
			_ = time.Since(start)

			toolMsg := llm.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			}
			if callErr != nil {
				toolMsg.Content = "ERROR: " + callErr.Error()
			}
			turnHistory = append(turnHistory, toolMsg)
			reqMessages = append(reqMessages, toolMsg)

			if a.shouldTriggerAutoMCPReloadAfterToolCall(call, callErr) {
				needsAutoMCPReload = true
			}

			if call.Function.Name == "skill_create" || call.Function.Name == "skill_install" {
				_ = a.ReloadSkills()
				a.SystemPrompt = a.buildSystemPrompt()
				systemMsg = llm.Message{Role: "system", Content: a.SystemPrompt}
				reqMessages = append(reqMessages, systemMsg)
			}
		}

		if needsAutoMCPReload {
			reloadMsg, err := a.reloadMCP(ctx)
			contextMsg := ""
			if err != nil {
				contextMsg = fmt.Sprintf("System event: MCP auto-reload failed after MCP-related updates: %v", err)
			} else {
				contextMsg = "System event: MCP auto-reload completed after MCP-related updates.\n" + reloadMsg
			}
			if strings.TrimSpace(contextMsg) != "" {
				autoMsg := llm.Message{Role: "system", Content: contextMsg}
				turnHistory = append(turnHistory, autoMsg)
				reqMessages = append(reqMessages, autoMsg)
			}
		}
	}

	// Exclude the user message (already appended by the UI) from the returned messages.
	if len(turnHistory) <= 1 {
		return nil, nil
	}
	return append([]llm.Message(nil), turnHistory[1:]...), nil
}

func injectRunIDForTool(toolName string, rawArgs string, runID string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return rawArgs
	}
	if !strings.HasPrefix(name, "agent_") && name != "subagents" {
		return rawArgs
	}

	id := strings.TrimSpace(runID)
	if id == "" {
		return rawArgs
	}

	trimmed := strings.TrimSpace(rawArgs)
	if trimmed == "" {
		out, err := json.Marshal(map[string]any{"run_id": id})
		if err != nil {
			return rawArgs
		}
		return string(out)
	}

	if !json.Valid([]byte(trimmed)) {
		return rawArgs
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return rawArgs
	}
	if v, ok := obj["run_id"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return rawArgs
		}
	}
	obj["run_id"] = id
	out, err := json.Marshal(obj)
	if err != nil {
		return rawArgs
	}
	return string(out)
}

func (m *tuiModel) rerender() {
	runID := m.currentRunID()
	if runID == "" {
		m.viewport.SetContent("")
		return
	}
	m.toolKeys = m.toolKeys[:0]
	m.toolLineOffset = m.toolLineOffset[:0]

	contentW := m.viewport.Width
	if contentW <= 0 {
		contentW = 80
	}
	items, toolKeys, toolOffsets := m.buildCenterContent(runID, m.currentAgentID(), contentW)
	m.toolKeys = toolKeys
	m.toolLineOffset = toolOffsets
	if m.toolCursor >= len(m.toolKeys) {
		m.toolCursor = len(m.toolKeys) - 1
	}
	if m.toolCursor < 0 && len(m.toolKeys) > 0 {
		m.toolCursor = 0
	}

	m.viewport.SetContent(items)
	m.scrollToToolCursor()
}

func (m *tuiModel) scrollToToolCursor() {
	if m.toolCursor < 0 || m.toolCursor >= len(m.toolLineOffset) {
		return
	}
	line := m.toolLineOffset[m.toolCursor]
	target := max(0, line-m.viewport.Height/3)
	m.viewport.SetYOffset(target)
}

func (m *tuiModel) buildCenterContent(runID string, agentID string, width int) (content string, toolKeys []string, toolOffsets []int) {
	var lines []string
	appendLine := func(s string) {
		lines = append(lines, s)
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	appendLine(headerStyle.Render(fmt.Sprintf("Session %s", runID)))

	agentLabel := agentID
	if agentLabel == tuiPrimaryAgentID {
		agentLabel = "primary"
	}
	appendLine(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(fmt.Sprintf("Agent %s (TAB switch, Shift+↑/↓ sessions, ESC browse, Enter toggle, Ctrl+C quit)", agentLabel)))
	appendLine("")

	if agentID == tuiPrimaryAgentID {
		sess := m.sessionData[runID]
		if sess == nil {
			return strings.Join(lines, "\n"), nil, nil
		}
		base := len(lines)
		chatLines, keys, offsets := renderPrimaryHistory(runID, agentID, sess.History, width, m.expandedTools, m.toolCursor)
		lines = append(lines, chatLines...)
		for i := range offsets {
			offsets[i] += base
		}
		return strings.Join(lines, "\n"), keys, offsets
	}

	base := len(lines)
	subLines, keys, offsets := m.renderSubagent(runID, agentID, width)
	lines = append(lines, subLines...)
	for i := range offsets {
		offsets[i] += base
	}
	return strings.Join(lines, "\n"), keys, offsets
}

func renderPrimaryHistory(runID string, agentID string, history []llm.Message, width int, expanded map[string]bool, cursor int) (lines []string, toolKeys []string, toolOffsets []int) {
	if width <= 0 {
		width = 80
	}

	userStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	assistantStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	systemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)

	var out []string
	var toolLineOffsets []int
	var keys []string

	toolResults := make(map[string]string, 32)
	for _, msg := range history {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			id := strings.TrimSpace(msg.ToolCallID)
			if id == "" {
				continue
			}
			toolResults[id] = msg.Content
		}
	}

	addWrapped := func(prefix string, style lipgloss.Style, text string) {
		text = strings.TrimRight(text, "\n")
		if strings.TrimSpace(text) == "" {
			return
		}
		wrapped := wrapText(text, max(10, width-len(prefix)))
		for i, line := range strings.Split(wrapped, "\n") {
			if i == 0 {
				out = append(out, style.Render(prefix+line))
			} else {
				out = append(out, style.Render(strings.Repeat(" ", len(prefix))+line))
			}
		}
	}

	for _, msg := range history {
		switch strings.ToLower(strings.TrimSpace(msg.Role)) {
		case "user":
			addWrapped("You: ", userStyle, msg.Content)
			out = append(out, "")
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" {
				addWrapped("AI:  ", assistantStyle, msg.Content)
				out = append(out, "")
			}
			for _, call := range msg.ToolCalls {
				uiKey := toolUIKey(runID, agentID, call.ID)
				tv := &toolView{
					Key:       uiKey,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
					Result:    toolResults[call.ID],
				}
				if strings.TrimSpace(tv.Result) != "" {
					tv.Status = "ok"
					if strings.HasPrefix(strings.TrimSpace(tv.Result), "ERROR:") {
						tv.Status = "error"
					}
				}
				keys = append(keys, uiKey)
				toolLineOffsets = append(toolLineOffsets, len(out))
				out = append(out, renderToolLine(tv, width, expanded, cursor, len(keys)-1))
				if expanded[uiKey] {
					out = append(out, renderToolDetails(tv, width)...)
				}
			}
		case "system":
			addWrapped("SYS: ", systemStyle, msg.Content)
			out = append(out, "")
		}
	}

	return out, keys, toolLineOffsets
}

type toolView struct {
	Key       string
	Name      string
	Arguments string
	Result    string
	Status    string

	DurationMs int64
	Error      string
}

func renderToolLine(tv *toolView, width int, expanded map[string]bool, cursor int, toolIndex int) string {
	if tv == nil {
		return ""
	}
	prefix := "  "
	arrow := " "
	if toolIndex == cursor {
		arrow = ">"
	}
	if strings.TrimSpace(tv.Status) == "" && strings.TrimSpace(tv.Result) != "" {
		tv.Status = "ok"
	}
	status := strings.ToLower(strings.TrimSpace(tv.Status))
	if status == "" && strings.TrimSpace(tv.Result) == "" {
		status = "running"
	}
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	switch status {
	case "ok", "completed", "success":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case "error", "failed":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	}

	argHint := ""
	if trimmed := strings.TrimSpace(tv.Arguments); trimmed != "" {
		if json.Valid([]byte(trimmed)) {
			if keys := topLevelJSONKeys(trimmed); keys != "" {
				argHint = "{" + keys + "}"
			}
		} else {
			argHint = fmt.Sprintf("args_bytes=%d", len(trimmed))
		}
	}
	resultHint := ""
	if trimmed := strings.TrimSpace(tv.Result); trimmed != "" {
		if parsed, ok := parseMCPResult(trimmed); ok {
			if parsed.Text != "" {
				resultHint = safeOneLine(parsed.Text, 80)
			} else if parsed.Structured != "" {
				resultHint = "structured"
			} else if parsed.RawJSON != "" {
				resultHint = "json"
			}
		} else if strings.HasPrefix(strings.TrimSpace(trimmed), "ERROR:") {
			resultHint = safeOneLine(strings.TrimSpace(trimmed), 120)
		} else {
			resultHint = safeOneLine(trimmed, 100)
		}
	}

	line := fmt.Sprintf("%s%s %s %s -> %s", prefix, arrow, tv.Name, argHint, statusStyle.Render(status))
	if resultHint != "" {
		line += " " + lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(resultHint)
	}
	if expanded != nil && expanded[tv.Key] {
		line += lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" (expanded)")
	}
	return truncateANSI(line, width)
}

func renderToolDetails(tv *toolView, width int) []string {
	if tv == nil {
		return nil
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	lines := make([]string, 0, 16)
	if strings.TrimSpace(tv.Arguments) != "" {
		lines = append(lines, dim.Render("    args:"))
		for _, line := range strings.Split(formatJSON(tv.Arguments), "\n") {
			lines = append(lines, dim.Render("      "+truncateANSI(line, width-6)))
		}
	}
	if strings.TrimSpace(tv.Result) != "" {
		lines = append(lines, dim.Render("    result:"))
		for _, line := range strings.Split(strings.TrimRight(tv.Result, "\n"), "\n") {
			lines = append(lines, dim.Render("      "+truncateANSI(line, width-6)))
		}
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	return lines
}

func (m *tuiModel) renderSubagent(runID string, agentID string, width int) (lines []string, toolKeys []string, toolOffsets []int) {
	spec, _ := m.coord.ReadAgentSpec(runID, agentID)
	state, _ := m.coord.ReadAgentState(runID, agentID)
	events, _ := multiagent.TailJSONL[multiagent.AgentEvent](m.coord.AgentEventsPath(runID, agentID), 200, 256*1024)
	result, _ := m.coord.ReadResult(runID, agentID)

	heading := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	lines = append(lines, heading.Render(fmt.Sprintf("%s [%s]", agentID, state.Status)))
	if strings.TrimSpace(spec.Task) != "" {
		appendBlock(&lines, wrapText("Task: "+spec.Task, width))
		lines = append(lines, "")
	}

	tools := subagentToolViews(events)
	for i := range tools {
		tv := tools[i]
		tv.Key = toolUIKey(runID, agentID, tv.Key)
		toolKeys = append(toolKeys, tv.Key)
		toolOffsets = append(toolOffsets, len(lines))
		lines = append(lines, renderToolLine(tv, width, m.expandedTools, m.toolCursor, len(toolKeys)-1))
		if m.expandedTools[tv.Key] {
			lines = append(lines, renderToolDetails(tv, width)...)
		}
	}

	if strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Error) != "" {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render("Final Output"))
		if strings.TrimSpace(result.Output) != "" {
			appendBlock(&lines, wrapText(result.Output, width))
		}
		if strings.TrimSpace(result.Error) != "" {
			wrapped := strings.Split(wrapText("Error: "+result.Error, width), "\n")
			red := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
			for _, line := range wrapped {
				lines = append(lines, red.Render(line))
			}
		}
	}
	return lines, toolKeys, toolOffsets
}

func toolUIKey(runID string, agentID string, toolKey string) string {
	r := strings.TrimSpace(runID)
	a := strings.TrimSpace(agentID)
	k := strings.TrimSpace(toolKey)
	if r == "" || a == "" || k == "" {
		return strings.TrimSpace(strings.Join([]string{r, a, k}, "/"))
	}
	return r + "/" + a + "/" + k
}

func subagentToolViews(events []multiagent.AgentEvent) []*toolView {
	out := make([]*toolView, 0, 32)
	var pending []*toolView
	for _, evt := range events {
		switch strings.TrimSpace(evt.Type) {
		case "tool_call_started":
			name, _ := evt.Payload["name"].(string)
			args, _ := evt.Payload["args"].(string)
			tv := &toolView{
				Key:       fmt.Sprintf("evt-%d", evt.Seq),
				Name:      strings.TrimSpace(name),
				Arguments: strings.TrimSpace(args),
			}
			out = append(out, tv)
			pending = append(pending, tv)
		case "tool_call_finished":
			name, _ := evt.Payload["name"].(string)
			status, _ := evt.Payload["status"].(string)
			errText, _ := evt.Payload["error"].(string)
			result, _ := evt.Payload["result"].(string)
			durationMs, _ := evt.Payload["duration_ms"].(float64)

			var tv *toolView
			for i := len(pending) - 1; i >= 0; i-- {
				if pending[i] != nil && strings.TrimSpace(pending[i].Name) == strings.TrimSpace(name) {
					tv = pending[i]
					pending = append(pending[:i], pending[i+1:]...)
					break
				}
			}
			if tv == nil {
				tv = &toolView{Key: fmt.Sprintf("evt-%d", evt.Seq), Name: strings.TrimSpace(name)}
				out = append(out, tv)
			}
			tv.Status = strings.TrimSpace(status)
			tv.Error = strings.TrimSpace(errText)
			tv.Result = strings.TrimSpace(result)
			tv.DurationMs = int64(durationMs)
		}
	}
	return out
}

func (m *tuiModel) renderSessions(width int, height int) string {
	style := lipgloss.NewStyle().Width(width).Height(height).BorderRight(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8"))
	var lines []string
	title := lipgloss.NewStyle().Bold(true).Render("Sessions")
	lines = append(lines, title)
	lines = append(lines, "")
	for i, run := range m.sessions {
		prefix := "  "
		if i == m.sessionIndex {
			prefix = "> "
		}
		line := fmt.Sprintf("%s%s", prefix, run.ID)
		lines = append(lines, truncateANSI(line, width-1))
	}
	return style.Render(strings.Join(lines, "\n"))
}

func (m *tuiModel) renderStatus(width int, height int) string {
	style := lipgloss.NewStyle().Width(width).Height(height).BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8"))
	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render("Status"))
	lines = append(lines, "")

	runID := m.currentRunID()
	states, _ := m.coord.ListAgentStates(runID)
	stateMap := make(map[string]multiagent.AgentState, len(states))
	for _, st := range states {
		stateMap[st.AgentID] = st
	}
	if _, ok := stateMap[tuiPrimaryAgentID]; !ok {
		stateMap[tuiPrimaryAgentID] = multiagent.AgentState{AgentID: tuiPrimaryAgentID, Status: "chat"}
	}

	ids := append([]string(nil), m.agentIDs...)
	if len(ids) == 0 {
		ids = []string{tuiPrimaryAgentID}
	}
	for _, id := range ids {
		st := stateMap[id]
		arrow := " "
		if id == m.currentAgentID() {
			arrow = ">"
		}
		status := strings.TrimSpace(st.Status)
		if status == "" {
			status = "unknown"
		}
		statusStyle := statusColor(status)
		line := fmt.Sprintf("%s %s %s", arrow, id, statusStyle.Render(status))
		lines = append(lines, truncateANSI(line, width-1))
	}
	return style.Render(strings.Join(lines, "\n"))
}

func (m *tuiModel) renderCenter(width int, height int) string {
	border := lipgloss.NewStyle().Width(width).Height(height)
	if width <= 0 || height <= 0 {
		return ""
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	header := headerStyle.Render("Chat")

	focusText := "INPUT"
	if m.focus == tuiFocusBrowse {
		focusText = "BROWSE"
	}
	if m.busy {
		focusText += " (busy)"
	}

	subHeader := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(fmt.Sprintf("Mode: %s | Agent: %s", focusText, m.currentAgentID()))
	notice := ""
	if strings.TrimSpace(m.notice) != "" {
		notice = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(truncateANSI("Error: "+m.notice, max(10, width-2)))
	}

	headerBlock := lipgloss.NewStyle().Padding(0, 1).Render(strings.Join([]string{header, subHeader, notice}, "\n"))

	vp := m.viewport.View()
	inputLine := m.renderInputLine(width)

	content := lipgloss.JoinVertical(lipgloss.Left, headerBlock, vp, inputLine)
	return border.Render(content)
}

func (m *tuiModel) renderInputLine(width int) string {
	if m.busy {
		m.input.Blur()
		return lipgloss.NewStyle().Width(width).Padding(0, 1).Foreground(lipgloss.Color("8")).Render("Thinking…")
	}
	if m.focus == tuiFocusInput {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
	m.input.Width = max(10, width-2)
	return lipgloss.NewStyle().Width(width).Padding(0, 1).Render(m.input.View())
}

func statusColor(status string) lipgloss.Style {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case multiagent.StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case multiagent.StatusPending:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	case multiagent.StatusPaused:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	case multiagent.StatusCompleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	case multiagent.StatusFailed:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	case multiagent.StatusCanceled:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	}
}

func truncateANSI(s string, width int) string {
	if width <= 0 {
		return s
	}
	if width == 1 {
		return "…"
	}

	maxVisible := width - 1
	var b strings.Builder
	b.Grow(len(s) + 4)

	visible := 0
	truncated := false
	sawEsc := false

	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			sawEsc = true
			seq, n := readANSISequence(s[i:])
			if n > 0 {
				b.WriteString(seq)
				i += n
				continue
			}
			// Fallback: skip the ESC byte.
			i++
			continue
		}

		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			i++
			continue
		}
		rw := runewidth.RuneWidth(r)
		if rw < 0 {
			rw = 0
		}
		if visible+rw > maxVisible {
			truncated = true
			break
		}
		b.WriteRune(r)
		visible += rw
		i += n
	}

	if !truncated {
		return s
	}

	b.WriteRune('…')
	if sawEsc {
		b.WriteString(ansiReset)
	}
	return b.String()
}

func safeOneLine(s string, maxChars int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if maxChars > 0 && len([]rune(s)) > maxChars {
		return string([]rune(s)[:maxChars]) + "…"
	}
	return s
}

func appendBlock(dst *[]string, block string) {
	if dst == nil || block == "" {
		return
	}
	for _, line := range strings.Split(block, "\n") {
		*dst = append(*dst, line)
	}
}

func wrapText(text string, width int) string {
	if width <= 10 {
		return text
	}
	return lipgloss.NewStyle().Width(width).Render(text)
}

func readJSONL[T any](path string, limit int) ([]T, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*64)
	scanner.Buffer(buf, 1024*1024)

	out := make([]T, 0, min(limit, 128))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item T
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return out, err
	}
	return out, nil
}

func appendJSONL(path string, payload any) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func clamp(minv int, v int, maxv int) int {
	if v < minv {
		return minv
	}
	if v > maxv {
		return maxv
	}
	return v
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func readANSISequence(s string) (seq string, n int) {
	if len(s) < 2 || s[0] != 0x1b {
		return "", 0
	}
	switch s[1] {
	case '[':
		// CSI: ESC [ ... final-byte(@-~)
		for i := 2; i < len(s); i++ {
			b := s[i]
			if b >= 0x40 && b <= 0x7e {
				return s[:i+1], i + 1
			}
		}
		return s, len(s)
	case ']':
		// OSC: ESC ] ... BEL or ESC \
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return s[:i+1], i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return s[:i+2], i + 2
			}
		}
		return s, len(s)
	default:
		// Other sequences: consume ESC + next byte.
		return s[:2], 2
	}
}
