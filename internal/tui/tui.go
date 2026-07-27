// Package tui is holocron's terminal client, built on Charm's Bubble Tea and Lip
// Gloss — the "Crush feel" without any Crush code. Dark black/gray base with a
// Carolina-blue accent. It drives an agent in-process and streams its events
// into a scrolling log.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	startGlyph  = lipgloss.NewStyle().Foreground(accent)              // something started
	doneGlyph   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6BBF73")) // something completed
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
	inTok     int64
	outTok    int64

	vp          viewport.Model
	input       textinput.Model
	sp          spinner.Model
	running     bool
	width       int
	height      int
	logoVariant int

	phrases   []string // whimsical status phrases shown while running
	phraseIdx int
	tick      int

	modeIdx int // permission mode (see modeLabels)

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
	// A spinning holocron: the crystal cycles hollow → faceted → filled →
	// faceted, as if catching light while it turns. These diamond glyphs render
	// widely (unlike braille/quadrant, which box out in many fonts).
	sp.Spinner = spinner.Spinner{
		Frames: []string{"◇", "◈", "◆", "◈"},
		FPS:    time.Second / 5,
	}
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
		phrases:     DefaultPhrases,
	}
}

// DefaultPhrases are the whimsical Star Wars status lines shown while the agent
// works, in the spirit of Claude Code's rotating spinner words. Configurable
// via the config file (see SetPhrases).
var DefaultPhrases = []string{
	"Channeling the Force",
	"Consulting the Jedi Council",
	"Decrypting the holocron",
	"Aligning the kyber crystal",
	"Plotting the jump to lightspeed",
	"Waking the astromech",
	"Rerouting power to the sensors",
	"Trusting the Force",
	"Scanning for life forms",
	"Warming up the ion engines",
	"Consulting the archives",
	"Spinning up the hyperdrive",
}

// modeLabels are the permission modes cycled with shift+tab (display state in
// the TUI; enforcement is a separate concern).
var modeLabels = []string{"Auto", "Manual", "Accept edits", "Plan"}

// SetPhrases overrides the status phrases (empty keeps the defaults).
func (m *Model) SetPhrases(p []string) {
	if len(p) > 0 {
		m.phrases = p
	}
}

func (m Model) phrase() string {
	if len(m.phrases) == 0 {
		return "working"
	}
	return m.phrases[m.phraseIdx%len(m.phrases)]
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
		case "shift+tab":
			m.modeIdx = (m.modeIdx + 1) % len(modeLabels)
			return m, nil
		case "enter":
			if !m.running && strings.TrimSpace(m.input.Value()) != "" {
				prompt := m.input.Value()
				m.append(startGlyph.Render("▸ ") + youLabel.Render("you") + "  " + prompt)
				m.input.Reset()
				m.running = true
				m.tick, m.phraseIdx = 0, 0
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
			m.append(doneGlyph.Render("✓ ") + metaStyle.Render(fmt.Sprintf("completed · $%.4f · %d turns", m.cost, m.turns)))
		}
	case spinner.TickMsg:
		if m.running {
			var c tea.Cmd
			m.sp, c = m.sp.Update(msg)
			cmds = append(cmds, c)
			// Advance the status phrase ~every 2.5s (ticks ~10/s), so it reads
			// like a rotating thought rather than a strobe.
			m.tick++
			if m.tick%25 == 0 {
				m.phraseIdx++
			}
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
	if len(m.lines) == 0 && !m.running {
		body = m.welcome()
	} else if m.running {
		// While working, show the spinner + rotating phrase as a live line at the
		// bottom of the conversation, the way Claude Code does.
		content := strings.Join(m.lines, "\n")
		work := m.sp.View() + agentText.Render(" "+m.phrase()+"…") +
			metaStyle.Render("  "+m.runStats())
		if content != "" {
			content += "\n"
		}
		m.vp.SetContent(content + work)
		m.vp.GotoBottom()
		body = m.vp.View()
	}
	return strings.Join([]string{
		m.header(),
		body,
		m.footer(),
	}, "\n")
}

// welcome is the empty-state splash: the big logo, centered, with a tagline.
func (m Model) welcome() string {
	bodyH := m.height - 1 - 5 // header + footer
	tagline := metaStyle.Render("cost-aware multi-model coding agent")
	block := lipgloss.JoinVertical(lipgloss.Center, renderLogo(m.logoVariant), "", tagline)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, block)
}

// --- rendering ------------------------------------------------------------

func (m Model) header() string {
	left := logoStyle.Render("◆ holocron")
	right := costStyle.Render(fmt.Sprintf("$%.4f", m.cost))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	bar := left + strings.Repeat(" ", gap) + right
	return headerBar.Width(m.width).Render(bar)
}

// runStats renders the live token usage + elapsed time in parentheses, shown
// inline with the working spinner/phrase.
func (m Model) runStats() string {
	parts := []string{fmt.Sprintf("%ds", m.tick/5)} // spinner runs at 5 fps
	if m.outTok > 0 {
		parts = append(parts, fmt.Sprintf("↓ %s tokens", humanTokens(m.outTok)))
	}
	return "(" + strings.Join(parts, " · ") + ")"
}

func (m Model) footer() string {
	// The working spinner/phrase now lives in the body; the footer just shows
	// turns when idle.
	status := ""
	if !m.running {
		status = metaStyle.Render(fmt.Sprintf("%d turns", m.turns))
	}
	line := promptMark.Render("› ") + m.input.View()
	box := inputBox.Width(m.width - 2).Render(line)
	mode := startGlyph.Render("◆ ") + costStyle.Render(modeLabels[m.modeIdx]) +
		metaStyle.Render("  shift+tab mode · ← sessions")
	help := metaStyle.Render("  enter send · esc quit   ") + status
	return box + "\n" + "  " + mode + "\n" + help
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
		m.append(indent + startGlyph.Render("▸ ") + toolName.Render(name) + " " + toolArgs.Render(truncate(args, 70)))
	case "tool-result":
		m.append(indent + doneGlyph.Render("  ✓ ") + resultStyle.Render(truncate(oneLine(e.Data), 68)))
	case "edit":
		var ed struct {
			File    string `json:"file"`
			Added   int    `json:"added"`
			Removed int    `json:"removed"`
		}
		if json.Unmarshal([]byte(e.Data), &ed) == nil {
			m.append(indent + doneGlyph.Render("+ ") + toolName.Render("edit ") +
				toolArgs.Render(ed.File) + " " +
				checkStyle.Render(fmt.Sprintf("+%d", ed.Added)) + " " +
				errStyle.Render(fmt.Sprintf("-%d", ed.Removed)))
		}
	case "usage":
		var u struct {
			In  int64 `json:"in"`
			Out int64 `json:"out"`
		}
		if json.Unmarshal([]byte(e.Data), &u) == nil {
			m.inTok, m.outTok = u.In, u.Out
		}
	case "review":
		width := m.width - 14
		for i, l := range wrap(e.Data, width) {
			label := "  "
			if i == 0 {
				label = checkStyle.Render("review ")
			}
			m.append(label + agentText.Render(l))
		}
	case "checkpoint":
		m.append(indent + checkStyle.Render("✓ "+e.Data))
	case "error":
		m.append(indent + errStyle.Render("✗ "+truncate(e.Data, 80)))
	}
}

// humanTokens renders a token count compactly (e.g. 1234 → "1.2k").
func humanTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func (m *Model) append(line string) {
	m.lines = append(m.lines, line)
	m.vp.SetContent(strings.Join(m.lines, "\n"))
	m.vp.GotoBottom()
}

func (m *Model) layout() {
	headerH, footerH := 1, 5
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
