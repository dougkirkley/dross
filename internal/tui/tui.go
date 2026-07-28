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
	startGlyph  = lipgloss.NewStyle().Foreground(accent)                    // something started
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

// askState holds a pending ask_user request: the questions, per-question option
// selections (a set, for multi-select), the highlighted cursor per question, the
// active tab (0..len = Submit), and the reply channel the agent goroutine blocks
// on. freeText is the single no-options case.
type askState struct {
	questions []agent.AskQuestion
	sel       [][]bool // sel[q][opt] chosen
	cursor    []int    // highlighted option per question
	tab       int      // 0..len-1 = question tabs, len = Submit
	warn      bool     // "you have not answered all questions"
	freeText  bool
	reply     chan string
}

func (a *askState) answered(q int) bool {
	for _, v := range a.sel[q] {
		if v {
			return true
		}
	}
	return false
}

// askMsg delivers an askState into the Bubble Tea loop.
type askMsg struct{ st *askState }

// Ask is the AskFunc wired into the agent: it posts the questions to the running
// TUI program and blocks until the user submits their answers.
func Ask(p *tea.Program, req agent.AskRequest) (string, error) {
	st := &askState{
		questions: req.Questions,
		sel:       make([][]bool, len(req.Questions)),
		cursor:    make([]int, len(req.Questions)),
		reply:     make(chan string, 1),
	}
	for i, q := range req.Questions {
		st.sel[i] = make([]bool, len(q.Options))
	}
	if len(req.Questions) == 1 && len(req.Questions[0].Options) == 0 {
		st.freeText = true
	}
	p.Send(askMsg{st: st})
	return <-st.reply, nil
}

// session is a saved conversation, so the TUI can hold several and switch
// between them (← opens the switcher). Snapshot of the swappable Model fields.
type session struct {
	title   string
	lines   []string
	turns   int
	cost    float64
	inTok   int64
	outTok  int64
	summary string    // last agent line, for the switcher
	updated time.Time // last activity, for the age column
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

	queue []string // messages typed while a run is in flight; drained on done

	// background shells started by the bash tool (background=true)
	shellOrder []string
	shells     map[string]*shellInfo

	// the foreground bash command currently executing (blank when none)
	bashCmd   string
	bashStart time.Time

	// live subagent tree, shown under the prompt while workers run
	workerOrder []string            // worker tags, in first-seen order
	workerAct   map[string]string   // worker tag -> present-tense activity
	workerLog   map[string][]string // worker tag -> rendered detail lines
	workerSel   int                 // selection cursor: 0 = main, 1..N = workers
	workerFocus string              // tag whose detail overlay is open ("" = none)

	modeIdx    int       // permission mode (see modeLabels)
	pendingAsk *askState // set while an ask_user request awaits answers

	title      string    // current session title
	lastText   string    // last plain agent text, for the switcher summary
	runStart   time.Time // when the current run began, for "Churned for X"
	sessions   []session // saved sessions (current one is snapshotted on switch)
	cur        int       // index of the current session
	pickerOpen bool      // session switcher overlay open
	pickIdx    int
	pickOrder  []int // session indices in switcher display order
	helpOpen   bool  // shortcuts overlay open

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
		sessions:    []session{{}},
	}
}

func (m *Model) saveCurrent() {
	if m.cur < len(m.sessions) {
		up := m.sessions[m.cur].updated
		if len(m.lines) > 0 {
			up = time.Now()
		}
		m.sessions[m.cur] = session{
			title: m.title, lines: m.lines, turns: m.turns,
			cost: m.cost, inTok: m.inTok, outTok: m.outTok,
			summary: m.lastText, updated: up,
		}
	}
}

// statusOf classifies a session for the switcher's grouping.
func (m Model) statusOf(i int) string {
	if i == m.cur && m.running {
		return "working"
	}
	if len(m.sessions[i].lines) > 0 {
		return "awaiting"
	}
	return "new"
}

