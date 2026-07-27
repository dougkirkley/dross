// Package tui is holocron's terminal client, built on Charm's Bubble Tea and Lip
// Gloss — the "Crush feel" without any Crush code. Dark black/gray base with a
// Carolina-blue accent. It drives an agent in-process and streams its events
// into a scrolling log.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dougkirkley/holocron/internal/agent"
)

// --- palette: dark black/gray + Carolina blue -----------------------------

var (
	accent   = lipgloss.Color("#4B9CD3") // Carolina blue
	accentHi = lipgloss.Color("#8FC7E8") // lighter blue
	fg       = lipgloss.Color("#E6E6E6")
	muted    = lipgloss.Color("#6B7280")
	dim      = lipgloss.Color("#4B5158")
	bad      = lipgloss.Color("#E06C75")
	base     = lipgloss.Color("#0C0F12") // near-black
	panel    = lipgloss.Color("#15181C") // gray panel

	logoStyle   = lipgloss.NewStyle().Foreground(accent).Bold(true)
	metaStyle   = lipgloss.NewStyle().Foreground(muted)
	costStyle   = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
	headerBar   = lipgloss.NewStyle().Background(panel).Padding(0, 1)
	ruleStyle   = lipgloss.NewStyle().Foreground(dim)
	youLabel    = lipgloss.NewStyle().Foreground(muted).Bold(true)
	agentLabel  = lipgloss.NewStyle().Foreground(accent).Bold(true)
	agentText   = lipgloss.NewStyle().Foreground(fg)
	toolArrow   = lipgloss.NewStyle().Foreground(accent)
	toolName    = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
	toolArgs    = lipgloss.NewStyle().Foreground(muted)
	resultStyle = lipgloss.NewStyle().Foreground(dim)
	workerTag   = lipgloss.NewStyle().Foreground(lipgloss.Color("#5C8AA6"))
	checkStyle  = lipgloss.NewStyle().Foreground(accentHi)
	errStyle    = lipgloss.NewStyle().Foreground(bad)
	promptMark  = lipgloss.NewStyle().Foreground(accent).Bold(true)
	inputBox    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(0, 1)
)

// EventMsg carries an agent event into the Bubble Tea loop.
type EventMsg agent.Event

// doneMsg signals a run finished.
type doneMsg struct {
	res *agent.Result
	err error
}

// Model is the TUI state.
type Model struct {
	agentName string
	model     string
	provider  string
	cost      float64
	turns     int

	vp          viewport.Model
	input       textinput.Model
	sp          spinner.Model
	running     bool
	width       int
	height      int
	logoVariant int

	lines []string // rendered log lines

	run  func(ctx context.Context, prompt string, emit func(agent.Event)) (*agent.Result, error)
	msgs chan tea.Msg
}

// RenderRun executes one real prompt synchronously, feeding its events into a
// TUI model, and returns the final rendered frame. Used to capture a live run
// (against a real model) as a single screenshot without keystroke injection.
func RenderRun(ctx context.Context, width, height int, agentName, model, provider string,
	run func(ctx context.Context, prompt string, emit func(agent.Event)) (*agent.Result, error),
	prompt string) string {
	m := New(agentName, model, provider, run)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  " + prompt)
	res, err := run(ctx, prompt, func(e agent.Event) { m.applyEvent(e) })
	if err != nil {
		m.applyEvent(agent.Event{Kind: "error", Data: err.Error()})
	}
	if res != nil {
		m.cost = res.Cost()
		m.turns = res.Turns
	}
	return m.View()
}

// New builds a TUI. run executes a prompt, calling emit for each event.
func New(agentName, model, provider string, run func(ctx context.Context, prompt string, emit func(agent.Event)) (*agent.Result, error)) Model {
	ti := textinput.New()
	ti.Placeholder = "ask holocron to build, edit, or run something…"
	ti.Prompt = ""
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accent)

	return Model{
		agentName:   agentName,
		model:       model,
		provider:    provider,
		input:       ti,
		sp:          sp,
		run:         run,
		msgs:        make(chan tea.Msg, 256),
		logoVariant: 1, // gradient fade (default)
	}
}

