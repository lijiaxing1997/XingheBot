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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"test_skill_agent/internal/appinfo"
	"test_skill_agent/internal/gateway"
	"test_skill_agent/internal/llm"
	"test_skill_agent/internal/multiagent"
	"test_skill_agent/internal/restart"
)

const (
	tuiPrimaryAgentID          = "primary"
	tuiPrimaryAgentTaskDefault = "primary chat session"
	tuiHistoryFileName         = "history.jsonl"
)

var tuiSpinnerFrames = []string{"|", "/", "-", "\\"}

type TUIMode string

const (
	TUIModeTUI   TUIMode = "tui"
	TUIModePlain TUIMode = "plain"
)

type TUIOptions struct {
	Coordinator *multiagent.Coordinator
	ConfigPath  string
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

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	model := newTUIModel(ctx, a, opts.Coordinator, strings.TrimSpace(opts.ConfigPath))
	prog := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithInput(in),
		tea.WithOutput(out),
	)
	_, err := prog.Run()
	return err
}

type tuiModel struct {
	ctx   context.Context
	agent *Agent
	coord *multiagent.Coordinator

	events chan tuiAsyncMsg

	width  int
	height int

	gatewayConfigPath string
	emailGateway      *gateway.EmailGateway
	gatewayEnabled    bool
	gatewayEmail      string
	gatewayStatus     gateway.EmailStatus
	gatewayInbox      []gateway.EmailInbound
	startGatewayOnce  sync.Once

	sessions      []multiagent.RunManifest
	sessionIndex  int
	sessionCursor int

	sessionData map[string]*tuiSessionData

	agentIDs     []string
	agentIndex   int
	showDone     bool
	hiddenDone   int
	hiddenManual int

	input    textinput.Model
	viewport viewport.Model

	expandedTools map[string]bool
	lineToolKeys  []string
	cursorLine    int
	stickToBottom bool
	spinnerFrame  int

	loading            bool
	busy               bool
	notice             string
	banner             string
	deleteConfirmRunID string
	deleteConfirmAt    time.Time
	fatal              error
}

type tuiSessionData struct {
	RunID       string
	HistoryPath string
	History     []llm.Message
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

type tuiSessionDeletedMsg struct {
	RunID string
	Err   error
}

type tuiRunManifestUpdatedMsg struct {
	RunID string
	Run   multiagent.RunManifest
	Err   error
}

type tuiAsyncMsg struct {
	Event tea.Msg
}

type tuiAppendHistoryMsg struct {
	RunID string
	Msg   llm.Message
}

type tuiSetBusyMsg struct {
	Busy bool
}

type tuiSetNoticeMsg struct {
	Text string
}

type tuiGatewayStatusMsg struct {
	Status gateway.EmailStatus
}

type tuiGatewayInboundMsg struct {
	Msg gateway.EmailInbound
}

func newTUIModel(ctx context.Context, a *Agent, coord *multiagent.Coordinator, configPath string) tuiModel {
	inp := textinput.New()
	inp.Placeholder = "Type a message…"
	inp.Prompt = "› "
	inp.CharLimit = 0
	inp.Focus()

	vp := viewport.New(0, 0)
	vp.SetContent("")

	banner := ""
	if a != nil {
		banner = strings.TrimSpace(a.StartupBanner)
	}

	enabled := false
	var gcfg gateway.GatewayConfig
	if strings.TrimSpace(configPath) != "" {
		if loaded, err := gateway.LoadGatewayConfig(configPath); err == nil {
			gcfg = loaded
			enabled = loaded.Enabled
		} else {
			enabled = false
			if banner != "" {
				banner += " | "
			}
			banner += "Gateway config load failed: " + err.Error()
		}
	}

	var emailGateway *gateway.EmailGateway
	emailAddr := ""
	if enabled {
		emailGateway = gateway.NewEmailGateway(gcfg.Email)
		emailAddr = strings.TrimSpace(gcfg.Email.EmailAddress)
	}

	return tuiModel{
		ctx:               ctx,
		agent:             a,
		coord:             coord,
		events:            make(chan tuiAsyncMsg, 512),
		input:             inp,
		viewport:          vp,
		sessionData:       make(map[string]*tuiSessionData),
		expandedTools:     make(map[string]bool),
		cursorLine:        -1,
		stickToBottom:     true,
		showDone:          false,
		banner:            banner,
		gatewayConfigPath: strings.TrimSpace(configPath),
		emailGateway:      emailGateway,
		gatewayEnabled:    enabled,
		gatewayEmail:      emailAddr,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return tuiInitMsg{} },
		tuiLoadSessionsCmd(m.coord),
		tuiTickCmd(),
		waitAsyncCmd(m.events),
	)
}

func (m *tuiModel) startGateway() {
	if m == nil {
		return
	}
	m.startGatewayOnce.Do(func() {
		if !m.gatewayEnabled || m.emailGateway == nil {
			return
		}
		m.gatewayStatus = gateway.EmailStatus{
			OK:        false,
			Error:     "connecting…",
			CheckedAt: time.Now().UTC(),
		}
		ctx := m.ctx
		events := m.events
		gw := m.emailGateway
		go func() {
			err := gw.Run(ctx,
				func(st gateway.EmailStatus) {
					events <- tuiAsyncMsg{Event: tuiGatewayStatusMsg{Status: st}}
				},
				func(in gateway.EmailInbound) {
					events <- tuiAsyncMsg{Event: tuiGatewayInboundMsg{Msg: in}}
				},
			)
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				events <- tuiAsyncMsg{Event: tuiGatewayStatusMsg{
					Status: gateway.EmailStatus{OK: false, Error: err.Error(), CheckedAt: time.Now().UTC()},
				}}
			}
		}()
	})
}

func tuiTickCmd() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return tuiRefreshMsg{} })
}