// fmtDur renders a run duration like "1m 57s" or "12s".
func fmtDur(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm %ds", s/60, s%60)
}

func ageString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (m *Model) loadSession(i int) {
	s := m.sessions[i]
	m.cur = i
	m.title, m.lines, m.turns = s.title, s.lines, s.turns
	m.cost, m.inTok, m.outTok = s.cost, s.inTok, s.outTok
	m.vp.SetContent(strings.Join(m.lines, "\n"))
	m.vp.GotoBottom()
}

func (m *Model) newSession() {
	m.saveCurrent()
	m.sessions = append(m.sessions, session{})
	m.loadSession(len(m.sessions) - 1)
}

func (m *Model) removeSession(i int) {
	if len(m.sessions) <= 1 || i < 0 || i >= len(m.sessions) {
		return
	}
	m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
	target := m.cur
	if i < target {
		target--
	}
	if target >= len(m.sessions) {
		target = len(m.sessions) - 1
	}
	m.loadSession(target)
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
		// Shortcuts overlay: any key closes it.
		if m.helpOpen {
			m.helpOpen = false
			return m, nil
		}
		// "?" on an empty prompt opens the shortcuts overlay.
		if msg.String() == "?" && m.input.Value() == "" && !m.pickerOpen && m.pendingAsk == nil {
			m.helpOpen = true
			return m, nil
		}
		// Session switcher overlay captures keys.
		if m.pickerOpen {
			switch msg.String() {
			case "esc", "left", "h", "q":
				m.pickerOpen = false
			case "up", "k":
				if m.pickIdx > 0 {
					m.pickIdx--
				}
			case "down", "j":
				if m.pickIdx < len(m.pickOrder)-1 {
					m.pickIdx++
				}
			case "enter", "right", "l":
				m.loadSession(m.pickOrder[m.pickIdx])
				m.pickerOpen = false
			case "n":
				m.newSession()
				m.pickerOpen = false
			case "x", "d":
				m.removeSession(m.pickOrder[m.pickIdx])
				m.pickOrder = m.buildPickOrder()
				if m.pickIdx >= len(m.pickOrder) {
					m.pickIdx = len(m.pickOrder) - 1
				}
			}
			return m, nil
		}
		// ← opens the session switcher when idle and the input is empty.
		if msg.String() == "left" && !m.running && m.pendingAsk == nil && m.input.Value() == "" {
			m.saveCurrent()
			m.pickOrder = m.buildPickOrder()
			m.pickerOpen = true
			m.pickIdx = 0
			return m, nil
		}
		// A multi-question ask_user picker captures keys: ←/→ switch tabs, ↑/↓ or
		// digits pick an option, enter advances / submits.
		if m.pendingAsk != nil && !m.pendingAsk.freeText {
			a := m.pendingAsk
			nq := len(a.questions)
			pick := func(i int) {
				if a.tab >= nq || i >= len(a.sel[a.tab]) {
					return
				}
				if a.questions[a.tab].MultiSelect {
					a.sel[a.tab][i] = !a.sel[a.tab][i]
				} else {
					for j := range a.sel[a.tab] {
						a.sel[a.tab][j] = j == i
					}
				}
			}
			switch msg.String() {
			case "ctrl+c", "esc":
				return m, tea.Quit
			case "left", "h":
				if a.tab > 0 {
					a.tab--
				}
				return m, nil
			case "right", "l", "tab":
				if a.tab < nq {
					a.tab++
				}
				return m, nil
			case "up", "k":
				if a.tab < nq && a.cursor[a.tab] > 0 {
					a.cursor[a.tab]--
				}
				return m, nil
			case "down", "j":
				if a.tab < nq && a.cursor[a.tab] < len(a.questions[a.tab].Options)-1 {
					a.cursor[a.tab]++
				}
				return m, nil
			case " ":
				if a.tab < nq {
					pick(a.cursor[a.tab])
				}
				return m, nil
			case "enter":
				if a.tab < nq {
					if !a.questions[a.tab].MultiSelect && !a.answered(a.tab) {
						pick(a.cursor[a.tab]) // single-select: enter chooses the highlighted option
					}
					a.tab++ // advance to next tab (or Submit)
					return m, nil
				}
				// Submit tab: require every question answered.
				for i := range a.questions {
					if !a.answered(i) {
						a.warn = true
						a.tab = i
						return m, nil
					}
				}
				m.submitAsk()
				return m, nil
			default:
				if a.tab < nq && len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
					pick(int(msg.String()[0] - '1'))
				}
				return m, nil
			}
		}
		// While a run is in flight: enter queues the typed message; ↑ pulls the
		// most recent queued message back into the input to edit it.
		if m.running {
			if msg.String() == "enter" && strings.TrimSpace(m.input.Value()) != "" {
				m.queue = append(m.queue, m.input.Value())
				m.input.Reset()
				return m, nil
			}
			if msg.String() == "up" && m.input.Value() == "" && len(m.queue) > 0 {
				m.input.SetValue(m.queue[len(m.queue)-1])
				m.input.CursorEnd()
				m.queue = m.queue[:len(m.queue)-1]
				return m, nil
			}
		}
		// Subagent tree navigation: while workers run, ↑/↓ move the selection
		// (main + each worker) and enter opens/closes the selected worker's detail.
		if m.running && len(m.workerOrder) > 0 {
			switch msg.String() {
			case "esc":
				if m.workerFocus != "" {
					m.workerFocus = ""
					return m, nil
				}
			case "down", "j":
				if m.workerFocus == "" && m.workerSel < len(m.workerOrder) {
					m.workerSel++
				}
				return m, nil
			case "up", "k":
				if m.workerFocus == "" && m.workerSel > 0 {
					m.workerSel--
				}
				return m, nil
			case "enter":
				if m.workerFocus != "" {
					m.workerFocus = "" // enter closes the open detail
				} else if m.workerSel >= 1 && m.workerSel <= len(m.workerOrder) {
					m.workerFocus = m.workerOrder[m.workerSel-1]
				}
				return m, nil
			}
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "shift+tab":
			m.modeIdx = (m.modeIdx + 1) % len(modeLabels)
			return m, nil
		case "enter":
			if m.pendingAsk != nil && m.pendingAsk.freeText && strings.TrimSpace(m.input.Value()) != "" {
				ans := m.input.Value()
				m.append(youLabel.Render("you") + "  " + ans)
				m.pendingAsk.reply <- ans
				m.pendingAsk = nil
				m.input.Reset()
				m.input.Placeholder = "ask holocron to build, edit, or run something…"
				return m, nil
			}
			if !m.running && strings.TrimSpace(m.input.Value()) != "" {
				prompt := m.input.Value()
				if m.title == "" {
					m.title = truncate(prompt, 40)
				}
				m.append(startGlyph.Render("▸ ") + youLabel.Render("you") + "  " + prompt)
				m.input.Reset()
				m.running = true
				m.runStart = time.Now()
				m.tick, m.phraseIdx = 0, 0
				m.workerOrder, m.workerAct, m.workerLog = nil, nil, nil
				m.workerSel, m.workerFocus = 0, ""
				m.shellOrder, m.shells = nil, nil
				m.bashCmd = ""
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
			m.append(doneGlyph.Render("✻ ") + metaStyle.Render(fmt.Sprintf("Churned for %s · $%.4f · %d turns", fmtDur(time.Since(m.runStart)), m.cost, m.turns)))
			if r := strings.TrimSpace(msg.res.Text); r != "" {
				for i, l := range wrap("recap: "+r, m.width-6) {
					pad := ""
					if i > 0 {
						pad = "  "
					}
					m.append(pad + metaStyle.Render(l))
				}
			}
		}
		// Fire the next queued message, if any.
		if len(m.queue) > 0 {
			next := m.queue[0]
			m.queue = m.queue[1:]
			m.append(startGlyph.Render("▸ ") + youLabel.Render("you") + "  " + next)
			m.running = true
			m.runStart = time.Now()
			m.tick, m.phraseIdx = 0, 0
			m.workerOrder, m.workerAct, m.workerLog = nil, nil, nil
			m.workerSel, m.workerFocus = 0, ""
			m.shellOrder, m.shells = nil, nil
			m.bashCmd = ""
			cmds = append(cmds, m.startRun(next), m.sp.Tick, m.pump())
		}
	case askMsg:
		m.pendingAsk = msg.st
		if msg.st.freeText {
			m.input.Placeholder = "type your answer…"
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
	// Footer height is dynamic: the live subagent tree renders under the prompt
	// while workers run, so size the body viewport around the actual footer.
	footer := m.footer()
	bodyH := m.height - 1 - lipgloss.Height(footer) // header is 1 line
	if bodyH < 1 {
		bodyH = 1
	}
	m.vp.Height = bodyH
	body := m.vp.View()
	if m.workerFocus != "" {
		m.vp.SetContent(m.renderWorkerFocus())
		m.vp.GotoBottom()
		body = m.vp.View()
	} else if m.helpOpen {
		m.vp.SetContent(renderHelp())
		m.vp.GotoBottom()
		body = m.vp.View()
	} else if m.pickerOpen {
		m.vp.SetContent(m.renderSessions())
		m.vp.GotoBottom()
		body = m.vp.View()
	} else if len(m.lines) == 0 && !m.running && m.pendingAsk == nil {
		body = m.welcome()
	} else if m.pendingAsk != nil && !m.pendingAsk.freeText {
		content := strings.Join(m.lines, "\n")
		if content != "" {
			content += "\n"
		}
		m.vp.SetContent(content + m.renderPicker())
		m.vp.GotoBottom()
		body = m.vp.View()
	} else if m.running {
		// While working, show the spinner + rotating phrase as a live line at the
		// bottom of the conversation, the way Claude Code does.
		content := strings.Join(m.lines, "\n")
		work := m.bashBlock() + m.sp.View() + agentText.Render(" "+m.phrase()+"…") +
			metaStyle.Render("  "+m.runStats()) + m.queueStrip()
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
		footer,
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

// trackWorker updates the live subagent tree from a worker event. Worker data
// from parallel delegation is tagged "#N " so subagents stay distinguishable; a
// lone delegated worker has no tag and groups under "worker".
// parseWorkerData splits a worker event's payload into the subagent name and the
// underlying data, using the unit-separator the agent inserts. Untagged payloads
// (a lone delegated worker) group under "worker".
func parseWorkerData(data string) (name, rest string) {
	if i := strings.IndexByte(data, '\x1f'); i >= 0 {
		return data[:i], data[i+1:]
	}
	return "worker", data
}

func (m *Model) trackWorker(kind, tag, rest string) {
	if m.workerAct == nil {
		m.workerAct = map[string]string{}
	}
	if m.workerLog == nil {
		m.workerLog = map[string][]string{}
	}
	if _, seen := m.workerAct[tag]; !seen {
		m.workerOrder = append(m.workerOrder, tag)
	}
	logLine := ""
	switch kind {
	case "tool":
		name, args, _ := strings.Cut(rest, " ")
		m.workerAct[tag] = workerToolLabel(name, args)
		logLine = startGlyph.Render("▸ ") + toolName.Render(name) + " " + toolArgs.Render(truncate(args, 60))
	case "tool-result":
		logLine = doneGlyph.Render("  ✓ ") + resultStyle.Render(truncate(oneLine(rest), 60))
	case "edit":
		var ed struct {
			File string `json:"file"`
		}
		if json.Unmarshal([]byte(rest), &ed) == nil {
			base := ed.File
			if i := strings.LastIndexByte(base, '/'); i >= 0 {
				base = base[i+1:]
			}
			m.workerAct[tag] = "Editing " + base
			logLine = resultStyle.Render("Edited ") + toolName.Render(base)
		}
	case "text":
		m.workerAct[tag] = "Done" // conclusion returned
		logLine = agentText.Render(truncate(oneLine(rest), 66))
	case "error":
		m.workerAct[tag] = "Failed"
		logLine = errStyle.Render("✗ " + truncate(oneLine(rest), 64))
	}
	if logLine != "" {
		m.workerLog[tag] = append(m.workerLog[tag], logLine)
	}
}

// workerToolLabel renders a present-tense activity from a worker's tool call,
// mirroring Claude's "Reading <file>" / "Running a command" style.
func workerToolLabel(name, args string) string {
	switch name {
	case "read":
		return "Reading " + toolTargetPath(args)
	case "write":
		return "Writing " + toolTargetPath(args)
	case "edit":
		return "Editing " + toolTargetPath(args)
	case "bash":
		return "Running a command"
	case "delegate":
		return "Delegating"
	default:
		return name
	}
}

// toolTargetPath pulls a bare filename out of a tool's JSON args for display.
func toolTargetPath(args string) string {
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(args), &a) == nil && a.Path != "" {
		if i := strings.LastIndexByte(a.Path, '/'); i >= 0 {
			return a.Path[i+1:]
		}
		return a.Path
	}
	return "a file"
}

// workerTree renders the live subagent panel shown under the spinner: the lead
// as "● main" plus each active worker as "○ <tag> <activity>". Empty when no
// workers have run this turn.
// shellInfo tracks a background shell for the TUI viewer.
type shellInfo struct {
	command string
	running bool
	exit    string
	start   time.Time
}

// trackShell updates the background-shell registry from a shell:* event.
func (m *Model) trackShell(kind, data string) {
	id, rest := parseWorkerData(data) // reuse the unit-separator split
	if m.shells == nil {
		m.shells = map[string]*shellInfo{}
	}
	switch kind {
	case "shell:start":
		if _, ok := m.shells[id]; !ok {
			m.shellOrder = append(m.shellOrder, id)
		}
		m.shells[id] = &shellInfo{command: rest, running: true, start: time.Now()}
	case "shell:exit":
		if s := m.shells[id]; s != nil {
			s.running = false
			s.exit = rest
		}
	case "shell:output":
		// output is streamed; the TUI shows status inline, full output lives in
		// the mobile viewer. (No-op here beyond keeping the shell known.)
	}
}

// bashBlock renders the foreground bash command currently executing as a
// "Running N shell command(s) · Ns…" header with the command lines, shown above
// the spinner. Empty when no foreground command is running.
func (m Model) bashBlock() string {
	if m.bashCmd == "" {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(m.bashCmd, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	elapsed := int(time.Since(m.bashStart).Seconds())
	var b strings.Builder
	b.WriteString(checkStyle.Render("● ") +
		agentText.Render(fmt.Sprintf("Running %d shell command%s ", len(lines), plural(len(lines)))) +
		metaStyle.Render(fmt.Sprintf("· %ds…", elapsed)))
	width := m.width - 8
	if width < 20 {
		width = 20
	}
	b.WriteString("\n" + resultStyle.Render("  └ $ "+truncate(lines[0], width)))
	for _, l := range lines[1:] {
		b.WriteString("\n" + resultStyle.Render("      "+truncate(l, width)))
	}
	b.WriteString("\n")
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// runningShells counts shells still executing.
func (m Model) runningShells() int {
	n := 0
	for _, s := range m.shells {
		if s.running {
			n++
		}
	}
	return n
}

// queueStrip lists messages queued during the current run, newest last, with a
// hint that ↑ edits the most recent one.
func (m Model) queueStrip() string {
	if len(m.queue) == 0 {
		return ""
	}
	var b strings.Builder
	for _, q := range m.queue {
		b.WriteString("\n" + metaStyle.Render("  ⏱ "+truncate(oneLine(q), 64)))
	}
	b.WriteString("\n  " + metaStyle.Render("↑ edit queued · enter to queue"))
	return b.String()
}

func (m Model) workerTree() string {
	if len(m.workerOrder) == 0 {
		return ""
	}
	// cursor marks the selected row (0 = main, 1..N = workers).
	cursor := func(i int) string {
		if i == m.workerSel {
			return promptMark.Render("› ")
		}
		return "  "
	}
	var b strings.Builder
	b.WriteString("\n" + cursor(0) + checkStyle.Render("● ") + agentText.Render("main"))
	for i, tag := range m.workerOrder {
		act := m.workerAct[tag]
		b.WriteString("\n" + cursor(i+1) + workerTag.Render("○ "+tag) + "  " + resultStyle.Render(act))
	}
	b.WriteString("\n  " + metaStyle.Render("↑/↓ select · enter view subagent"))
	return b.String()
}

// renderWorkerFocus renders the detail log of the focused subagent as an overlay
// body, the way selecting a subagent in Claude Code shows its own transcript.
func (m Model) renderWorkerFocus() string {
	tag := m.workerFocus
	title := "subagent " + tag
	if tag == "worker" {
		title = "subagent"
	}
	var b strings.Builder
	b.WriteString(agentLabel.Render(title) + "  " + resultStyle.Render(m.workerAct[tag]) + "\n\n")
	log := m.workerLog[tag]
	if len(log) == 0 {
		b.WriteString(metaStyle.Render("  (no activity yet)"))
	}
	for _, l := range log {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString("\n" + metaStyle.Render("esc / enter — back to the run"))
	return b.String()
}

// chosen returns the selected option strings for question i.
func (a *askState) chosen(i int) []string {
	var out []string
	for j, v := range a.sel[i] {
		if v {
			out = append(out, a.questions[i].Options[j])
		}
	}
	return out
}

// submitAsk sends the picked answers back to the blocked agent goroutine.
func (m *Model) submitAsk() {
	a := m.pendingAsk
	if a == nil {
		return
	}
	var parts []string
	for i, q := range a.questions {
		label := q.Header
		if label == "" {
			label = q.Question
		}
		ans := strings.Join(a.chosen(i), ", ")
		parts = append(parts, label+": "+ans)
		m.append(youLabel.Render("you") + "  " + agentText.Render(label+" → "+ans))
	}
	a.reply <- strings.Join(parts, "\n")
	m.pendingAsk = nil
}

// buildPickOrder orders sessions for the switcher: working, then awaiting input,
// then new.
func (m Model) buildPickOrder() []int {
	var work, await, neu []int
	for i := range m.sessions {
		switch m.statusOf(i) {
		case "working":
			work = append(work, i)
		case "awaiting":
			await = append(await, i)
		default:
			neu = append(neu, i)
		}
	}
	return append(append(work, await...), neu...)
}

// renderHelp draws the shortcuts overlay (holocron's own keybindings).
func renderHelp() string {
	rows := [][2]string{
		{"enter", "send message"},
		{"shift+tab", "cycle mode (auto/manual/accept-edits/plan)"},
		{"←", "session switcher"},
		{"ctrl+c", "interrupt run · promote queued message"},
		{"esc", "quit"},
		{"↑ ↓ · 1-9", "move / pick in menus"},
		{"space", "toggle option (multi-select questions)"},
		{"?", "this help"},
	}
	var b strings.Builder
	b.WriteString(agentLabel.Render("Shortcuts") + "\n\n")
	for _, r := range rows {
		b.WriteString("  " + costStyle.Render(fmt.Sprintf("%-12s", r[0])) +
			metaStyle.Render(r[1]) + "\n")
	}
	b.WriteString("\n" + metaStyle.Render("  any key closes"))
	return b.String()
}

// renderSessions draws the session switcher overlay: sessions grouped by status
// with a last-message summary and age, like Claude Code's agents dashboard.
func (m Model) renderSessions() string {
	counts := map[string]int{}
	for i := range m.sessions {
		counts[m.statusOf(i)]++
	}
	var b strings.Builder
	b.WriteString(agentLabel.Render("holocron sessions") + "  " +
		metaStyle.Render(fmt.Sprintf("%d awaiting · %d working · %d new",
			counts["awaiting"], counts["working"], counts["new"])) + "\n")
	b.WriteString(metaStyle.Render("enter opens · n new · x remove · esc closes") + "\n")

	groups := []struct{ key, label string }{
		{"working", "Working"}, {"awaiting", "Needs input"}, {"new", "New"},
	}
	for _, g := range groups {
		first := true
		for pos, si := range m.pickOrder {
			if m.statusOf(si) != g.key {
				continue
			}
			if first {
				b.WriteString("\n" + metaStyle.Render(g.label) + "\n")
				first = false
			}
			s := m.sessions[si]
			title := s.title
			if title == "" {
				title = fmt.Sprintf("session %d", si+1)
			}
			glyph, tstyle := metaStyle.Render("· "), resultStyle
			if g.key == "working" {
				glyph = costStyle.Render("* ")
			} else if g.key == "awaiting" {
				glyph = doneGlyph.Render("* ")
			}
			if pos == m.pickIdx {
				glyph, tstyle = promptMark.Render("› "), agentText
			}
			row := glyph + tstyle.Render(fmt.Sprintf("%-32s", truncate(title, 32)))
			if s.summary != "" {
				row += metaStyle.Render("  " + truncate(s.summary, 40))
			}
			if age := ageString(s.updated); age != "" {
				row += metaStyle.Render("  " + age)
			}
			b.WriteString(row + "\n")
		}
	}
	return b.String()
}

// renderPicker draws the multi-question tabs + the current question's options.
func (m Model) renderPicker() string {
	a := m.pendingAsk
	nq := len(a.questions)
	var b strings.Builder

	var tabs []string
	for i, q := range a.questions {
		h := q.Header
		if h == "" {
			h = fmt.Sprintf("Q%d", i+1)
		}
		mark := "  "
		if a.answered(i) {
			mark = doneGlyph.Render("✓ ")
		}
		if i == a.tab {
			tabs = append(tabs, promptMark.Render("["+h+"]"))
		} else {
			tabs = append(tabs, mark+metaStyle.Render(h))
		}
	}
	if a.tab == nq {
		tabs = append(tabs, promptMark.Render("[Submit]"))
	} else {
		tabs = append(tabs, metaStyle.Render("→ Submit"))
	}
	b.WriteString(strings.Join(tabs, "  ") + "\n\n")

	if a.tab < nq {
		q := a.questions[a.tab]
		b.WriteString(agentText.Render(q.Question))
		if q.MultiSelect {
			b.WriteString(metaStyle.Render("  (choose any)"))
		}
		b.WriteString("\n")
		for i, opt := range q.Options {
			cur := "  "
			style := resultStyle
			if i == a.cursor[a.tab] {
				cur, style = promptMark.Render("› "), agentText
			}
			box := "( ) "
			if q.MultiSelect {
				box = "[ ] "
			}
			if a.sel[a.tab][i] {
				box = "(*) "
				if q.MultiSelect {
					box = "[x] "
				}
			}
			line := cur + style.Render(box+fmt.Sprintf("%d. %s", i+1, opt))
			if i == 0 {
				line += metaStyle.Render("  (recommended)")
			}
			b.WriteString(line + "\n")
		}
		hint := "←→ tabs · ↑↓ move · enter next"
		if q.MultiSelect {
			hint = "←→ tabs · ↑↓ move · space toggle · enter next"
		}
		b.WriteString("\n" + metaStyle.Render("  "+hint))
	} else {
		b.WriteString(agentText.Render("Review your answers") + "\n")
		if a.warn {
			b.WriteString(errStyle.Render("  ⚠ You have not answered all questions") + "\n")
		}
		for i, q := range a.questions {
			h := q.Header
			if h == "" {
				h = q.Question
			}
			ans := strings.Join(a.chosen(i), ", ")
			if ans == "" {
				ans = metaStyle.Render("(unanswered)")
			} else {
				ans = agentText.Render(ans)
			}
			b.WriteString(metaStyle.Render("  "+h+": ") + ans + "\n")
		}
		b.WriteString("\n" + promptMark.Render("› ") + agentText.Render("Submit answers") + metaStyle.Render("   (enter)"))
	}
	return b.String()
}

func (m Model) footer() string {
	// The working spinner/phrase lives in the body; the footer is just the input
	// and the mode line.
	line := promptMark.Render("› ") + m.input.View()
	box := inputBox.Width(m.width - 2).Render(line)
	mode := startGlyph.Render("▸ ") + costStyle.Render(strings.ToLower(modeLabels[m.modeIdx])+" mode")
	if n := m.runningShells(); n > 0 {
		mode += costStyle.Render(fmt.Sprintf(" · %d shell%s", n, plural(n)))
	}
	mode += metaStyle.Render(" · shift+tab cycle · ← sessions · enter send · ? for shortcuts")
	out := box + "\n" + "  " + mode
	// The live subagent tree sits under the prompt while workers run, the way
	// Claude Code shows its subagents.
	if m.running {
		if tree := m.workerTree(); tree != "" {
			out += tree
		}
	}
	return out
}

func (m *Model) applyEvent(e agent.Event) {
	if strings.HasPrefix(e.Kind, "shell:") {
		m.trackShell(e.Kind, e.Data)
		return
	}
	worker := strings.HasPrefix(e.Kind, "worker:")
	kind := strings.TrimPrefix(e.Kind, "worker:")
	indent := ""
	if worker {
		var name string
		name, e.Data = parseWorkerData(e.Data)
		indent = workerTag.Render("  ⋮ " + name + " ")
		m.trackWorker(kind, name, e.Data)
	}
	switch kind {
	case "text":
		if !worker {
			m.lastText = truncate(oneLine(e.Data), 70)
			m.bashCmd = "" // a message means the foreground command finished
		}
		width := m.width - 14
		lines := wrap(e.Data, width)
		m.append(indent + agentLabel.Render(m.agentName) + "  " + agentText.Render(lines[0]))
		for _, l := range lines[1:] {
			m.append(indent + "          " + agentText.Render(l))
		}
	case "tool":
		name, args, _ := strings.Cut(e.Data, " ")
		if !worker {
			// Surface a foreground bash command live (the "Running N shell
			// commands…" block); background bash is a shell, tracked separately.
			if name == "bash" {
				var a struct {
					Command    string `json:"command"`
					Background bool   `json:"background"`
				}
				if json.Unmarshal([]byte(args), &a) == nil && !a.Background {
					m.bashCmd, m.bashStart = a.Command, time.Now()
				}
			} else {
				m.bashCmd = ""
			}
		}
		m.append(indent + startGlyph.Render("▸ ") + toolName.Render(name) + " " + toolArgs.Render(truncate(args, 70)))
	case "tool-result":
		if !worker {
			m.bashCmd = ""
		}
		m.append(indent + doneGlyph.Render("  ✓ ") + resultStyle.Render(truncate(oneLine(e.Data), 68)))
	case "edit":
		var ed struct {
			File    string `json:"file"`
			Added   int    `json:"added"`
			Removed int    `json:"removed"`
		}
		if json.Unmarshal([]byte(e.Data), &ed) == nil {
			base := ed.File
			if i := strings.LastIndexByte(base, '/'); i >= 0 {
				base = base[i+1:]
			}
			m.append(indent + resultStyle.Render("Edited ") + toolName.Render(base) + " " +
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
	case "ask":
		width := m.width - 14
		lines := wrap(e.Data, width)
		m.append(agentLabel.Render(m.agentName) + " asks  " + agentText.Render(lines[0]))
		for _, l := range lines[1:] {
			m.append("          " + agentText.Render(l))
		}
	case "answer":
		// The answer is shown immediately when the user hits enter; skip here.
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
