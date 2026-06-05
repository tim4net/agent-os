package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/tim4net/agent-os/internal/tuiclient"
)

type sessionState int

const (
	chatView sessionState = iota
	agentPickerView
	historyView
)

// UI constants
var (
	accentColor = lipgloss.Color("#5e81ac") // Nord blue-ish purple feel
	textColor   = lipgloss.Color("#eceff4")
	dimColor    = lipgloss.Color("#4c566a")

	statusOnline  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a3be8c")).Render("●")
	statusOffline = lipgloss.NewStyle().Foreground(lipgloss.Color("#bf616a")).Render("●")
	statusUnknown = lipgloss.NewStyle().Foreground(lipgloss.Color("#ebcb8b")).Render("●")

	appStyle = lipgloss.NewStyle().Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Foreground(textColor).
			Background(accentColor).
			Padding(0, 1).
			Bold(true)

	userMessageStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#88c0d0")).
				Bold(true)

	assistantMessageStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#b48ead")).
				Bold(true)

	toolStyle = lipgloss.NewStyle().
			Foreground(dimColor).
			Italic(true)
)

// Chat message representation
type message struct {
	role    string // "user" or "assistant"
	content string
	raw     bool // if false, content is fully rendered via glamour
}

// Key map
type keyMap struct {
	SwitchAgent key.Binding
	History     key.Binding
	Quit        key.Binding
	Send        key.Binding
	Help        key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.SwitchAgent, k.History, k.Help, k.Quit}
}
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.SwitchAgent, k.History, k.Send},
		{k.Help, k.Quit},
	}
}

var keys = keyMap{
	SwitchAgent: key.NewBinding(
		key.WithKeys("tab", "ctrl+p"),
		key.WithHelp("tab", "switch agent"),
	),
	History: key.NewBinding(
		key.WithKeys("ctrl+r"),
		key.WithHelp("ctrl+r", "history"),
	),
	Quit: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("ctrl+c", "quit"),
	),
	Send: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "send message"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
	),
}

// Custom commands and messages
type agentsLoadedMsg []tuiclient.Agent
type agentLoadErrMsg error
type convsLoadedMsg []tuiclient.Conversation
type convMessagesLoadedMsg struct {
	convID   string
	messages []tuiclient.Message
}
type loadErrMsg error
type streamStartedMsg struct{ ch <-chan tuiclient.ChatEvent }
type streamEventMsg tuiclient.ChatEvent
type streamDoneMsg struct{}
type streamErrMsg error

// Agent list item
type agentItem struct {
	agent tuiclient.Agent
}

func (i agentItem) Title() string       { return i.agent.DisplayName }
func (i agentItem) Description() string { return fmt.Sprintf("Harness: %s", i.agent.Harness) }
func (i agentItem) FilterValue() string { return i.agent.DisplayName + " " + i.agent.Harness }

// Conversation list item
type convItem struct {
	conv tuiclient.Conversation
}

func (i convItem) Title() string {
	t := strings.TrimSpace(i.conv.Title)
	if t == "" {
		t = "(untitled)"
	}
	return t
}
func (i convItem) Description() string {
	// Prefer a short date portion of updated_at (RFC3339); fall back to summary.
	when := i.conv.UpdatedAt
	if len(when) >= 16 {
		when = strings.Replace(when[:16], "T", " ", 1)
	}
	if i.conv.Summary != nil && strings.TrimSpace(*i.conv.Summary) != "" {
		return fmt.Sprintf("%s · %s", when, strings.TrimSpace(*i.conv.Summary))
	}
	return when
}
func (i convItem) FilterValue() string { return i.conv.Title }

type model struct {
	client *tuiclient.Client
	state  sessionState

	// Chat components
	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model
	help     help.Model

	// Agent components
	agentList list.Model

	// Conversation history components
	convList list.Model

	// App state
	agents         []tuiclient.Agent
	activeAgent    *tuiclient.Agent
	conversationID string
	messages       []message
	currentStream  string // Buffer for currently streaming assistant message
	isStreaming    bool
	width          int
	height         int
	renderer       *glamour.TermRenderer
	cancelStream   context.CancelFunc
	eventChan      <-chan tuiclient.ChatEvent
}