func waitAsyncCmd(ch <-chan tuiAsyncMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
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

func tuiDeleteSessionCmd(coord *multiagent.Coordinator, runID string) tea.Cmd {
	return func() tea.Msg {
		err := coord.DeleteRun(runID)
		return tuiSessionDeletedMsg{RunID: runID, Err: err}
	}
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.agent != nil && m.agent.RestartManager != nil && m.agent.RestartManager.IsRestartRequested() {
		return m, tea.Quit
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		m.rerender()
		return m, nil
	case tuiAsyncMsg:
		m.handleAsyncEvent(msg.Event)
		m.rerender()
		return m, waitAsyncCmd(m.events)
	case tuiInitMsg:
		m.loading = true
		m.startGateway()
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
		m.sessionCursor = m.sessionIndex
		m.ensureSessionLoaded(m.currentRunID())
		m.createPrimaryAgent(m.currentRunID())
		m.refreshAgentIDs()
		m.rerender()
		return m, nil
	case tuiRefreshMsg:
		if m.agent != nil && m.agent.RestartManager != nil && m.agent.RestartManager.IsRestartRequested() {
			return m, tea.Quit
		}
		if m.currentRunID() == "" {
			return m, tuiTickCmd()
		}
		if len(tuiSpinnerFrames) > 0 {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(tuiSpinnerFrames)
		}
		m.refreshAgentIDs()
		m.maybeAutoFollowup()
		m.maybeProcessGatewayInbox()
		m.rerender()
		return m, tuiTickCmd()
	case tuiSessionCreatedMsg:
		if msg.Err != nil {
			m.notice = msg.Err.Error()
			return m, nil
		}
		m.sessions = append([]multiagent.RunManifest{msg.Run}, m.sessions...)
		m.sessionIndex = 0
		m.sessionCursor = 0
		m.ensureSessionLoaded(m.currentRunID())
		m.createPrimaryAgent(m.currentRunID())
		m.refreshAgentIDs()
		m.notice = ""
		m.rerender()
		return m, nil
	case tuiSessionDeletedMsg:
		if msg.Err != nil {
			m.notice = msg.Err.Error()
			return m, nil
		}
		deletedID := strings.TrimSpace(msg.RunID)
		if deletedID == "" {
			return m, nil
		}
		delete(m.sessionData, deletedID)

		deletedIndex := -1
		for i := range m.sessions {
			if strings.TrimSpace(m.sessions[i].ID) == deletedID {
				deletedIndex = i
				break
			}
		}
		if deletedIndex >= 0 {
			m.sessions = append(m.sessions[:deletedIndex], m.sessions[deletedIndex+1:]...)
			if deletedIndex < m.sessionIndex {
				m.sessionIndex--
			} else if deletedIndex == m.sessionIndex {
				if m.sessionIndex >= len(m.sessions) {
					m.sessionIndex = len(m.sessions) - 1
				}
			}
		}

		m.deleteConfirmRunID = ""
		m.deleteConfirmAt = time.Time{}
		m.notice = ""

		if len(m.sessions) == 0 {
			m.sessionIndex = 0
			m.sessionCursor = -1
			m.rerender()
			return m, tuiCreateSessionCmd(m.coord)
		}
		if m.sessionIndex < 0 {
			m.sessionIndex = 0
		}
		if m.sessionIndex >= len(m.sessions) {
			m.sessionIndex = len(m.sessions) - 1
		}
		m.sessionCursor = m.sessionIndex
		m.ensureSessionLoaded(m.currentRunID())
		m.createPrimaryAgent(m.currentRunID())
		m.refreshAgentIDs()
		m.cursorLine = -1
		m.stickToBottom = true
		m.rerender()
		return m, nil
	case tuiSessionSelectedMsg:
		if msg.RunID == "" {
			return m, nil
		}
		for i, run := range m.sessions {
			if run.ID == msg.RunID {
				m.sessionIndex = i
				m.sessionCursor = i
				m.ensureSessionLoaded(msg.RunID)
				m.createPrimaryAgent(msg.RunID)
				m.refreshAgentIDs()
				m.rerender()
				break
			}
		}
		return m, nil
	case tea.KeyMsg:
		handled, cmd := m.handleKey(msg)
		if handled {
			return m, cmd
		}
		if m.busy || m.currentAgentID() != tuiPrimaryAgentID {
			return m, nil
		}
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

const (
	autoFollowupMaxAgentsPerTick      = 6
	autoFollowupMaxTaskChars          = 800
	autoFollowupMaxOutputPreviewChars = 5000
)

func (m *tuiModel) maybeAutoFollowup() {
	if m == nil || m.coord == nil || m.agent == nil {
		return
	}
	if m.busy {
		return
	}
	runID := strings.TrimSpace(m.currentRunID())
	if runID == "" {
		return
	}

	m.ensureSessionLoaded(runID)
	m.createPrimaryAgent(runID)
	sess := m.sessionData[runID]
	if sess == nil {
		return
	}
	// Avoid spamming old runs that have no conversation history.
	if len(sess.History) == 0 {
		return
	}

	ui, err := m.coord.ReadRunUIState(runID)
	if err != nil {
		return
	}
	states, err := m.coord.ListAgentStates(runID)
	if err != nil {
		return
	}

	now := time.Now().UTC()
	allTerminal := true
	pendingAll := make([]multiagent.AgentState, 0, 4)
	for _, st := range states {
		if strings.TrimSpace(st.AgentID) == "" || st.AgentID == tuiPrimaryAgentID {
			continue
		}
		if !multiagent.IsTerminalStatus(st.Status) {
			allTerminal = false
			continue
		}
		finishedAt := st.FinishedAt
		if finishedAt.IsZero() {
			finishedAt = st.UpdatedAt
		}
		if ui.ReportedAgentResults != nil {
			if rec, ok := ui.ReportedAgentResults[st.AgentID]; ok && !rec.FinishedAt.IsZero() && finishedAt.Equal(rec.FinishedAt) {
				continue
			}
		}
		pendingAll = append(pendingAll, st)
	}
	if len(pendingAll) == 0 {
		return
	}
	sort.Slice(pendingAll, func(i, j int) bool {
		ai := pendingAll[i].FinishedAt
		aj := pendingAll[j].FinishedAt
		if ai.IsZero() {
			ai = pendingAll[i].UpdatedAt
		}
		if aj.IsZero() {
			aj = pendingAll[j].UpdatedAt
		}
		if ai.Equal(aj) {
			return pendingAll[i].AgentID < pendingAll[j].AgentID
		}
		return ai.Before(aj)
	})

	pending := pendingAll
	if len(pending) > autoFollowupMaxAgentsPerTick {
		pending = pending[:autoFollowupMaxAgentsPerTick]
	}

	content, previews := m.buildAutoFollowupSystemMessage(runID, pending, now)
	sysMsg := llm.Message{Role: "system", Content: content}
	sess.History = append(sess.History, sysMsg)
	_ = appendJSONL(sess.HistoryPath, sysMsg)

	reports := make([]multiagent.ReportedAgentResultRecord, 0, len(pending))
	for _, st := range pending {
		finishedAt := st.FinishedAt
		if finishedAt.IsZero() {
			finishedAt = st.UpdatedAt
		}
		if finishedAt.IsZero() {
			finishedAt = now
		}
		resultPath := strings.TrimSpace(st.ResultPath)
		if resultPath == "" {
			resultPath = m.coord.AgentResultPath(runID, st.AgentID)
		}
		reports = append(reports, multiagent.ReportedAgentResultRecord{
			AgentID:      st.AgentID,
			Status:       st.Status,
			FinishedAt:   finishedAt,
			ResultPath:   resultPath,
			Error:        st.Error,
			PreviewChars: previews[st.AgentID],
		})
	}
	if _, err := m.coord.MarkAgentResultsReported(runID, reports, now); err != nil {
		m.notice = err.Error()
	}

	base := append([]llm.Message(nil), sess.History...)
	var emailReply *autoFollowupEmailReply
	if allTerminal && len(pendingAll) == len(pending) {
		if reply, ok := m.loadAutoFollowupEmailReply(runID); ok {
			emailReply = &reply
		}
	}
	m.busy = true
	m.notice = ""
	m.stickToBottom = true
	m.cursorLine = -1
	m.rerender()
	go m.runAutoFollowup(runID, base, emailReply)
}

func (m *tuiModel) maybeProcessGatewayInbox() {
	if m == nil {
		return
	}
	if m.busy {
		return
	}
	if len(m.gatewayInbox) == 0 {
		return
	}
	msg := m.gatewayInbox[0]
	m.gatewayInbox = m.gatewayInbox[1:]
	m.processGatewayEmail(msg)
}

func (m *tuiModel) processGatewayEmail(in gateway.EmailInbound) {
	if m == nil || m.coord == nil || m.agent == nil {
		return
	}
	if strings.TrimSpace(in.From) == "" {
		return
	}

	subjectKey := normalizeEmailSubject(in.Subject)
	replySubject := strings.TrimSpace(in.Subject)
	if replySubject == "" {
		replySubject = subjectKey
	}
	runID, err := m.ensureEmailSession(subjectKey)
	if err != nil {
		m.notice = err.Error()
		m.rerender()
		return
	}

	userText := strings.TrimSpace(in.Body)
	userText = trimQuotedReplyBody(userText)
	if userText == "" {
		userText = strings.TrimSpace(in.Subject)
	}
	if userText == "" {
		userText = "(empty email)"
	}

	m.ensureSessionLoaded(runID)
	m.createPrimaryAgent(runID)
	sess := m.sessionData[runID]
	if sess == nil {
		m.notice = "failed to load session"
		m.rerender()
		return
	}
	base := append([]llm.Message(nil), sess.History...)

	userMsg := llm.Message{Role: "user", Content: userText}
	sess.History = append(sess.History, userMsg)
	_ = appendJSONL(sess.HistoryPath, userMsg)

	m.busy = true
	m.notice = ""
	m.stickToBottom = true
	m.cursorLine = -1
	m.rerender()
	thread := gateway.EmailThreadContext{
		MessageID:  in.MessageID,
		InReplyTo:  in.InReplyTo,
		References: in.References,
	}
	m.recordGatewayEmailContext(runID, in.From, replySubject, thread)
	go m.runTurnEmail(runID, userText, base, replySubject, in.From, thread)
}

func (m *tuiModel) recordGatewayEmailContext(runID string, replyTo string, replySubject string, thread gateway.EmailThreadContext) {
	if m == nil || m.coord == nil {
		return
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return
	}
	to := strings.TrimSpace(replyTo)
	if to == "" {
		return
	}
	subject := strings.TrimSpace(replySubject)
	now := time.Now().UTC()

	updated, err := m.coord.UpdateRun(id, func(run *multiagent.RunManifest) error {
		if run == nil {
			return nil
		}
		if run.Metadata == nil {
			run.Metadata = make(map[string]any)
		}
		run.Metadata["source"] = "email"

		emailMeta := map[string]any{}
		if existing, ok := run.Metadata["email"].(map[string]any); ok && existing != nil {
			emailMeta = existing
		}
		emailMeta["reply_to"] = to
		if subject != "" {
			emailMeta["reply_subject"] = subject
		}
		emailMeta["updated_at"] = now.Format(time.RFC3339)
		emailMeta["thread"] = map[string]any{
			"message_id":  strings.TrimSpace(thread.MessageID),
			"in_reply_to": strings.TrimSpace(thread.InReplyTo),
			"references":  thread.References,
		}
		run.Metadata["email"] = emailMeta
		return nil
	})
	if err != nil {
		m.notice = err.Error()
		return
	}
	for i := range m.sessions {
		if m.sessions[i].ID == updated.ID {
			m.sessions[i] = updated
			break
		}
	}
}

func (m *tuiModel) ensureEmailSession(subjectKey string) (string, error) {
	key := strings.TrimSpace(subjectKey)
	if key == "" {
		key = "(无主题)"
	}

	// Try to find an existing session by title (email subject key).
	for i, run := range m.sessions {
		if strings.TrimSpace(multiagent.RunTitle(run)) != key {
			continue
		}
		m.sessionIndex = i
		m.sessionCursor = i
		m.ensureSessionLoaded(run.ID)
		m.createPrimaryAgent(run.ID)
		m.refreshAgentIDs()
		m.cursorLine = -1
		m.stickToBottom = true
		m.rerender()
		return run.ID, nil
	}

	// Compatibility: some email clients auto-prefix reply/forward subjects, e.g.
	// "回复：" / "Re:" / "Fwd:".
	for i, run := range m.sessions {
		title := strings.TrimSpace(multiagent.RunTitle(run))
		if title == "" || title == key {
			continue
		}
		if normalizeEmailSubject(title) != key {
			continue
		}
		m.sessionIndex = i
		m.sessionCursor = i
		m.ensureSessionLoaded(run.ID)
		m.createPrimaryAgent(run.ID)
		m.refreshAgentIDs()
		m.cursorLine = -1
		m.stickToBottom = true
		m.rerender()
		return run.ID, nil
	}

	// Create a new session.
	run, err := m.coord.CreateRun("", map[string]any{
		"source":  "email",
		"subject": key,
	})
	if err != nil {
		return "", err
	}
	updated := run
	if titled, err := m.coord.SetRunTitle(run.ID, key); err == nil {
		updated = titled
	}

	m.sessions = append([]multiagent.RunManifest{updated}, m.sessions...)
	m.sessionIndex = 0
	m.sessionCursor = 0
	m.ensureSessionLoaded(updated.ID)
	m.createPrimaryAgent(updated.ID)
	m.refreshAgentIDs()
	m.cursorLine = -1
	m.stickToBottom = true
	m.rerender()
	return updated.ID, nil
}

func normalizeEmailSubject(subject string) string {
	s := strings.TrimSpace(subject)
	if s == "" {
		return "(无主题)"
	}
	for {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return "(无主题)"
		}
		next, changed := stripEmailSubjectPrefixes(trimmed)
		if !changed {
			return trimmed
		}
		s = next
	}
}

func stripEmailSubjectPrefixes(subject string) (string, bool) {
	s := strings.TrimSpace(subject)
	if s == "" {
		return s, false
	}

	// Common Chinese prefixes: "回复：" / "转发：" etc.
	for _, tag := range []string{"回复", "答复", "转发"} {
		if next, ok := stripCJKSubjectTag(s, tag); ok {
			return next, true
		}
	}

	// Common ASCII prefixes: Re:/Fw:/Fwd: (including "Re[2]:" / "Re(2):", and both ':'/'：').
	for _, tag := range []string{"re", "fw", "fwd"} {
		if next, ok := stripASCIISubjectTag(s, tag); ok {
			return next, true
		}
	}

	return s, false
}

func stripCJKSubjectTag(subject string, tag string) (string, bool) {
	if strings.TrimSpace(tag) == "" {
		return subject, false
	}
	if !strings.HasPrefix(subject, tag) {
		return subject, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(subject, tag))
	if rest == "" {
		return "", true
	}
	switch {
	case strings.HasPrefix(rest, ":"):
		rest = strings.TrimSpace(rest[1:])
		return rest, true
	case strings.HasPrefix(rest, "："):
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "："))
		return rest, true
	default:
		// Some clients omit the colon but still prepend the tag.
		return rest, true
	}
}

