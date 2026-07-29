// Package tui is holocron's terminal client, built on Charm's Bubble Tea and Lip
// Gloss — the "Crush feel" without any Crush code. Dark black/gray base with a
// Carolina-blue accent. It drives an agent in-process and streams its events
// into a scrolling log.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
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
	gold     = lipgloss.Color("#E6B450") // shell-mode ("!") accent
	holoCyan = lipgloss.Color("#5CE1E6") // holographic gradient start
	holoViol = lipgloss.Color("#A78BFA") // holographic gradient end

	logoStyle    = lipgloss.NewStyle().Foreground(accent).Bold(true)
	metaStyle    = lipgloss.NewStyle().Foreground(muted)
	costStyle    = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
	headerBar    = lipgloss.NewStyle().Background(panel).Padding(0, 1)
	ruleStyle    = lipgloss.NewStyle().Foreground(dim)
	youLabel     = lipgloss.NewStyle().Foreground(muted).Bold(true)
	agentLabel   = lipgloss.NewStyle().Foreground(accent).Bold(true)
	agentText    = lipgloss.NewStyle().Foreground(fg)
	toolArrow    = lipgloss.NewStyle().Foreground(accent)
	toolName     = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
	toolArgs     = lipgloss.NewStyle().Foreground(muted)
	resultStyle  = lipgloss.NewStyle().Foreground(dim)
	workerTag    = lipgloss.NewStyle().Foreground(lipgloss.Color("#5C8AA6"))
	checkStyle   = lipgloss.NewStyle().Foreground(accentHi)
	errStyle     = lipgloss.NewStyle().Foreground(bad)
	startGlyph   = lipgloss.NewStyle().Foreground(accent)                    // something started
	doneGlyph    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6BBF73")) // something completed
	promptMark   = lipgloss.NewStyle().Foreground(accent).Bold(true)
	goldStyle    = lipgloss.NewStyle().Foreground(gold).Bold(true)
	crystalStyle = lipgloss.NewStyle().Foreground(holoCyan).Bold(true)
	violStyle    = lipgloss.NewStyle().Foreground(holoViol)
	inputBox     = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(holoCyan).
			Padding(0, 1)
)

// RunFunc executes one prompt under a permission mode, streaming events via emit.
type RunFunc func(ctx context.Context, prompt, mode string, emit func(agent.Event)) (*agent.Result, error)

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

// remoteStatusMsg reports progress of a /login or /rc slash command.
type remoteStatusMsg struct{ text string }

// bangMsg carries the output of a "!command" shell escape.
type bangMsg struct {
	out string
	err error
}

// ModelGroup is one provider's available models, for the /model picker.
type ModelGroup struct {
	Provider string
	Models   []string
}

// modelsMsg delivers fetched models into the loop to open the picker (or an
// error from provider setup).
type modelsMsg struct {
	groups []ModelGroup
	err    error
}

// slashCmd is a "/command" offered by autocomplete.
type slashCmd struct{ name, desc string }

// slashCommands is the set surfaced by autocomplete and handled on enter.
var slashCommands = []slashCmd{
	{"/model", "list and switch models"},
	{"/usage", "token and cost usage by model"},
	{"/clear", "start a fresh session"},
	{"/init", "generate an AGENTS.md for this repo"},
	{"/help", "show shortcuts"},
	{"/login", "sign in via SSO"},
	{"/rc", "link this machine to the server"},
	{"/remote-control", "link this machine to the server"},
}

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
	model   string // model active when this session was last saved (for /usage)
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
	usageBar    progress.Model
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

	run  RunFunc
	msgs chan tea.Msg

	// remote device linking (/login + /rc). Nil when the TUI was started
	// without a --server, in which case those commands report as unavailable.
	remoteServer string
	remoteLogin  func(ctx context.Context) error                   // SSO sign-in, stores token
	remoteLink   func(ctx context.Context, log func(string)) error // advertise this machine
	remoteLinked bool

	// /model picker. modelList lists the configured provider's models;
	// modelSetup configures a provider (kind, base URL, key) and returns its
	// models; onModel switches the model used for subsequent runs.
	modelList       func() []ModelGroup
	modelSetup      func(kind, baseURL, apiKey string) ([]ModelGroup, error)
	onModel         func(id string)
	modelGroups     []ModelGroup
	modelFlat       []string // selectable ids, in display order
	modelPickerOpen bool
	modelSel        int

	slashSel int    // highlighted row in slash-command autocomplete
	reset    func() // clears persistent conversation memory (/clear)
}