func initialModel() model {
	baseURL := os.Getenv("AOS_API")
	if baseURL == "" {
		baseURL = "http://localhost:8420"
	}

	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 10000
	ta.SetHeight(3)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter"))

	vp := viewport.New(0, 0)
	vp.SetContent("Loading agents...")

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accentColor)

	h := help.New()

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(accentColor).BorderLeftForeground(accentColor)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(lipgloss.Color("#d8dee9")).BorderLeftForeground(accentColor)

	al := list.New([]list.Item{}, delegate, 0, 0)
	al.Title = "Select Agent"
	al.SetShowStatusBar(false)
	al.SetFilteringEnabled(true)
	al.Styles.Title = headerStyle

	cl := list.New([]list.Item{}, delegate, 0, 0)
	cl.Title = "Conversation History"
	cl.SetShowStatusBar(false)
	cl.SetFilteringEnabled(true)
	cl.Styles.Title = headerStyle

	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)

	return model{
		client:    tuiclient.NewClient(baseURL),
		state:     chatView,
		viewport:  vp,
		textarea:  ta,
		spinner:   sp,
		help:      h,
		agentList: al,
		convList:  cl,
		renderer:  r,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		m.loadAgentsCmd(),
	)
}

func (m model) loadAgentsCmd() tea.Cmd {
	return func() tea.Msg {
		agents, err := m.client.ListAgents(context.Background())
		if err != nil {
			return agentLoadErrMsg(err)
		}
		return agentsLoadedMsg(agents)
	}
}

func (m model) loadConvsCmd(agentID string) tea.Cmd {
	return func() tea.Msg {
		convs, err := m.client.ListConversations(context.Background(), agentID)
		if err != nil {
			return loadErrMsg(err)
		}
		return convsLoadedMsg(convs)
	}
}

func (m model) loadConvMessagesCmd(convID string) tea.Cmd {
	return func() tea.Msg {
		msgs, err := m.client.ListMessages(context.Background(), convID)
		if err != nil {
			return loadErrMsg(err)
		}
		return convMessagesLoadedMsg{convID: convID, messages: msgs}
	}
}

func (m *model) startStreamCmd(agentID string, msg string, convID string) tea.Cmd {
	return func() tea.Msg {
		req := tuiclient.ChatRequest{
			Message:        msg,
			ConversationID: convID,
		}

		events, err := m.client.StreamChat(context.Background(), agentID, req)
		if err != nil {
			return streamErrMsg(err)
		}

		// Hand the channel back to Update via a message so it is stored on the
		// REAL model (mutating m here only touches this goroutine's copy).
		return streamStartedMsg{ch: events}
	}
}