func stripASCIISubjectTag(subject string, tag string) (string, bool) {
	if strings.TrimSpace(tag) == "" {
		return subject, false
	}
	lower := strings.ToLower(subject)
	if !strings.HasPrefix(lower, tag) {
		return subject, false
	}

	rest := subject[len(tag):]
	rest = strings.TrimLeft(rest, " \t")

	// Optional counter: Re[2]: / Re(2):
	if len(rest) > 0 && (rest[0] == '[' || rest[0] == '(') {
		close := byte(']')
		if rest[0] == '(' {
			close = ')'
		}
		if idx := strings.IndexByte(rest, close); idx > 0 && idx <= 12 {
			rest = rest[idx+1:]
			rest = strings.TrimLeft(rest, " \t")
		}
	}

	// Must have a colon delimiter to qualify as a prefix.
	switch {
	case strings.HasPrefix(rest, ":"):
		return strings.TrimSpace(rest[1:]), true
	case strings.HasPrefix(rest, "："):
		return strings.TrimSpace(strings.TrimPrefix(rest, "：")), true
	default:
		return subject, false
	}
}

func trimQuotedReplyBody(body string) string {
	text := strings.TrimSpace(body)
	if text == "" {
		return ""
	}
	markers := []string{
		"---- 回复的原邮件 ----",
		"----回复的原邮件----",
	}
	cut := -1
	for _, m := range markers {
		if idx := strings.Index(text, m); idx >= 0 && (cut < 0 || idx < cut) {
			cut = idx
		}
	}
	if cut >= 0 {
		text = strings.TrimSpace(text[:cut])
	}
	return text
}

func (m *tuiModel) runTurnEmail(runID string, userText string, baseHistory []llm.Message, replySubject string, replyTo string, thread gateway.EmailThreadContext) {
	events := m.events
	agentRef := m.agent
	ctx := m.ctx
	gw := m.emailGateway

	defer func() {
		events <- tuiAsyncMsg{Event: tuiSetBusyMsg{Busy: false}}
	}()

	var finalText string
	emit := func(msg llm.Message) {
		events <- tuiAsyncMsg{Event: tuiAppendHistoryMsg{
			RunID: runID,
			Msg:   msg,
		}}
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") && len(msg.ToolCalls) == 0 {
			if t := strings.TrimSpace(msg.Content); t != "" {
				finalText = t
			}
		}
	}

	userTextForLLM := strings.TrimSpace(userText)
	// Email replies historically waited for completion so the user receives a final answer in one email.
	// But if the user explicitly requests async/non-blocking behavior, honor that and do not force waits.
	if userTextForLLM != "" && !userExplicitlyDeclinesBlockingWait(userTextForLLM) && !userExplicitlyRequestsBlockingWait(userTextForLLM) {
		userTextForLLM += "\n\n等待完成后告诉我最终结果。"
	}

	afterTool := func(call llm.ToolCall, result string, callErr error) []llm.Message {
		return m.maybeBuildWaitCompletionSystemMessages(runID, call, result, callErr)
	}
	err := runTUITurnStreaming(ctx, agentRef, runID, userTextForLLM, baseHistory, emit, afterTool)
	if err != nil {
		if finalText == "" {
			finalText = "ERROR: " + err.Error()
		} else {
			finalText += "\n\nERROR: " + err.Error()
		}
		events <- tuiAsyncMsg{Event: tuiSetNoticeMsg{Text: err.Error()}}
	}

	if strings.TrimSpace(finalText) == "" {
		finalText = "(no output)"
	}

	if gw == nil || strings.TrimSpace(replyTo) == "" {
		return
	}
	if strings.TrimSpace(replySubject) == "" {
		replySubject = "(无主题)"
	}

	replyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := gw.SendReply(replyCtx, replyTo, replySubject, finalText, thread); err != nil {
		events <- tuiAsyncMsg{Event: tuiSetNoticeMsg{Text: err.Error()}}
	}
}