// RenderRun executes one real prompt synchronously, feeding its events into a
// TUI model, and returns the final rendered frame. Used to capture a live run
// (against a real model) as a single screenshot without keystroke injection.
func RenderRun(ctx context.Context, width, height int, agentName, model, provider string,
	run RunFunc,
	prompt string) string {
	m := New(agentName, model, provider, run)
	m.width, m.height = width, height
	m.layout()
	m.append(youLabel.Render("you") + "  " + prompt)
	res, err := run(ctx, prompt, "", func(e agent.Event) { m.applyEvent(e) })
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
func New(agentName, model, provider string, run RunFunc) Model {
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
	sp.Style = lipgloss.NewStyle().Foreground(holoCyan)

	bar := progress.New(progress.WithGradient("#2B6C8F", "#8FC7E8"), progress.WithoutPercentage())

	return Model{
		agentName:   agentName,
		model:       model,
		provider:    provider,
		input:       ti,
		sp:          sp,
		usageBar:    bar,
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
			title: m.title, model: m.model, lines: m.lines, turns: m.turns,
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

// SetRemote wires the /login and /rc slash commands. login signs the user in
// via SSO (storing a session token); link advertises this machine to the server
// as available for the logged-in user, streaming status through its log arg.
func (m *Model) SetRemote(server string, login func(ctx context.Context) error, link func(ctx context.Context, log func(string)) error) {
	m.remoteServer = server
	m.remoteLogin = login
	m.remoteLink = link
}

// SetReset wires /clear to drop persistent conversation memory.
func (m *Model) SetReset(fn func()) { m.reset = fn }

// SetModelPicker wires /model: list returns the configured provider's models
// (nil if none), setup configures a provider from (kind, baseURL, apiKey) and
// returns its models, onPick switches the model for subsequent runs.
func (m *Model) SetModelPicker(
	list func() []ModelGroup,
	setup func(kind, baseURL, apiKey string) ([]ModelGroup, error),
	onPick func(id string),
) {
	m.modelList = list
	m.modelSetup = setup
	m.onModel = onPick
}

// matchSlash returns the slash commands whose names prefix the current input,
// for autocomplete. Empty when the input isn't a bare "/command" fragment.
func (m Model) matchSlash() []slashCmd {
	v := strings.TrimSpace(m.input.Value())
	if len(v) == 0 || v[0] != '/' || strings.Contains(v, " ") {
		return nil
	}
	var out []slashCmd
	for _, c := range slashCommands {
		if strings.HasPrefix(c.name, v) {
			out = append(out, c)
		}
	}
	if len(out) == 1 && out[0].name == v { // already complete
		return nil
	}
	return out
}

// resolveSlash maps a possibly-partial "/cmd" to a full command name by exact or
// prefix match (first match wins on ties, matching the autocomplete Tab target).
// Returns "" when nothing matches.
func resolveSlash(name string) string {
	for _, c := range slashCommands {
		if c.name == name {
			return c.name
		}
	}
	for _, c := range slashCommands {
		if strings.HasPrefix(c.name, name) {
			return c.name
		}
	}
	return ""
}

// handleSlash runs a "/command" typed at the prompt. Any "/"-prefixed input is
// handled here (resolved by prefix, or reported as unknown) so it never leaks to
// the agent as a prompt.
func (m *Model) handleSlash(text string) (bool, tea.Cmd) {
	name := resolveSlash(strings.Fields(text)[0])
	if name == "" {
		m.append(errStyle.Render("unknown command: " + strings.Fields(text)[0]))
		m.input.Reset()
		return true, nil
	}
	switch name {
	case "/usage":
		m.input.Reset()
		m.renderUsage()
		return true, nil
	case "/help":
		m.input.Reset()
		m.helpOpen = true
		return true, nil
	case "/clear":
		m.input.Reset()
		m.lines = nil
		m.title, m.lastText = "", ""
		m.turns, m.cost, m.inTok, m.outTok = 0, 0, 0, 0
		m.workerOrder, m.workerAct, m.workerLog = nil, nil, nil
		m.shellOrder, m.shells = nil, nil
		m.vp.SetContent("")
		if m.reset != nil {
			m.reset() // drop conversation memory too
		}
		return true, nil
	case "/init":
		m.input.Reset()
		if m.running {
			return true, nil
		}
		prompt := "Inspect this repository and write a concise AGENTS.md at the root using the write tool: cover the build, test, and run commands, the project layout, and key conventions. Keep it short and skimmable."
		if m.title == "" {
			m.title = "/init"
		}
		m.append(startGlyph.Render("▸ ") + youLabel.Render("you") + "  /init")
		m.running = true
		m.runStart = time.Now()
		m.tick, m.phraseIdx = 0, 0
		m.workerOrder, m.workerAct, m.workerLog = nil, nil, nil
		m.workerSel, m.workerFocus = 0, ""
		m.shellOrder, m.shells = nil, nil
		m.bashCmd = ""
		return true, tea.Batch(m.startRun(prompt), m.sp.Tick, m.pump())
	case "/model":
		m.input.Reset()
		args := strings.Fields(text)[1:]
		// "/model <kind> [baseURL] [apiKey]" sets up a provider; bare "/model"
		// lists the configured provider's models.
		if len(args) > 0 {
			if m.modelSetup == nil {
				m.append(metaStyle.Render("provider setup not available"))
				return true, nil
			}
			kind := args[0]
			base, key := "", ""
			if len(args) > 1 {
				base = args[1]
			}
			if len(args) > 2 {
				key = args[2]
			}
			setup := m.modelSetup
			m.append(metaStyle.Render("configuring " + kind + " …"))
			return true, func() tea.Msg {
				g, err := setup(kind, base, key)
				return modelsMsg{groups: g, err: err}
			}
		}
		if m.modelList == nil {
			m.append(metaStyle.Render("model switching not available"))
			return true, nil
		}
		list := m.modelList
		return true, func() tea.Msg {
			g := list()
			if len(g) == 0 {
				return modelsMsg{err: fmt.Errorf("no provider configured — use: /model <ollama|openai|anthropic> [baseURL] [apiKey]")}
			}
			return modelsMsg{groups: g}
		}
	case "/login":
		m.input.Reset()
		if m.remoteLogin == nil {
			m.append(metaStyle.Render("remote not configured (start with --server)"))
			return true, nil
		}
		m.append(metaStyle.Render("opening browser to sign in…"))
		go func() {
			if err := m.remoteLogin(context.Background()); err != nil {
				m.msgs <- remoteStatusMsg{"login failed: " + err.Error()}
				return
			}
			m.msgs <- remoteStatusMsg{"signed in. run /rc to make this machine drivable from your phone"}
		}()
		return true, m.pump()
	case "/rc", "/remote-control":
		m.input.Reset()
		if m.remoteLink == nil {
			m.append(metaStyle.Render("remote not configured (start with --server)"))
			return true, nil
		}
		if m.remoteLinked {
			m.append(metaStyle.Render("already linked to " + m.remoteServer))
			return true, nil
		}
		m.remoteLinked = true
		m.append(metaStyle.Render("linking this machine to " + m.remoteServer + " …"))
		go func() {
			err := m.remoteLink(context.Background(), func(s string) { m.msgs <- remoteStatusMsg{s} })
			if err != nil {
				m.msgs <- remoteStatusMsg{"link ended: " + err.Error()}
			} else {
				m.msgs <- remoteStatusMsg{"link closed"}
			}
		}()
		return true, m.pump()
	}
	// resolveSlash returned a name we handle above; anything else is unknown.
	m.append(errStyle.Render("unknown command: " + name))
	m.input.Reset()
	return true, nil
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
		// Model picker overlay captures keys.
		if m.modelPickerOpen {
			switch msg.String() {
			case "esc", "q":
				m.modelPickerOpen = false
			case "up", "k":
				if m.modelSel > 0 {
					m.modelSel--
				}
			case "down", "j":
				if m.modelSel < len(m.modelFlat)-1 {
					m.modelSel++
				}
			case "enter":
				if m.modelSel < len(m.modelFlat) {
					id := m.modelFlat[m.modelSel]
					m.model = id
					if m.onModel != nil {
						m.onModel(id)
					}
					m.append(startGlyph.Render("▸ ") + metaStyle.Render("model set to ") + toolName.Render(id))
				}
				m.modelPickerOpen = false
			}
			return m, nil
		}
		// Slash-command autocomplete: navigate matches with up/down, complete
		// with tab, run the highlighted one with enter.
		if ms := m.matchSlash(); len(ms) > 0 {
			if m.slashSel >= len(ms) {
				m.slashSel = len(ms) - 1
			}
			switch msg.String() {
			case "up":
				if m.slashSel > 0 {
					m.slashSel--
				}
				return m, nil
			case "down":
				if m.slashSel < len(ms)-1 {
					m.slashSel++
				}
				return m, nil
			case "tab", "right":
				m.input.SetValue(ms[m.slashSel].name)
				m.input.CursorEnd()
				m.slashSel = 0
				return m, nil
			case "enter":
				cmd := ms[m.slashSel].name
				m.slashSel = 0
				_, c := m.handleSlash(cmd)
				return m, c
			}
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
				// "!cmd" runs a shell command directly instead of prompting the
				// agent, like Claude Code's bash escape.
				if bang := strings.TrimSpace(prompt); strings.HasPrefix(bang, "!") {
					cmdStr := strings.TrimSpace(bang[1:])
					m.input.Reset()
					if cmdStr == "" {
						return m, nil
					}
					m.append(promptMark.Render("! ") + youLabel.Render("bash") + "  " + cmdStr)
					return m, m.runBang(cmdStr)
				}
				if strings.HasPrefix(strings.TrimSpace(prompt), "/") {
					if handled, cmd := m.handleSlash(strings.TrimSpace(prompt)); handled {
						return m, cmd
					}
				}
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
	case remoteStatusMsg:
		if strings.HasPrefix(msg.text, "link ended") || msg.text == "link closed" {
			m.remoteLinked = false
		}
		m.append(metaStyle.Render(msg.text))
		cmds = append(cmds, m.pump())
	case bangMsg:
		for _, l := range strings.Split(strings.TrimRight(msg.out, "\n"), "\n") {
			m.append(resultStyle.Render(l))
		}
		if msg.err != nil {
			m.append(errStyle.Render(msg.err.Error()))
		}
	case modelsMsg:
		if msg.err != nil {
			m.append(errStyle.Render(msg.err.Error()))
			break
		}
		m.modelGroups = msg.groups
		m.modelFlat = m.modelFlat[:0]
		for _, g := range msg.groups {
			m.modelFlat = append(m.modelFlat, g.Models...)
		}
		if len(m.modelFlat) == 0 {
			m.append(metaStyle.Render("no models available"))
		} else {
			m.modelSel = 0
			for i, id := range m.modelFlat { // start on the current model
				if id == m.model {
					m.modelSel = i
				}
			}
			m.modelPickerOpen = true
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
	} else if m.modelPickerOpen {
		m.vp.SetContent(m.renderModels())
		m.vp.GotoTop()
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
		work := m.bashBlock() + m.sp.View() + " " + gradientText(m.phrase()+"…", holoRamp) +
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
	tagline := violStyle.Render("the archive remembers your code")
	block := lipgloss.JoinVertical(lipgloss.Center, renderLogo(m.logoVariant), "", tagline)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, block)
}

// --- rendering ------------------------------------------------------------

// crystalMeter draws a compact context-fill gauge from crystal glyphs
// (◈ filled, ◇ empty) for the header.
func crystalMeter(frac float64, cells int) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*float64(cells) + 0.5)
	var b strings.Builder
	for i := 0; i < cells; i++ {
		if i < filled {
			b.WriteString(crystalStyle.Render("◈"))
		} else {
			b.WriteString(resultStyle.Render("◇"))
		}
	}
	return b.String()
}

func (m Model) header() string {
	left := crystalStyle.Render("◈ ") + gradientText("HOLOCRON", holoRamp)
	if m.model != "" {
		left += metaStyle.Render("  " + m.model)
	}
	// Right: a crystal context meter (when we know the window) + cost.
	right := costStyle.Render(fmt.Sprintf("$%.4f", m.cost))
	if win, ok := contextWindow(m.model); ok && m.inTok > 0 {
		frac := float64(m.inTok) / float64(win)
		right = crystalMeter(frac, 6) + metaStyle.Render(fmt.Sprintf(" %d%%", int(frac*100+0.5))) +
			metaStyle.Render("  ·  ") + right
	}
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
	b.WriteString(checkStyle.Render(dotsFrame(m.tick)+" ") +
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

// dotsFrame is a pure-ASCII "dots" spinner (a dot tracks across three slots),
// advanced by the tick counter (~10 ticks/sec, so /2 gives a ~5fps cycle).
var dotsFrames = []string{".  ", ".. ", "...", " ..", "  .", "   "}

func dotsFrame(tick int) string {
	return dotsFrames[(tick/2)%len(dotsFrames)]
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

// modelUsage aggregates usage for one model across sessions.
type modelUsage struct {
	model    string
	peakIn   int64 // largest single-session context (input tokens) reached
	in, out  int64 // summed tokens
	cost     float64
	sessions int
}

// renderUsage appends a usage summary grouped by model for the provider in use.
// Each model with a known provider context max gets a context-fill bar
// (peak session context / context max); models without a known max show totals
// only.
func (m *Model) renderUsage() {
	prov := m.provider
	if prov == "" {
		prov = "—"
	}

	// Aggregate every touched session by its model.
	order := []string{}
	agg := map[string]*modelUsage{}
	for i := range m.sessions {
		model := m.sessions[i].model
		in, out, turns, cost := m.sessions[i].inTok, m.sessions[i].outTok, m.sessions[i].turns, m.sessions[i].cost
		if i == m.cur { // live values for the current session
			model = m.model
			in, out, turns, cost = m.inTok, m.outTok, m.turns, m.cost
		}
		if turns == 0 && in == 0 {
			continue
		}
		if model == "" {
			model = "—"
		}
		u := agg[model]
		if u == nil {
			u = &modelUsage{model: model}
			agg[model] = u
			order = append(order, model)
		}
		if in > u.peakIn {
			u.peakIn = in
		}
		u.in += in
		u.out += out
		u.cost += cost
		u.sessions++
	}

	m.append(agentLabel.Render("Usage") + metaStyle.Render("  "+prov))
	if len(order) == 0 {
		m.append(metaStyle.Render("  no usage yet"))
		return
	}

	w := m.width - 16
	if w > 44 {
		w = 44
	}
	if w < 10 {
		w = 10
	}
	m.usageBar.Width = w

	var total float64
	for _, name := range order {
		u := agg[name]
		total += u.cost
		m.append(youLabel.Render(name) +
			metaStyle.Render(fmt.Sprintf("  %d session(s)  ↑%s ↓%s  $%.4f",
				u.sessions, humanTokens(u.in), humanTokens(u.out), u.cost)))
		if win, ok := contextWindow(name); ok {
			pct := float64(u.peakIn) / float64(win)
			if pct < 0 {
				pct = 0
			}
			if pct > 1 {
				pct = 1
			}
			m.append("  " + m.usageBar.ViewAs(pct) +
				metaStyle.Render(fmt.Sprintf("  %d%%  %s / %s ctx",
					int(pct*100+0.5), humanTokens(u.peakIn), humanTokens(win))))
		} else {
			m.append(metaStyle.Render("  no context max for this provider"))
		}
	}
	if len(order) > 1 {
		m.append(metaStyle.Render(fmt.Sprintf("  total  $%.4f", total)))
	}
}

// renderModels draws the /model picker: available models grouped by provider,
// with the current one marked.
func (m Model) renderModels() string {
	var b strings.Builder
	b.WriteString(agentLabel.Render("Models") + "\n\n")
	row := 0
	for _, g := range m.modelGroups {
		b.WriteString(metaStyle.Render(strings.ToUpper(g.Provider)) + "\n")
		for _, id := range g.Models {
			cursor, style := "  ", agentText
			if row == m.modelSel {
				cursor, style = promptMark.Render("› "), checkStyle
			}
			line := cursor + style.Render(id)
			if id == m.model {
				line += metaStyle.Render("  (current)")
			}
			b.WriteString(line + "\n")
			row++
		}
		b.WriteString("\n")
	}
	b.WriteString(metaStyle.Render("↑/↓ select · enter switch · esc cancel"))
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
	// Shell-mode ("!"): gold border + prompt mark, so it's obvious the next
	// Enter runs a shell command instead of prompting the agent.
	bang := strings.HasPrefix(strings.TrimSpace(m.input.Value()), "!")
	mark, boxStyle := crystalStyle.Render("◈ "), inputBox
	if bang {
		mark, boxStyle = goldStyle.Render("! "), inputBox.BorderForeground(gold)
	}
	line := mark + m.input.View()
	box := boxStyle.Width(m.width - 2).Render(line)
	// Slash-command autocomplete: matching commands listed above the input; the
	// first is the Tab target.
	var suggest string
	if !m.running {
		if ms := m.matchSlash(); len(ms) > 0 {
			sel := m.slashSel
			if sel >= len(ms) {
				sel = len(ms) - 1
			}
			var rows []string
			for i, c := range ms {
				cur, style := "  ", metaStyle
				if i == sel {
					cur, style = promptMark.Render("› "), agentText
				}
				rows = append(rows, cur+style.Render(c.name)+metaStyle.Render("  "+c.desc))
			}
			suggest = strings.Join(rows, "\n") + "\n"
		}
	}
	box = suggest + box
	var mode string
	if bang {
		mode = goldStyle.Render("! shell mode") + metaStyle.Render(" · enter runs a shell command")
	} else {
		mode = startGlyph.Render("◆ ") + costStyle.Render(strings.ToLower(modeLabels[m.modeIdx]))
		if m.modeIdx != 0 { // non-Auto: gating is enforced
			mode += metaStyle.Render(" gated")
		}
		if n := m.runningShells(); n > 0 {
			mode += costStyle.Render(fmt.Sprintf(" · %d shell%s", n, plural(n)))
		}
		mode += metaStyle.Render(" · ⇥ mode · ← sessions · ⏎ send · / cmds · ? help")
	}
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
		m.append(indent + crystalStyle.Render("◆ ") + toolName.Render(name) + " " + toolArgs.Render(truncate(args, 70)))
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
			m.append(indent + violStyle.Render("◈ ") + resultStyle.Render("edited ") + toolName.Render(base) + " " +
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

// contextWindow is the approximate token context window for a model and whether
// one is known. Values are indicative, not exact; ok=false means no context max
// is known for the provider/model, so /usage shows no bar for it.
func contextWindow(model string) (int64, bool) {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "opus"), strings.Contains(m, "sonnet"), strings.Contains(m, "haiku"):
		return 200000, true
	case strings.Contains(m, "qwen"):
		return 32768, true
	case strings.Contains(m, "llama"):
		return 131072, true
	case strings.Contains(m, "deepseek"):
		return 16384, true
	case strings.Contains(m, "gemma"):
		return 8192, true
	case strings.Contains(m, "gpt"):
		return 128000, true
	default:
		return 0, false
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
		mode := modeLabels[m.modeIdx]
		go func() {
			res, err := m.run(context.Background(), prompt, mode, func(e agent.Event) {
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

// runBang runs a "!command" shell escape in the launch directory and returns its
// combined output.
func (m Model) runBang(command string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("bash", "-c", command).CombinedOutput()
		return bangMsg{out: string(out), err: err}
	}
}

func oneLine(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ") }

func truncate(s string, n int) string {
	s = oneLine(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