func waitForEventCmd(ch <-chan tuiclient.ChatEvent) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return streamDoneMsg{}
		}
		evt, ok := <-ch
		if !ok {
			return streamDoneMsg{}
		}
		return streamEventMsg(evt)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
		lsCmd tea.Cmd
		spCmd tea.Cmd
		cmds  []tea.Cmd
	)

	m.spinner, spCmd = m.spinner.Update(msg)
	cmds = append(cmds, spCmd)

	// Key handling is routed per-state FIRST so that command keys (switch agent,
	// history, help, quit, send) are consumed here and never leak into the
	// composer or sub-list as typed characters. Only non-command keys fall
	// through to the focused component (textarea in chat; the list in pickers).
	if km, ok := msg.(tea.KeyMsg); ok {
		switch m.state {
		case chatView:
			composerEmpty := strings.TrimSpace(m.textarea.Value()) == ""
			switch {
			case key.Matches(km, keys.Quit):
				return m, tea.Quit
			case km.String() == "q" && composerEmpty:
				return m, tea.Quit
			case key.Matches(km, keys.SwitchAgent):
				m.state = agentPickerView
				return m, tea.Batch(cmds...)
			case key.Matches(km, keys.History):
				// Open history for the active agent and load its conversations.
				if m.activeAgent != nil {
					m.state = historyView
					m.convList.SetItems(nil)
					m.convList.Title = "History · " + m.activeAgent.DisplayName
					cmds = append(cmds, m.loadConvsCmd(m.activeAgent.ID))
				}
				return m, tea.Batch(cmds...)
			case km.String() == "?" && composerEmpty:
				m.help.ShowAll = !m.help.ShowAll
				return m, tea.Batch(cmds...)
			case km.Type == tea.KeyEnter && !km.Alt:
				// Send message (Enter). Shift/Alt+Enter inserts a newline and is
				// handled by the textarea below.
				if !m.isStreaming && m.activeAgent != nil {
					userText := strings.TrimSpace(m.textarea.Value())
					if userText == "" {
						return m, tea.Batch(cmds...)
					}
					m.messages = append(m.messages, message{role: "user", content: userText, raw: false})
					m.textarea.Reset()
					m.isStreaming = true
					m.currentStream = ""
					m.updateViewport()
					cmds = append(cmds, m.startStreamCmd(m.activeAgent.ID, userText, m.conversationID))
				}
				return m, tea.Batch(cmds...)
			default:
				// Not a command — let the composer handle it (typing).
				m.textarea, tiCmd = m.textarea.Update(msg)
				m.viewport, vpCmd = m.viewport.Update(msg)
				cmds = append(cmds, tiCmd, vpCmd)
				return m, tea.Batch(cmds...)
			}

		case agentPickerView:
			if km.Type == tea.KeyEsc {
				m.state = chatView
				return m, tea.Batch(cmds...)
			}
			if km.Type == tea.KeyEnter {
				if i, ok := m.agentList.SelectedItem().(agentItem); ok {
					if m.activeAgent == nil || m.activeAgent.ID != i.agent.ID {
						m.activeAgent = &i.agent
						m.conversationID = ""
						m.messages = []message{}
						m.updateViewport()
					}
					m.state = chatView
				}
				return m, tea.Batch(cmds...)
			}
			// Everything else drives the agent list (navigation, filtering).
			m.agentList, lsCmd = m.agentList.Update(msg)
			cmds = append(cmds, lsCmd)
			return m, tea.Batch(cmds...)

		case historyView:
			if km.Type == tea.KeyEsc {
				m.state = chatView
				return m, tea.Batch(cmds...)
			}
			if km.Type == tea.KeyEnter {
				if i, ok := m.convList.SelectedItem().(convItem); ok {
					cmds = append(cmds, m.loadConvMessagesCmd(i.conv.ID))
				}
				return m, tea.Batch(cmds...)
			}
			// Everything else drives the conversation list.
			m.convList, lsCmd = m.convList.Update(msg)
			cmds = append(cmds, lsCmd)
			return m, tea.Batch(cmds...)
		}
	}

	// Non-key messages: keep the focused components ticking.
	m.textarea, tiCmd = m.textarea.Update(msg)
	cmds = append(cmds, tiCmd)
	switch m.state {
	case chatView:
		m.viewport, vpCmd = m.viewport.Update(msg)
		cmds = append(cmds, vpCmd)
	case agentPickerView:
		m.agentList, lsCmd = m.agentList.Update(msg)
		cmds = append(cmds, lsCmd)
	case historyView:
		m.convList, lsCmd = m.convList.Update(msg)
		cmds = append(cmds, lsCmd)
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		switch m.state {
		case chatView:
			m.viewport, vpCmd = m.viewport.Update(msg)
			cmds = append(cmds, vpCmd)
		case agentPickerView:
			m.agentList, lsCmd = m.agentList.Update(msg)
			cmds = append(cmds, lsCmd)
		case historyView:
			m.convList, lsCmd = m.convList.Update(msg)
			cmds = append(cmds, lsCmd)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		m.help.Width = msg.Width

		m.textarea.SetWidth(msg.Width - 2)
		m.agentList.SetSize(msg.Width-4, msg.Height-6)
		m.convList.SetSize(msg.Width-4, msg.Height-6)

		headerHeight := 2
		footerHeight := m.textarea.Height() + 2 // including help padding
		m.viewport.Width = msg.Width - 2
		m.viewport.Height = msg.Height - headerHeight - footerHeight

		if m.renderer != nil {
			m.renderer, _ = glamour.NewTermRenderer(
				glamour.WithAutoStyle(),
				glamour.WithWordWrap(msg.Width-4),
			)
			m.updateViewport()
		}

	case agentsLoadedMsg:
		m.agents = []tuiclient.Agent(msg)
		items := make([]list.Item, len(m.agents))
		var fallback *tuiclient.Agent
		var agy *tuiclient.Agent

		for i, a := range m.agents {
			items[i] = agentItem{agent: a}
			if fallback == nil && a.Status == "online" {
				fallback = &m.agents[i]
			}
			if a.Harness == "agy" {
				agy = &m.agents[i]
			}
		}
		m.agentList.SetItems(items)

		if m.activeAgent == nil && len(m.agents) > 0 {
			if agy != nil {
				m.activeAgent = agy
			} else if fallback != nil {
				m.activeAgent = fallback
			} else {
				m.activeAgent = &m.agents[0]
			}
			m.updateViewport()
		}

	case convsLoadedMsg:
		convs := []tuiclient.Conversation(msg)
		items := make([]list.Item, len(convs))
		for i, c := range convs {
			items[i] = convItem{conv: c}
		}
		m.convList.SetItems(items)

	case convMessagesLoadedMsg:
		// Load a past conversation into the chat view and continue it.
		m.conversationID = msg.convID
		m.messages = m.messages[:0]
		for _, mm := range msg.messages {
			if mm.Role != "user" && mm.Role != "assistant" {
				continue // skip system/tool rows in the transcript
			}
			// assistant messages render as markdown (raw=false); user messages plain.
			m.messages = append(m.messages, message{
				role:    mm.Role,
				content: mm.Content,
				raw:     mm.Role != "assistant",
			})
		}
		m.state = chatView
		m.isStreaming = false
		m.currentStream = ""
		m.updateViewport()

	case loadErrMsg:
		// Non-fatal: surface the failure in the transcript and return to chat.
		m.state = chatView
		m.messages = append(m.messages, message{role: "assistant", content: fmt.Sprintf("Load error: %v", error(msg)), raw: true})
		m.updateViewport()

	case streamErrMsg:
		m.isStreaming = false
		m.messages = append(m.messages, message{role: "assistant", content: fmt.Sprintf("Stream Error: %v", msg), raw: true})
		m.currentStream = ""
		m.updateViewport()
		m.eventChan = nil

	case streamDoneMsg:
		// channel closed
		if m.isStreaming {
			m.isStreaming = false
			m.messages = append(m.messages, message{role: "assistant", content: m.currentStream, raw: false})
			m.currentStream = ""
			m.updateViewport()
		}
		m.eventChan = nil

	case streamStartedMsg:
		// Store the channel on the real model, then begin reading events.
		m.eventChan = msg.ch
		cmds = append(cmds, waitForEventCmd(m.eventChan))

	case streamEventMsg:
		evt := tuiclient.ChatEvent(msg)
		if evt.Type == "error" {
			m.isStreaming = false
			m.messages = append(m.messages, message{role: "assistant", content: "Error: " + evt.Error, raw: true})
			m.currentStream = ""
			m.updateViewport()
			m.eventChan = nil
			return m, tea.Batch(cmds...)
		} else if evt.Type == "done" {
			m.isStreaming = false
			if evt.ConversationID != "" {
				m.conversationID = evt.ConversationID
			}
			m.messages = append(m.messages, message{role: "assistant", content: m.currentStream, raw: false})
			m.currentStream = ""
			m.updateViewport()
			m.eventChan = nil
			return m, tea.Batch(cmds...)
		} else {
			if evt.Type == "chunk" {
				m.currentStream += evt.Content
				m.updateViewport()
			} else if evt.Type == "tool" {
				m.currentStream += fmt.Sprintf("\n*⚙ %s (%s)*\n", evt.ToolName, evt.ToolStatus)
				m.updateViewport()
			}
			// Wait for the next event
			cmds = append(cmds, waitForEventCmd(m.eventChan))
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *model) updateViewport() {
	var content strings.Builder

	if m.activeAgent == nil {
		content.WriteString("Loading...")
		m.viewport.SetContent(content.String())
		return
	}

	if len(m.messages) == 0 && !m.isStreaming {
		content.WriteString(fmt.Sprintf("\nChat with %s\nType a message to start.", m.activeAgent.DisplayName))
		m.viewport.SetContent(content.String())
		return
	}

	for _, msg := range m.messages {
		if msg.role == "user" {
			content.WriteString(userMessageStyle.Render("You\n"))
			content.WriteString(msg.content + "\n\n")
		} else {
			content.WriteString(assistantMessageStyle.Render(fmt.Sprintf("%s\n", m.activeAgent.DisplayName)))
			if msg.raw {
				content.WriteString(msg.content + "\n\n")
			} else {
				rendered, err := m.renderer.Render(msg.content)
				if err != nil {
					content.WriteString(msg.content + "\n\n")
				} else {
					content.WriteString(rendered + "\n")
				}
			}
		}
	}

	if m.isStreaming {
		content.WriteString(assistantMessageStyle.Render(fmt.Sprintf("%s %s\n", m.activeAgent.DisplayName, m.spinner.View())))
		if m.currentStream != "" {
			content.WriteString(m.currentStream + "\n")
		}
	}

	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

func (m model) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	if m.state == agentPickerView {
		return appStyle.Render(m.agentList.View())
	}

	if m.state == historyView {
		return appStyle.Render(m.convList.View())
	}

	// Header
	baseURL := m.client.BaseURL
	headerText := "Agent OS"
	if m.activeAgent != nil {
		status := statusUnknown
		switch m.activeAgent.Status {
		case "online":
			status = statusOnline
		case "offline", "error":
			status = statusOffline
		}
		headerText = fmt.Sprintf("Agent OS · %s %s (%s) · %s", m.activeAgent.DisplayName, status, m.activeAgent.Harness, baseURL)
	}

	header := headerStyle.Width(m.width).Render(headerText)

	vp := m.viewport.View()
	ta := m.textarea.View()
	helpView := m.help.View(keys)

	return fmt.Sprintf("%s\n%s\n%s\n%s", header, vp, ta, helpView)
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