func (m *tuiModel) buildAutoFollowupSystemMessage(runID string, states []multiagent.AgentState, now time.Time) (string, map[string]int) {
	previews := make(map[string]int, len(states))
	var b strings.Builder
	b.WriteString("[System Message] Subagent work completed.\n")
	b.WriteString(fmt.Sprintf("run_id=%s\n", strings.TrimSpace(runID)))
	if !now.IsZero() {
		b.WriteString(fmt.Sprintf("reported_at=%s\n", now.Format(time.RFC3339)))
	}
	b.WriteString("\n")

	b.WriteString("Results:\n")
	for _, st := range states {
		agentID := strings.TrimSpace(st.AgentID)
		if agentID == "" {
			continue
		}
		spec, _ := m.coord.ReadAgentSpec(runID, agentID)
		result, _ := m.coord.ReadResult(runID, agentID)
		finishedAt := st.FinishedAt
		if finishedAt.IsZero() {
			finishedAt = result.FinishedAt
		}
		if finishedAt.IsZero() {
			finishedAt = st.UpdatedAt
		}

		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("- agent_id=%s status=%s\n", agentID, strings.TrimSpace(st.Status)))
		if !finishedAt.IsZero() {
			b.WriteString(fmt.Sprintf("  finished_at=%s\n", finishedAt.Format(time.RFC3339)))
		}
		if task := strings.TrimSpace(spec.Task); task != "" {
			preview, _ := truncateForPrompt(task, autoFollowupMaxTaskChars)
			b.WriteString("  task:\n")
			b.WriteString(indentBlock(preview, "    "))
			b.WriteString("\n")
		}
		if errText := strings.TrimSpace(result.Error); errText != "" {
			b.WriteString("  error:\n")
			b.WriteString(indentBlock(errText, "    "))
			b.WriteString("\n")
		}
		out := strings.TrimSpace(result.Output)
		if out == "" && strings.TrimSpace(st.Error) != "" {
			out = "ERROR: " + strings.TrimSpace(st.Error)
		}
		if out != "" {
			preview, truncated := truncateForPrompt(out, autoFollowupMaxOutputPreviewChars)
			previews[agentID] = len(preview)
			b.WriteString("  output:\n")
			b.WriteString(indentBlock(preview, "    "))
			if truncated {
				b.WriteString("\n")
				b.WriteString("    ...(truncated)\n")
			} else {
				b.WriteString("\n")
			}
		}
		agentDir := m.coord.AgentDir(runID, agentID)
		b.WriteString(fmt.Sprintf("  agent_dir=%s\n", agentDir))
		b.WriteString(fmt.Sprintf("  result_path=%s\n", m.coord.AgentResultPath(runID, agentID)))
	}

	b.WriteString("\n")
	b.WriteString("Please provide a user-facing update that synthesizes these results with the session context. Avoid dumping raw logs; summarize, list key file changes, and propose next steps.\n")
	return b.String(), previews
}