func (m Model) Init() tea.Cmd { return textinput.Blink }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			if !m.running && strings.TrimSpace(m.input.Value()) != "" {
				prompt := m.input.Value()
				m.append(youLabel.Render("you") + "  " + prompt)
				m.input.Reset()
				m.running = true
				return m, tea.Batch(m.startRun(prompt), m.sp.Tick, m.pump())
			}
		}
	case EventMsg:
		m.applyEvent(agent.Event(msg))
		cmds = append(cmds, m.pump())
	case doneMsg:
		m.running = false
		if msg.err != nil {
			m.append(errStyle.Render("error: " + msg.err.Error()))
		} else if msg.res != nil {
			m.cost = msg.res.Cost()
			m.turns = msg.res.Turns
		}
	case spinner.TickMsg:
		if m.running {
			var c tea.Cmd
			m.sp, c = m.sp.Update(msg)
			cmds = append(cmds, c)
		}
	}
	var c tea.Cmd
	m.input, c = m.input.Update(msg)
	cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	body := m.vp.View()
	if len(m.lines) == 0 {
		body = m.welcome()
	}
	return strings.Join([]string{
		m.header(),
		body,
		m.footer(),
	}, "\n")
}

// welcome is the empty-state splash: the big logo, centered, with a tagline.
func (m Model) welcome() string {
	bodyH := m.height - 1 - 4 // header + footer
	tagline := metaStyle.Render("cost-aware multi-model coding agent")
	block := lipgloss.JoinVertical(lipgloss.Center, renderLogo(m.logoVariant), "", tagline)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, block)
}

// --- rendering ------------------------------------------------------------

func (m Model) header() string {
	left := logoStyle.Render("◆ holocron")
	right := metaStyle.Render(m.model+"  ·  "+m.provider) + "   " +
		costStyle.Render(fmt.Sprintf("$%.4f", m.cost))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + right
	return headerBar.Width(m.width).Render(bar)
}

func (m Model) footer() string {
	status := ""
	if m.running {
		status = m.sp.View() + metaStyle.Render(" working")
	} else {
		status = metaStyle.Render(fmt.Sprintf("%d turns", m.turns))
	}
	line := promptMark.Render("› ") + m.input.View()
	box := inputBox.Width(m.width - 2).Render(line)
	return box + "\n" + metaStyle.Render("  enter send · esc quit   ") + status
}

func (m *Model) applyEvent(e agent.Event) {
	worker := strings.HasPrefix(e.Kind, "worker:")
	kind := strings.TrimPrefix(e.Kind, "worker:")
	indent := ""
	if worker {
		indent = workerTag.Render("  ⋮ ")
	}
	switch kind {
	case "text":
		width := m.width - 14
		lines := wrap(e.Data, width)
		m.append(indent + agentLabel.Render(m.agentName) + "  " + agentText.Render(lines[0]))
		for _, l := range lines[1:] {
			m.append(indent + "          " + agentText.Render(l))
		}
	case "tool":
		name, args, _ := strings.Cut(e.Data, " ")
		m.append(indent + toolArrow.Render("→ ") + toolName.Render(name) + " " + toolArgs.Render(truncate(args, 70)))
	case "tool-result":
		m.append(indent + resultStyle.Render("  "+truncate(oneLine(e.Data), 70)))
	case "checkpoint":
		m.append(indent + checkStyle.Render("✓ "+e.Data))
	case "error":
		m.append(indent + errStyle.Render("✗ "+truncate(e.Data, 80)))
	}
}

func (m *Model) append(line string) {
	m.lines = append(m.lines, line)
	m.vp.SetContent(strings.Join(m.lines, "\n"))
	m.vp.GotoBottom()
}

func (m *Model) layout() {
	headerH, footerH := 1, 4
	m.vp = viewport.New(m.width, m.height-headerH-footerH)
	m.vp.SetContent(strings.Join(m.lines, "\n"))
	m.vp.GotoBottom()
	m.input.Width = m.width - 6
}

// --- run plumbing ---------------------------------------------------------

// startRun spawns the agent run; events and the final doneMsg flow through msgs.
func (m Model) startRun(prompt string) tea.Cmd {
	return func() tea.Msg {
		go func() {
			res, err := m.run(context.Background(), prompt, func(e agent.Event) {
				m.msgs <- EventMsg(e)
			})
			m.msgs <- doneMsg{res: res, err: err}
		}()
		return nil
	}
}

// pump delivers the next queued msg (event or done) into the Bubble Tea loop.
func (m Model) pump() tea.Cmd {
	return func() tea.Msg { return <-m.msgs }
}

func oneLine(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ") }

func truncate(s string, n int) string {
	s = oneLine(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