func (m *tuiModel) maybeBuildWaitCompletionSystemMessages(runID string, call llm.ToolCall, result string, callErr error) []llm.Message {
	if m == nil || m.coord == nil {
		return nil
	}
	if callErr != nil {
		return nil
	}
	if strings.TrimSpace(call.Function.Name) != "agent_wait" {
		return nil
	}
	raw := strings.TrimSpace(result)
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil
	}
	var out struct {
		TimedOut  bool                    `json:"timed_out"`
		States    []multiagent.AgentState `json:"states"`
		CheckedAt time.Time               `json:"checked_at"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	if out.TimedOut || len(out.States) == 0 {
		return nil
	}
	now := time.Now().UTC()
	if !out.CheckedAt.IsZero() {
		now = out.CheckedAt
	}

	ui, err := m.coord.ReadRunUIState(runID)
	if err != nil {
		return nil
	}

	pending := make([]multiagent.AgentState, 0, len(out.States))
	for _, st := range out.States {
		if strings.TrimSpace(st.AgentID) == "" || st.AgentID == tuiPrimaryAgentID {
			continue
		}
		if !multiagent.IsTerminalStatus(st.Status) {
			continue
		}
		finishedAt := st.FinishedAt
		if finishedAt.IsZero() {
			finishedAt = st.UpdatedAt
		}
		if ui.ReportedAgentResults != nil {
			if rec, ok := ui.ReportedAgentResults[st.AgentID]; ok && !rec.FinishedAt.IsZero() && finishedAt.Equal(rec.FinishedAt) {
				continue
			}
		}
		pending = append(pending, st)
	}
	if len(pending) == 0 {
		return nil
	}

	sort.Slice(pending, func(i, j int) bool {
		ai := pending[i].FinishedAt
		aj := pending[j].FinishedAt
		if ai.IsZero() {
			ai = pending[i].UpdatedAt
		}
		if aj.IsZero() {
			aj = pending[j].UpdatedAt
		}
		if ai.Equal(aj) {
			return pending[i].AgentID < pending[j].AgentID
		}
		return ai.Before(aj)
	})

	subset := pending
	if len(subset) > autoFollowupMaxAgentsPerTick {
		subset = subset[:autoFollowupMaxAgentsPerTick]
	}
	content, previews := m.buildAutoFollowupSystemMessage(runID, subset, now)

	reports := make([]multiagent.ReportedAgentResultRecord, 0, len(pending))
	for _, st := range pending {
		finishedAt := st.FinishedAt
		if finishedAt.IsZero() {
			finishedAt = st.UpdatedAt
		}
		if finishedAt.IsZero() {
			finishedAt = now
		}
		resultPath := strings.TrimSpace(st.ResultPath)
		if resultPath == "" {
			resultPath = m.coord.AgentResultPath(runID, st.AgentID)
		}
		reports = append(reports, multiagent.ReportedAgentResultRecord{
			AgentID:      st.AgentID,
			Status:       st.Status,
			FinishedAt:   finishedAt,
			ResultPath:   resultPath,
			Error:        st.Error,
			PreviewChars: previews[st.AgentID],
		})
	}
	_, _ = m.coord.MarkAgentResultsReported(runID, reports, now)

	return []llm.Message{{Role: "system", Content: content}}
}

type autoFollowupEmailReply struct {
	To      string
	Subject string
	Thread  gateway.EmailThreadContext
}

func (m *tuiModel) loadAutoFollowupEmailReply(runID string) (autoFollowupEmailReply, bool) {
	if m == nil || m.coord == nil || m.emailGateway == nil {
		return autoFollowupEmailReply{}, false
	}
	run, err := m.coord.ReadRun(strings.TrimSpace(runID))
	if err != nil || run.Metadata == nil {
		return autoFollowupEmailReply{}, false
	}
	src, _ := run.Metadata["source"].(string)
	if strings.TrimSpace(src) != "email" {
		return autoFollowupEmailReply{}, false
	}
	emailMeta, ok := run.Metadata["email"].(map[string]any)
	if !ok || emailMeta == nil {
		return autoFollowupEmailReply{}, false
	}
	to, _ := emailMeta["reply_to"].(string)
	to = strings.TrimSpace(to)
	if to == "" {
		return autoFollowupEmailReply{}, false
	}
	subject, _ := emailMeta["reply_subject"].(string)
	subject = strings.TrimSpace(subject)
	if subject == "" {
		if s, ok := run.Metadata["subject"].(string); ok {
			subject = strings.TrimSpace(s)
		}
	}
	if subject == "" {
		subject = "(无主题)"
	}

	var thread gateway.EmailThreadContext
	if t, ok := emailMeta["thread"].(map[string]any); ok && t != nil {
		thread.MessageID, _ = t["message_id"].(string)
		thread.InReplyTo, _ = t["in_reply_to"].(string)
		thread.References = anyStringSlice(t["references"])
	}

	return autoFollowupEmailReply{
		To:      to,
		Subject: subject,
		Thread:  thread,
	}, true
}

func anyStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		out := make([]string, 0, len(t))
		for _, s := range t {
			if v := strings.TrimSpace(s); v != "" {
				out = append(out, v)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if v := strings.TrimSpace(s); v != "" {
				out = append(out, v)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case string:
		text := strings.TrimSpace(t)
		if text == "" {
			return nil
		}
		return strings.Fields(text)
	default:
		return nil
	}
}

func (m *tuiModel) runAutoFollowup(runID string, baseHistory []llm.Message, emailReply *autoFollowupEmailReply) {
	events := m.events
	agentRef := m.agent
	ctx := m.ctx
	gw := m.emailGateway

	defer func() {
		events <- tuiAsyncMsg{Event: tuiSetBusyMsg{Busy: false}}
	}()

	var finalText string
	emit := func(msg llm.Message) {
		events <- tuiAsyncMsg{Event: tuiAppendHistoryMsg{
			RunID: runID,
			Msg:   msg,
		}}
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") && len(msg.ToolCalls) == 0 {
			if t := strings.TrimSpace(msg.Content); t != "" {
				finalText = t
			}
		}
	}

	userText := "Using the latest [System Message] about subagent completion and the full session context: synthesize a concise user-facing update (what happened, what changed, files touched, commands to run if any, and next steps). If anything is ambiguous, ask the minimal clarifying questions."
	if style := strings.TrimSpace(agentRef.ReplyStyle); style != "" {
		userText += "\n\nReply style requirements (must follow):\n" + style
	}
	if err := runTUITurnStreaming(ctx, agentRef, runID, userText, baseHistory, emit, nil); err != nil {
		events <- tuiAsyncMsg{Event: tuiSetNoticeMsg{Text: err.Error()}}
	}

	if emailReply == nil || gw == nil {
		return
	}
	if strings.TrimSpace(finalText) == "" {
		finalText = "(no output)"
	}
	replyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := gw.SendReply(replyCtx, emailReply.To, emailReply.Subject, finalText, emailReply.Thread); err != nil {
		events <- tuiAsyncMsg{Event: tuiSetNoticeMsg{Text: err.Error()}}
	}
}

func truncateForPrompt(s string, limit int) (string, bool) {
	if limit <= 0 {
		return "", true
	}
	text := strings.TrimSpace(s)
	if len(text) <= limit {
		return text, false
	}
	return text[:limit], true
}

func indentBlock(text string, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) handleAsyncEvent(evt tea.Msg) {
	switch msg := evt.(type) {
	case tuiAppendHistoryMsg:
		m.ensureSessionLoaded(msg.RunID)
		sess := m.sessionData[msg.RunID]
		sess.History = append(sess.History, msg.Msg)
		_ = appendJSONL(sess.HistoryPath, msg.Msg)
		if msg.RunID == m.currentRunID() && m.stickToBottom {
			m.cursorLine = -1
		}
	case tuiSetBusyMsg:
		m.busy = msg.Busy
		if !m.busy {
			m.maybeProcessGatewayInbox()
		}
	case tuiSetNoticeMsg:
		m.notice = strings.TrimSpace(msg.Text)
	case tuiRunManifestUpdatedMsg:
		if msg.Err != nil {
			m.notice = strings.TrimSpace(msg.Err.Error())
			return
		}
		id := strings.TrimSpace(msg.RunID)
		if id == "" {
			id = strings.TrimSpace(msg.Run.ID)
		}
		if id == "" {
			return
		}
		for i := range m.sessions {
			if m.sessions[i].ID == id {
				m.sessions[i] = msg.Run
				break
			}
		}
	case tuiGatewayStatusMsg:
		m.gatewayStatus = msg.Status
	case tuiGatewayInboundMsg:
		m.gatewayInbox = append(m.gatewayInbox, msg.Msg)
		m.maybeProcessGatewayInbox()
	default:
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
	case "ctrl+d":
		runID := strings.TrimSpace(m.currentRunID())
		if runID == "" {
			return true, nil
		}
		if strings.TrimSpace(m.deleteConfirmRunID) == runID && !m.deleteConfirmAt.IsZero() && time.Since(m.deleteConfirmAt) < 3*time.Second {
			m.deleteConfirmRunID = ""
			m.deleteConfirmAt = time.Time{}
			m.notice = ""
			return true, tuiDeleteSessionCmd(m.coord, runID)
		}
		m.deleteConfirmRunID = runID
		m.deleteConfirmAt = time.Now().UTC()
		m.notice = "Press Ctrl+D again to delete this session."
		m.rerender()
		return true, nil
	case "tab":
		if len(m.agentIDs) > 0 {
			m.agentIndex = (m.agentIndex + 1) % len(m.agentIDs)
		}
		m.cursorLine = -1
		m.stickToBottom = true
		m.rerender()
		return true, nil
	case "ctrl+t":
		m.showDone = !m.showDone
		m.refreshAgentIDs()
		m.cursorLine = -1
		m.stickToBottom = true
		m.rerender()
		return true, nil
	case "shift+up", "alt+up":
		m.selectSession(-1)
		return true, nil
	case "shift+down", "alt+down":
		m.selectSession(1)
		return true, nil
	case "up", "k":
		m.moveCursor(-1)
		return true, nil
	case "down", "j":
		m.moveCursor(1)
		return true, nil
	case "left", "pgup":
		m.pageCursor(-1)
		return true, nil
	case "right", "pgdown":
		m.pageCursor(1)
		return true, nil
	case "enter":
		if m.sessionCursor == -1 {
			m.notice = ""
			return true, tuiCreateSessionCmd(m.coord)
		}
		if m.currentAgentID() != tuiPrimaryAgentID {
			if strings.TrimSpace(m.input.Value()) == "" {
				m.toggleToolAtCursor()
				return true, nil
			}
			m.notice = "Sub-agent view is read-only. Press TAB to return to primary chat."
			m.rerender()
			return true, nil
		}
		if strings.TrimSpace(m.input.Value()) == "" {
			m.toggleToolAtCursor()
			return true, nil
		}
		cmd := m.submitInput()
		return true, cmd
	case "ctrl+l":
		m.stickToBottom = true
		m.cursorLine = -1
		m.rerender()
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
	if strings.TrimSpace(m.gatewayConfigPath) != "" {
		headerH = 4
	}
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
	ui, _ := m.coord.ReadRunUIState(runID)
	hidden := ui.HiddenAgents
	states, err := m.coord.ListAgentStates(runID)
	if err != nil {
		return
	}
	ids := make([]string, 0, len(states)+1)
	ids = append(ids, tuiPrimaryAgentID)
	hiddenDone := 0
	hiddenManual := 0
	for _, st := range states {
		if st.AgentID == tuiPrimaryAgentID {
			continue
		}
		if _, ok := hidden[st.AgentID]; ok {
			hiddenManual++
			continue
		}
		status := strings.ToLower(strings.TrimSpace(st.Status))
		if status == multiagent.StatusFailed {
			ids = append(ids, st.AgentID)
			continue
		}
		if !multiagent.IsTerminalStatus(status) {
			ids = append(ids, st.AgentID)
			continue
		}
		if m.showDone {
			ids = append(ids, st.AgentID)
			continue
		}
		hiddenDone++
	}
	sort.Strings(ids[1:])

	current := m.currentAgentID()
	m.agentIDs = ids
	m.hiddenDone = hiddenDone
	m.hiddenManual = hiddenManual
	m.agentIndex = 0
	for i, id := range m.agentIDs {
		if id == current {
			m.agentIndex = i
			break
		}
	}
}

func (m *tuiModel) selectSession(delta int) {
	if delta == 0 {
		return
	}
	if len(m.sessions) == 0 {
		m.sessionCursor = -1
		m.rerender()
		return
	}

	cur := m.sessionCursor
	if cur < -1 || cur >= len(m.sessions) {
		cur = m.sessionIndex
	}

	total := len(m.sessions) + 1 // +1 for "New session"
	pos := cur + 1
	nextPos := (pos + delta) % total
	if nextPos < 0 {
		nextPos += total
	}
	next := nextPos - 1
	m.sessionCursor = next

	if next >= 0 {
		if next != m.sessionIndex {
			m.sessionIndex = next
			m.ensureSessionLoaded(m.currentRunID())
			m.createPrimaryAgent(m.currentRunID())
			m.refreshAgentIDs()
			m.cursorLine = -1
			m.stickToBottom = true
		}
	}
	m.rerender()
}

func (m *tuiModel) moveCursor(delta int) {
	total := len(m.lineToolKeys)
	if total == 0 || delta == 0 {
		m.cursorLine = -1
		return
	}
	if m.cursorLine < 0 {
		m.cursorLine = 0
	}
	next := m.cursorLine + delta
	if next < 0 {
		next = 0
	}
	if next >= total {
		next = total - 1
	}
	m.cursorLine = next
	m.stickToBottom = m.cursorLine >= total-1
	m.rerender()
}

func (m *tuiModel) pageCursor(deltaPages int) {
	if deltaPages == 0 {
		return
	}
	step := m.viewport.Height
	if step <= 0 {
		step = 10
	}
	m.moveCursor(deltaPages * step)
}

func (m *tuiModel) toggleToolAtCursor() {
	if m.cursorLine < 0 || m.cursorLine >= len(m.lineToolKeys) {
		return
	}
	key := strings.TrimSpace(m.lineToolKeys[m.cursorLine])
	if key == "" {
		return
	}
	m.expandedTools[key] = !m.expandedTools[key]
	m.stickToBottom = false
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
	case "/restart":
		if m.agent != nil && m.agent.RestartManager != nil {
			_, _, _ = m.agent.RestartManager.RequestRestart(restart.SentinelEntry{
				App:     appinfo.Name,
				Version: appinfo.Version,
				Reason:  "user",
				Note:    "relaunch requested",
				RunID:   strings.TrimSpace(runID),
			})
		}
		return tea.Quit
	case "/mcp reload", "/mcp-reload":
		m.busy = true
		m.notice = ""
		m.stickToBottom = true
		m.cursorLine = -1
		m.rerender()
		go m.runMCPReload(runID)
		return nil
	}
	if m.agent.shouldTriggerNaturalLanguageMCPReload(text) {
		m.busy = true
		m.notice = ""
		m.stickToBottom = true
		m.cursorLine = -1
		m.rerender()
		go m.runMCPReload(runID)
		return nil
	}

	m.ensureSessionLoaded(runID)
	m.createPrimaryAgent(runID)
	sess := m.sessionData[runID]
	base := append([]llm.Message(nil), sess.History...)
	shouldSetTitle := len(base) == 0 && !m.runHasTitle(runID)

	userMsg := llm.Message{Role: "user", Content: text}
	sess.History = append(sess.History, userMsg)
	_ = appendJSONL(sess.HistoryPath, userMsg)

	m.busy = true
	m.notice = ""
	m.stickToBottom = true
	m.cursorLine = -1
	m.rerender()
	go m.runTurn(runID, text, base, shouldSetTitle)
	return nil
}

func (m *tuiModel) runMCPReload(runID string) {
	events := m.events
	agentRef := m.agent
	ctx := m.ctx

	defer func() {
		events <- tuiAsyncMsg{Event: tuiSetBusyMsg{Busy: false}}
	}()

	msg, err := agentRef.reloadMCP(ctx)
	content := msg
	if err != nil {
		content = "MCP reload failed: " + err.Error()
	}
	events <- tuiAsyncMsg{Event: tuiAppendHistoryMsg{
		RunID: runID,
		Msg:   llm.Message{Role: "system", Content: content},
	}}
	if err != nil {
		events <- tuiAsyncMsg{Event: tuiSetNoticeMsg{Text: err.Error()}}
	}
}

func (m *tuiModel) runTurn(runID string, userText string, baseHistory []llm.Message, shouldSetTitle bool) {
	events := m.events
	agentRef := m.agent
	coord := m.coord
	ctx := m.ctx

	defer func() {
		events <- tuiAsyncMsg{Event: tuiSetBusyMsg{Busy: false}}
	}()

	if shouldSetTitle && coord != nil {
		if title := generateSessionTitle(ctx, agentRef, userText); strings.TrimSpace(title) != "" {
			updated, err := coord.SetRunTitle(runID, title)
			events <- tuiAsyncMsg{Event: tuiRunManifestUpdatedMsg{RunID: runID, Run: updated, Err: err}}
		}
	}

	emit := func(msg llm.Message) {
		events <- tuiAsyncMsg{Event: tuiAppendHistoryMsg{
			RunID: runID,
			Msg:   msg,
		}}
	}

	afterTool := func(call llm.ToolCall, result string, callErr error) []llm.Message {
		return m.maybeBuildWaitCompletionSystemMessages(runID, call, result, callErr)
	}
	if err := runTUITurnStreaming(ctx, agentRef, runID, userText, baseHistory, emit, afterTool); err != nil {
		events <- tuiAsyncMsg{Event: tuiSetNoticeMsg{Text: err.Error()}}
	}
}

func (m *tuiModel) runHasTitle(runID string) bool {
	id := strings.TrimSpace(runID)
	if id == "" {
		return false
	}
	for _, run := range m.sessions {
		if strings.TrimSpace(run.ID) != id {
			continue
		}
		if run.Metadata == nil {
			return false
		}
		if v, ok := run.Metadata["title"]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return true
			}
		}
		return false
	}
	return false
}

func generateSessionTitle(ctx context.Context, a *Agent, userText string) string {
	if a == nil || a.Client == nil {
		return ""
	}
	text := strings.TrimSpace(userText)
	if text == "" {
		return ""
	}

	if ctx == nil {
		ctx = context.Background()
	}
	titleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sys := llm.Message{
		Role: "system",
		Content: strings.Join([]string{
			"You are a session management agent. Create a concise chat session title based on the user's first message.",
			"Rules:",
			"- Output ONLY the title (no quotes, no markdown, no extra text).",
			"- Use the user's language when possible.",
			"- Keep it short (<= 12 words, or <= 20 Chinese characters).",
			"- Avoid trailing punctuation.",
		}, "\n"),
	}
	resp, err := a.Client.Chat(titleCtx, llm.ChatRequest{
		Messages: []llm.Message{
			sys,
			{Role: "user", Content: text},
		},
		Temperature: 0.2,
	})
	if err != nil || len(resp.Choices) == 0 {
		return fallbackSessionTitle(text)
	}

	title := strings.TrimSpace(resp.Choices[0].Message.Content)
	if i := strings.IndexByte(title, '\n'); i >= 0 {
		title = title[:i]
	}
	title = strings.Trim(title, `"'`+"`")
	title = strings.TrimSpace(title)
	if title == "" {
		return fallbackSessionTitle(text)
	}
	return title
}

func fallbackSessionTitle(userText string) string {
	text := safeOneLine(userText, 80)
	text = strings.Trim(text, `"'`+"`")
	text = strings.TrimSpace(text)
	if text == "" {
		return "New session"
	}
	return text
}

func runTUITurnStreaming(ctx context.Context, a *Agent, runID string, userText string, baseHistory []llm.Message, emit func(llm.Message), afterTool func(call llm.ToolCall, result string, callErr error) []llm.Message) error {
	if a == nil || a.Client == nil {
		return errors.New("agent is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return nil
	}
	if emit == nil {
		emit = func(llm.Message) {}
	}

	systemMsg := llm.Message{Role: "system", Content: a.SystemPrompt}
	sessionMsg := llm.Message{
		Role: "system",
		Content: fmt.Sprintf(
			"Session context: run_id=%s. Use this run_id for agent_* tools unless the user specifies otherwise.",
			strings.TrimSpace(runID),
		),
	}
	toolCtx := multiagent.WithSessionRunID(ctx, runID)

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
	reqMessages = append(reqMessages, pruneHistoryAfterLastAutoCompaction(baseHistory)...)
	reqMessages = append(reqMessages, turnHistory...)

	for {
		resp, sentMessages, err := chatWithAutoCompaction(ctx, a.Client, llm.ChatRequest{
			Messages:    reqMessages,
			Tools:       toolDefs,
			Temperature: a.Temperature,
		}, a.AutoCompaction, func(summary llm.Message) {
			emit(summary)
		})
		if err != nil {
			return err
		}
		reqMessages = sentMessages
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
		emit(msg)
		turnHistory = append(turnHistory, msg)
		reqMessages = append(reqMessages, msg)

		if len(msg.ToolCalls) == 0 {
			break
		}

		needsAutoMCPReload := false
		for _, call := range msg.ToolCalls {
			start := time.Now()
			result, callErr := a.callToolWithPolicy(toolCtx, call, &policy)
			_ = time.Since(start)

			toolMsg := llm.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			}
			if callErr != nil {
				toolMsg.Content = "ERROR: " + callErr.Error()
			}
			emit(toolMsg)
			turnHistory = append(turnHistory, toolMsg)
			reqMessages = append(reqMessages, toolMsg)

			if afterTool != nil {
				for _, extra := range afterTool(call, result, callErr) {
					if strings.TrimSpace(extra.Role) == "" {
						continue
					}
					emit(extra)
					turnHistory = append(turnHistory, extra)
					reqMessages = append(reqMessages, extra)
				}
			}

			if a.shouldTriggerAutoMCPReloadAfterToolCall(call, callErr) {
				needsAutoMCPReload = true
			}
			if a.RestartManager != nil && a.RestartManager.IsRestartRequested() {
				return nil
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
				emit(autoMsg)
				turnHistory = append(turnHistory, autoMsg)
				reqMessages = append(reqMessages, autoMsg)
			}
		}
	}

	return nil
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
		m.viewport.SetYOffset(0)
		m.lineToolKeys = nil
		m.cursorLine = -1
		return
	}

	width := m.viewport.Width
	if width <= 0 {
		width = 80
	}
	contentWidth := max(10, width-2)

	lines := m.buildConversationLines(runID, m.currentAgentID(), contentWidth)
	m.lineToolKeys = make([]string, len(lines))
	if len(lines) == 0 {
		m.cursorLine = -1
		m.viewport.SetContent("")
		m.viewport.SetYOffset(0)
		return
	}

	if m.stickToBottom {
		m.cursorLine = len(lines) - 1
	}
	if m.cursorLine < 0 {
		m.cursorLine = 0
	}
	if m.cursorLine >= len(lines) {
		m.cursorLine = len(lines) - 1
	}
	if m.cursorLine >= len(lines)-1 {
		m.stickToBottom = true
	}

	rendered := make([]string, 0, len(lines))
	for i, line := range lines {
		m.lineToolKeys[i] = strings.TrimSpace(line.ToolKey)
		arrow := " "
		if i == m.cursorLine {
			arrow = ">"
		}
		rendered = append(rendered, arrow+" "+truncateANSI(line.Text, contentWidth))
	}

	m.viewport.SetContent(strings.Join(rendered, "\n"))
	m.adjustViewportForCursor(len(lines))
}

func (m *tuiModel) adjustViewportForCursor(totalLines int) {
	if totalLines <= 0 || m.viewport.Height <= 0 {
		m.viewport.SetYOffset(0)
		return
	}
	maxYOffset := max(0, totalLines-m.viewport.Height)
	if m.stickToBottom {
		m.viewport.SetYOffset(maxYOffset)
		return
	}

	y := m.viewport.YOffset
	if m.cursorLine < y {
		y = m.cursorLine
	} else if m.cursorLine >= y+m.viewport.Height {
		y = m.cursorLine - m.viewport.Height + 1
	}
	y = clamp(0, y, maxYOffset)
	m.viewport.SetYOffset(y)
}

func (m *tuiModel) spinner() string {
	if len(tuiSpinnerFrames) == 0 {
		return ""
	}
	return tuiSpinnerFrames[m.spinnerFrame%len(tuiSpinnerFrames)]
}

type tuiLine struct {
	Text    string
	ToolKey string
}

func (m *tuiModel) buildConversationLines(runID string, agentID string, width int) []tuiLine {
	if runID == "" {
		return nil
	}
	if agentID == tuiPrimaryAgentID {
		m.ensureSessionLoaded(runID)
		sess := m.sessionData[runID]
		if sess == nil {
			return nil
		}
		return buildPrimaryLines(runID, agentID, sess.History, width, m.expandedTools, m.spinner())
	}
	return m.buildSubagentLines(runID, agentID, width)
}

func buildPrimaryLines(runID string, agentID string, history []llm.Message, width int, expanded map[string]bool, spinner string) []tuiLine {
	if width <= 0 {
		width = 80
	}

	userStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	assistantStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	systemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)

	toolResults := make(map[string]string, 32)
	toolSeen := make(map[string]bool, 32)
	for _, msg := range history {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			id := strings.TrimSpace(msg.ToolCallID)
			if id == "" {
				continue
			}
			toolResults[id] = msg.Content
			toolSeen[id] = true
		}
	}

	out := make([]tuiLine, 0, max(64, len(history)*2))
	addWrapped := func(prefix string, style lipgloss.Style, text string) {
		out = append(out, wrapPrefixedLines(prefix, style, text, width)...)
	}
	addBlank := func() {
		if len(out) == 0 || strings.TrimSpace(out[len(out)-1].Text) != "" {
			out = append(out, tuiLine{Text: ""})
		}
	}

	for _, msg := range history {
		switch strings.ToLower(strings.TrimSpace(msg.Role)) {
		case "user":
			addWrapped("You: ", userStyle, msg.Content)
			addBlank()
		case "assistant":
			hadContent := false
			if strings.TrimSpace(msg.Content) != "" {
				addWrapped("AI:  ", assistantStyle, msg.Content)
				hadContent = true
			}
			hadToolCalls := false
			for _, call := range msg.ToolCalls {
				hadToolCalls = true
				uiKey := toolUIKey(runID, agentID, call.ID)
				tv := &toolView{
					Key:       uiKey,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
					Result:    toolResults[call.ID],
				}
				if toolSeen[call.ID] {
					tv.Status = "ok"
					if strings.HasPrefix(strings.TrimSpace(tv.Result), "ERROR:") {
						tv.Status = "error"
					}
				}
				isExpanded := expanded != nil && expanded[uiKey]
				out = append(out, tuiLine{
					Text:    renderToolLine(tv, width, isExpanded, spinner),
					ToolKey: uiKey,
				})
				if isExpanded {
					out = append(out, renderToolDetails(tv, width)...)
				}
			}
			if hadContent || hadToolCalls {
				addBlank()
			}
		case "system":
			addWrapped("SYS: ", systemStyle, msg.Content)
			addBlank()
		}
	}

	return out
}

func wrapPrefixedLines(prefix string, style lipgloss.Style, text string, width int) []tuiLine {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	prefixWidth := runewidth.StringWidth(prefix)
	contentWidth := max(10, width-prefixWidth)
	wrapped := wrapText(text, contentWidth)

	lines := strings.Split(wrapped, "\n")
	out := make([]tuiLine, 0, len(lines))
	indent := strings.Repeat(" ", len(prefix))
	for i, line := range lines {
		if i == 0 {
			out = append(out, tuiLine{Text: style.Render(prefix + line)})
			continue
		}
		out = append(out, tuiLine{Text: style.Render(indent + line)})
	}
	return out
}

func (m *tuiModel) buildSubagentLines(runID string, agentID string, width int) []tuiLine {
	spec, _ := m.coord.ReadAgentSpec(runID, agentID)
	state, _ := m.coord.ReadAgentState(runID, agentID)
	events, _ := multiagent.TailJSONL[multiagent.AgentEvent](m.coord.AgentEventsPath(runID, agentID), 240, 256*1024)
	result, _ := m.coord.ReadResult(runID, agentID)

	out := make([]tuiLine, 0, 256)
	heading := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	out = append(out, tuiLine{Text: heading.Render(fmt.Sprintf("%s [%s]", agentID, strings.TrimSpace(state.Status)))})

	if strings.TrimSpace(spec.Task) != "" {
		appendBlockLines(&out, wrapText("Task: "+spec.Task, width))
		out = append(out, tuiLine{Text: ""})
	}

	tools := subagentToolViews(events)
	for _, tv := range tools {
		uiKey := toolUIKey(runID, agentID, tv.Key)
		tv.Key = uiKey
		isExpanded := m.expandedTools != nil && m.expandedTools[uiKey]
		out = append(out, tuiLine{
			Text:    renderToolLine(tv, width, isExpanded, m.spinner()),
			ToolKey: uiKey,
		})
		if isExpanded {
			out = append(out, renderToolDetails(tv, width)...)
		}
	}

	if strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Error) != "" {
		out = append(out, tuiLine{Text: ""})
		out = append(out, tuiLine{Text: heading.Render("Final Output")})
		if strings.TrimSpace(result.Output) != "" {
			appendBlockLines(&out, wrapText(result.Output, width))
		}
		if strings.TrimSpace(result.Error) != "" {
			red := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
			for _, line := range strings.Split(wrapText("Error: "+result.Error, width), "\n") {
				out = append(out, tuiLine{Text: red.Render(line)})
			}
		}
	}

	return out
}

func appendBlockLines(dst *[]tuiLine, block string) {
	if dst == nil || block == "" {
		return
	}
	for _, line := range strings.Split(block, "\n") {
		*dst = append(*dst, tuiLine{Text: line})
	}
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

func renderToolLine(tv *toolView, width int, expanded bool, spinner string) string {
	if tv == nil {
		return ""
	}

	toolStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	summaryStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	disclosure := "▸"
	if expanded {
		disclosure = "▾"
	}

	status := strings.ToLower(strings.TrimSpace(tv.Status))
	if status == "" {
		switch {
		case strings.TrimSpace(tv.Error) != "" || strings.HasPrefix(strings.TrimSpace(tv.Result), "ERROR:"):
			status = "error"
		case strings.TrimSpace(tv.Result) != "":
			status = "ok"
		default:
			status = "running"
		}
	}

	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	switch status {
	case "ok", "completed", "success":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case "error", "failed":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	case "running":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	}

	argsSummary := summarizeToolArgs(tv.Arguments)
	resultSummary := ""
	if status == "running" {
		if strings.TrimSpace(spinner) != "" {
			resultSummary = spinner
		} else {
			resultSummary = "…"
		}
	} else {
		if strings.TrimSpace(tv.Error) != "" && strings.TrimSpace(tv.Result) == "" {
			resultSummary = safeOneLine(tv.Error, 100)
		} else {
			resultSummary = summarizeToolResult(tv.Result)
		}
	}

	var b strings.Builder
	b.WriteString(summaryStyle.Render(disclosure))
	b.WriteString(" ")
	b.WriteString(toolStyle.Render(strings.TrimSpace(tv.Name)))
	if strings.TrimSpace(argsSummary) != "" {
		b.WriteString(" ")
		b.WriteString(summaryStyle.Render(argsSummary))
	}
	b.WriteString(" -> ")
	b.WriteString(statusStyle.Render(status))
	if strings.TrimSpace(resultSummary) != "" {
		b.WriteString(" ")
		b.WriteString(summaryStyle.Render(resultSummary))
	}
	line := b.String()
	return truncateANSI(line, width)
}

func renderToolDetails(tv *toolView, width int) []tuiLine {
	if tv == nil {
		return nil
	}
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	lines := make([]tuiLine, 0, 24)
	if strings.TrimSpace(tv.Arguments) != "" {
		lines = append(lines, tuiLine{Text: dim.Render("    args:")})
		for _, line := range strings.Split(formatJSON(tv.Arguments), "\n") {
			lines = append(lines, tuiLine{Text: dim.Render("      " + truncateANSI(line, width-6))})
		}
	}
	resultText := strings.TrimRight(strings.TrimSpace(tv.Result), "\n")
	if resultText == "" {
		resultText = strings.TrimRight(strings.TrimSpace(tv.Error), "\n")
	}
	if resultText != "" {
		lines = append(lines, tuiLine{Text: dim.Render("    result:")})
		for _, line := range strings.Split(resultText, "\n") {
			lines = append(lines, tuiLine{Text: dim.Render("      " + truncateANSI(line, width-6))})
		}
	}
	if len(lines) > 0 {
		lines = append(lines, tuiLine{Text: ""})
	}
	return lines
}

func summarizeToolArgs(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !json.Valid([]byte(trimmed)) {
		return fmt.Sprintf("args=%dB", len(trimmed))
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return fmt.Sprintf("args=%dB", len(trimmed))
	}
	if len(obj) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, min(3, len(keys)))
	for _, k := range keys {
		if isSensitiveKey(k) {
			parts = append(parts, k+"=<redacted>")
		} else {
			parts = append(parts, k)
		}
		if len(parts) >= 3 {
			break
		}
	}
	more := ""
	if len(keys) > len(parts) {
		more = fmt.Sprintf(" +%d", len(keys)-len(parts))
	}
	return "{" + strings.Join(parts, ", ") + more + "}"
}

func formatArgPreview(key string, v any) string {
	if isSensitiveKey(key) {
		return "<redacted>"
	}
	switch t := v.(type) {
	case string:
		s := safeOneLine(t, 26)
		if s == "" {
			return `""`
		}
		return fmt.Sprintf("%q", s)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%.3g", t)
	case bool:
		return fmt.Sprintf("%t", t)
	case nil:
		return "null"
	case []any:
		return fmt.Sprintf("[%d]", len(t))
	case map[string]any:
		return "{…}"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return false
	}
	sensitive := []string{"api_key", "apikey", "token", "secret", "password", "passwd", "authorization", "bearer", "cookie"}
	for _, needle := range sensitive {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func summarizeToolResult(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if parsed, ok := parseMCPResult(trimmed); ok {
		switch {
		case strings.TrimSpace(parsed.Text) != "":
			return safeOneLine(parsed.Text, 90)
		case strings.TrimSpace(parsed.Structured) != "":
			return "structured"
		case strings.TrimSpace(parsed.RawJSON) != "":
			return "json"
		}
	}
	if strings.HasPrefix(strings.TrimSpace(trimmed), "ERROR:") {
		return safeOneLine(strings.TrimSpace(trimmed), 140)
	}
	return safeOneLine(trimmed, 110)
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

	cursor := m.sessionCursor
	if cursor < -1 || cursor >= len(m.sessions) {
		cursor = m.sessionIndex
	}

	newPrefix := "  "
	if cursor == -1 {
		newPrefix = "> "
	}
	newStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	lines = append(lines, truncateANSI(newPrefix+newStyle.Render("+ New session"), width-1))
	lines = append(lines, "")

	activeStyle := lipgloss.NewStyle().Bold(true)
	for i, run := range m.sessions {
		prefix := "  "
		if i == cursor {
			prefix = "> "
		}
		label := multiagent.RunTitle(run)
		if i == m.sessionIndex {
			label = activeStyle.Render(label)
		}
		line := fmt.Sprintf("%s%s", prefix, label)
		lines = append(lines, truncateANSI(line, width-1))
	}
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	hints := []string{
		truncateANSI(hintStyle.Render("-Sessions: Shift+↑/↓"), width-1),
		truncateANSI(hintStyle.Render("-New session: Enter"), width-1),
		truncateANSI(hintStyle.Render("-Delete session: Ctrl+D"), width-1),
	}
	if height > 0 {
		need := height - len(lines) - len(hints)
		for need > 0 {
			lines = append(lines, "")
			need--
		}
	}
	lines = append(lines, hints...)
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

	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	if m.hiddenManual > 0 {
		hidden := fmt.Sprintf("hidden archived: %d (agent_subagent_list scope=hidden)", m.hiddenManual)
		lines = append(lines, truncateANSI(hintStyle.Render(hidden), width-1))
	}
	if !m.showDone && m.hiddenDone > 0 {
		hidden := fmt.Sprintf("hidden finished: %d (Ctrl+T to show)", m.hiddenDone)
		lines = append(lines, truncateANSI(hintStyle.Render(hidden), width-1))
	}
	hint := truncateANSI(hintStyle.Render("TAB: switch agent | Ctrl+T: toggle finished"), width-1)
	if height > 0 {
		need := height - len(lines) - 1
		for need > 0 {
			lines = append(lines, "")
			need--
		}
	}
	lines = append(lines, hint)
	return style.Render(strings.Join(lines, "\n"))
}

func (m *tuiModel) renderCenter(width int, height int) string {
	border := lipgloss.NewStyle().Width(width).Height(height)
	if width <= 0 || height <= 0 {
		return ""
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	headerText := appinfo.Display()
	if m.busy && strings.TrimSpace(m.spinner()) != "" {
		headerText += " " + m.spinner()
	}
	header := headerStyle.Render(headerText)

	sessionID := strings.TrimSpace(m.currentRunID())
	if sessionID == "" {
		sessionID = "-"
	}
	subHeader := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(fmt.Sprintf("Session: %s | Agent: %s", sessionID, m.currentAgentID()))
	infoLines := make([]string, 0, 2)
	if strings.TrimSpace(m.notice) != "" {
		infoLines = append(infoLines, lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(
			truncateANSI("Error: "+strings.TrimSpace(m.notice), max(10, width-2)),
		))
	} else if strings.TrimSpace(m.banner) != "" {
		infoLines = append(infoLines, lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(
			truncateANSI(strings.TrimSpace(m.banner), max(10, width-2)),
		))
	}

	gatewayLine := m.renderGatewayLine(width)
	if gatewayLine != "" {
		infoLines = append(infoLines, gatewayLine)
	}

	headerParts := []string{header, subHeader}
	headerParts = append(headerParts, infoLines...)
	headerBlock := lipgloss.NewStyle().Padding(0, 1).Render(strings.Join(headerParts, "\n"))

	vp := m.viewport.View()
	inputLine := m.renderInputLine(width)

	content := lipgloss.JoinVertical(lipgloss.Left, headerBlock, vp, inputLine)
	return border.Render(content)
}

func (m *tuiModel) renderGatewayLine(width int) string {
	if m == nil {
		return ""
	}
	if strings.TrimSpace(m.gatewayConfigPath) == "" {
		return ""
	}

	maxW := max(10, width-2)
	queueN := len(m.gatewayInbox)
	queueText := ""
	if queueN > 0 {
		queueText = fmt.Sprintf(" | 待处理: %d", queueN)
	}

	if !m.gatewayEnabled || m.emailGateway == nil {
		text := "消息网关：已禁用" + queueText
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(truncateANSI(text, maxW))
	}

	emailAddr := strings.TrimSpace(m.gatewayEmail)
	if emailAddr == "" {
		emailAddr = strings.TrimSpace(m.emailGateway.Config().EmailAddress)
	}
	interval := m.emailGateway.Config().PollIntervalSeconds
	if interval <= 0 {
		interval = 5
	}

	if m.gatewayStatus.OK {
		text := fmt.Sprintf("消息网关：%s 连接正常 (轮询%ds)%s", emailAddr, interval, queueText)
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(truncateANSI(text, maxW))
	}
	errText := strings.TrimSpace(m.gatewayStatus.Error)
	if errText == "" {
		errText = "unknown error"
	}
	text := fmt.Sprintf("消息网关：%s 连接异常: %s%s", emailAddr, safeOneLine(errText, 120), queueText)
	return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(truncateANSI(text, maxW))
}

func (m *tuiModel) renderInputLine(width int) string {
	if m.busy {
		m.input.Blur()
		thinking := "Thinking"
		if strings.TrimSpace(m.spinner()) != "" {
			thinking += " " + m.spinner()
		} else {
			thinking += "…"
		}
		return lipgloss.NewStyle().Width(width).Padding(0, 1).Foreground(lipgloss.Color("8")).Render(thinking)
	}
	if m.currentAgentID() != tuiPrimaryAgentID {
		m.input.Blur()
		hintStyle := lipgloss.NewStyle().Width(width).Padding(0, 1).Foreground(lipgloss.Color("8"))
		msg := "Sub-agent view is read-only. Press TAB to return to primary chat; use agent_control to message sub-agents."
		return hintStyle.Render(truncateANSI(msg, max(10, width-2)))
	}
	m.input.Focus()
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
