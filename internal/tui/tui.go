// Package tui is dross's terminal client, built on Charm's Bubble Tea and Lip
// Gloss — the "Crush feel" without any Crush code. Dark black/gray base with a
// Carolina-blue accent. It drives an agent in-process and streams its events
// into a scrolling log.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dougkirkley/dross/pkg/agent"
)

// --- palette: dark black/gray + Carolina blue -----------------------------

// Palette colors. Overridable via config (see ApplyColors); rebuildStyles
// re-derives every style below from them.
var (
	accent   = lipgloss.Color("#A78BFA") // aetheric violet
	accentHi = lipgloss.Color("#22D3EE") // aurora cyan (highlights, cost)
	fg       = lipgloss.Color("#ECE9FB") // cool off-white
	prose    = lipgloss.Color("#F2F0FF") // assistant prose: bright cool white
	muted    = lipgloss.Color("#7D7BA3") // violet-gray
	dim      = lipgloss.Color("#55547A")
	bad      = lipgloss.Color("#FB7185")
	ok       = lipgloss.Color("#34D399") // something completed
	base     = lipgloss.Color("#07070F") // void
	panel    = lipgloss.Color("#0E0E1A") // violet-cast panel
	gold     = lipgloss.Color("#E6B450") // shell-mode ("!") accent
	worker   = lipgloss.Color("#8B5CF6") // subagent tag
	holoCyan = lipgloss.Color("#22D3EE") // holographic gradient start
	holoViol = lipgloss.Color("#8B5CF6") // holographic gradient end

	logoStyle    lipgloss.Style
	metaStyle    lipgloss.Style
	costStyle    lipgloss.Style
	headerBar    lipgloss.Style
	ruleStyle    lipgloss.Style
	youLabel     lipgloss.Style
	remoteLabel  lipgloss.Style
	youGutter    lipgloss.Style
	remoteGutter lipgloss.Style
	agentGutter  lipgloss.Style
	agentLabel   lipgloss.Style
	agentText    lipgloss.Style
	proseStyle   lipgloss.Style
	toolArrow    lipgloss.Style
	toolName     lipgloss.Style
	toolArgs     lipgloss.Style
	resultStyle  lipgloss.Style
	workerTag    lipgloss.Style
	checkStyle   lipgloss.Style
	errStyle     lipgloss.Style
	diffAddStyle lipgloss.Style
	diffDelStyle lipgloss.Style
	startGlyph   lipgloss.Style
	doneGlyph    lipgloss.Style
	promptMark   lipgloss.Style
	goldStyle    lipgloss.Style
	crystalStyle lipgloss.Style
	violStyle    lipgloss.Style
	drossBullet  lipgloss.Style // violet orb: assistant messages, edits, asks
	orbDone      lipgloss.Style // green orb: completed / review
	orbTool      lipgloss.Style // cyan orb: tool actions
	inputBox     lipgloss.Style
)

// drossOrbGlyph is the Dross orb rendered as a single glyph — the leading bullet
// on each assistant block (Claude-Code style, but the circle is Dross's eye).
const drossOrbGlyph = "◉"

func init() { rebuildStyles() }

// dimLevel is the fraction (0..1) that every foreground color is blended toward
// the background, dimming the whole UI. 0 = full color. Changed via /dim.
var dimLevel float64

// dimSteps are the selectable dim levels /dim cycles through.
var dimSteps = []float64{0, 0.2, 0.4, 0.6}

// cycleDim advances to the next dim level, rebuilds styles, and returns the new
// level as a 0..100 percentage.
func cycleDim() int {
	i := 0
	for j, v := range dimSteps {
		if v == dimLevel {
			i = j
			break
		}
	}
	dimLevel = dimSteps[(i+1)%len(dimSteps)]
	rebuildStyles()
	return int(dimLevel*100 + 0.5)
}

// hexRGB parses "#RRGGBB" into components; ok is false for anything else (e.g. a
// named/ANSI color), so callers leave those untouched.
func hexRGB(c lipgloss.Color) (r, g, b int, ok bool) {
	s := string(c)
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, false
	}
	var n int
	if _, err := fmt.Sscanf(s[1:], "%06x", &n); err != nil {
		return 0, 0, 0, false
	}
	return n >> 16 & 0xff, n >> 8 & 0xff, n & 0xff, true
}

// dimColor blends c toward the base background by dimLevel. Non-hex colors and a
// zero dim level pass through unchanged.
func dimColor(c lipgloss.Color) lipgloss.Color {
	if dimLevel <= 0 {
		return c
	}
	fr, fg2, fb, ok := hexRGB(c)
	br, bg2, bb, ok2 := hexRGB(base)
	if !ok || !ok2 {
		return c
	}
	mix := func(f, b int) int { return int(float64(f)*(1-dimLevel) + float64(b)*dimLevel + 0.5) }
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(fr, br), mix(fg2, bg2), mix(fb, bb)))
}

// rebuildStyles derives every style from the current palette. Called at startup
// and after any theme/color/dim change. Colors are read through dimColor so the
// active dim level applies without mutating the raw palette.
func rebuildStyles() {
	accent, accentHi := dimColor(accent), dimColor(accentHi)
	fg, prose, muted, dim := dimColor(fg), dimColor(prose), dimColor(muted), dimColor(dim)
	bad, ok, gold, worker := dimColor(bad), dimColor(ok), dimColor(gold), dimColor(worker)
	holoCyan, holoViol := dimColor(holoCyan), dimColor(holoViol)

	logoStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	metaStyle = lipgloss.NewStyle().Foreground(muted)
	costStyle = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
	// No background: gradient wordmark spans reset it mid-row, so a panel bg only
	// paints behind the model and reads as a stuck highlight. Keep the header flat.
	headerBar = lipgloss.NewStyle().Padding(0, 1)
	ruleStyle = lipgloss.NewStyle().Foreground(dim)
	youLabel = lipgloss.NewStyle().Foreground(muted).Bold(true)
	// Accent, not muted: a prompt someone else sent should catch the eye of
	// whoever is watching the terminal it landed in.
	remoteLabel = lipgloss.NewStyle().Foreground(accent).Bold(true)
	// Gutter rules for a person's turn. A one-column bar down the left says
	// whose turn it is without spending a word on it, and the colour says which
	// origin — accent for this terminal, gold for a prompt that arrived from a
	// phone, keeping the "someone else sent this" contrast the labels had.
	youGutter = lipgloss.NewStyle().Foreground(accent)
	remoteGutter = lipgloss.NewStyle().Foreground(gold).Bold(true)
	// The agent's own rule, a different hue again, so a glance down the left
	// edge says who is speaking without reading a word of it.
	agentGutter = lipgloss.NewStyle().Foreground(holoViol)
	agentLabel = lipgloss.NewStyle().Foreground(accent).Bold(true)
	agentText = lipgloss.NewStyle().Foreground(fg)
	proseStyle = lipgloss.NewStyle().Foreground(prose)
	toolArrow = lipgloss.NewStyle().Foreground(accent)
	toolName = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
	toolArgs = lipgloss.NewStyle().Foreground(muted)
	resultStyle = lipgloss.NewStyle().Foreground(dim)
	workerTag = lipgloss.NewStyle().Foreground(worker)
	checkStyle = lipgloss.NewStyle().Foreground(accentHi)
	errStyle = lipgloss.NewStyle().Foreground(bad)
	// git-diff colors: added lines green, removed lines red.
	diffAddStyle = lipgloss.NewStyle().Foreground(ok)
	diffDelStyle = lipgloss.NewStyle().Foreground(bad)
	startGlyph = lipgloss.NewStyle().Foreground(accent)
	doneGlyph = lipgloss.NewStyle().Foreground(ok)
	promptMark = lipgloss.NewStyle().Foreground(accent).Bold(true)
	goldStyle = lipgloss.NewStyle().Foreground(gold).Bold(true)
	crystalStyle = lipgloss.NewStyle().Foreground(holoCyan).Bold(true)
	violStyle = lipgloss.NewStyle().Foreground(holoViol)
	drossBullet = lipgloss.NewStyle().Foreground(holoViol).Bold(true)
	orbDone = lipgloss.NewStyle().Foreground(ok).Bold(true)
	orbTool = lipgloss.NewStyle().Foreground(holoCyan).Bold(true)
	inputBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(holoCyan).Padding(0, 1)
	mdCached = nil // markdown renderer bakes in the palette; rebuild on theme change
}

// RunFunc executes one prompt under a permission mode, streaming events via emit.
type RunFunc func(ctx context.Context, prompt, mode string, emit func(agent.Event)) (*agent.Result, error)

// EventMsg carries an agent event into the Bubble Tea loop.
type EventMsg agent.Event

// doneMsg signals a run finished.
type doneMsg struct {
	res *agent.Result
	err error
}

// copyClearedMsg fires ~2s after a copy to re-render (hiding the "copied" toast).
type copyClearedMsg struct{}

// loginDoneMsg reports the result of an OAuth login started from the /model picker.
type loginDoneMsg struct{ err error }

// loginCmd runs the wired OAuth login (browser flow) for the pending provider.
func (m Model) loginCmd() tea.Cmd {
	fn, prov := m.onLogin, m.loginProv
	return func() tea.Msg {
		if fn == nil {
			return loginDoneMsg{err: fmt.Errorf("login not available")}
		}
		return loginDoneMsg{err: fn(context.Background(), prov)}
	}
}

// noteCopied records a copy and returns a command that clears the toast later.
func (m *Model) noteCopied() tea.Cmd {
	m.copiedAt = time.Now()
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return copyClearedMsg{} })
}

// askState holds a pending ask_user request: the questions, per-question option
// selections (a set, for multi-select), the highlighted cursor per question, the
// active tab (0..len = Submit), and the reply channel the agent goroutine blocks
// on. freeText is the single no-options case.
// queued is a message waiting for the current run to finish, with the origin it
// arrived on.
//
// The origin has to travel with the text. A prompt sent from a phone while a
// run is in flight waits here, and starting it later as though it had been
// typed at this keyboard gates its permission prompts on this terminal — the
// phone that sent it sees a run that emits a tool call and then nothing, with
// no question to answer and a Stop button that has nothing to cancel, because
// the question is parked on a screen the sender may not be near.
type queued struct {
	text   string
	remote bool
}

type askState struct {
	questions []agent.AskQuestion
	sel       [][]bool // sel[q][opt] chosen
	cursor    []int    // highlighted option per question
	writeIn   []string // per-question write-in text
	editing   bool     // cursor is on the write-in row and accepting keystrokes
	tab       int      // 0..len-1 = question tabs, len = Submit
	warn      bool     // "you have not answered all questions"
	freeText  bool
	reply     chan string
}

func (a *askState) answered(q int) bool {
	if q < len(a.writeIn) && strings.TrimSpace(a.writeIn[q]) != "" {
		return true
	}
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

// RemoteHooks is what a live link needs from the UI. A struct rather than a
// row of positional callbacks, which had grown to five and gave the compiler
// no way to catch two of the same shape being swapped.
type RemoteHooks struct {
	// Log reports link progress ("linked", "disconnected").
	Log func(string)
	// Ready hands over a publisher once the link is up, for streaming work
	// started at this keyboard into the session the remote is watching.
	Ready func(publish func(kind, data string), ask func(payload string) (string, error))
	// Prompt and Interrupt are the remote driving this machine. Taking them
	// means the relay builds no agent of its own.
	Prompt    func(sessionID, text string)
	Interrupt func(sessionID string)
}

// remoteReadyMsg hands the TUI the publisher and asker for the now-live link.
type remoteReadyMsg struct {
	publish func(kind, data string)
	ask     func(payload string) (string, error)
}

// remotePromptMsg is a prompt sent from the phone or browser. It runs here,
// through the same agent as anything typed at the keyboard.
type remotePromptMsg struct{ sessionID, text string }

// remoteInterruptMsg is the remote pressing stop.
type remoteInterruptMsg struct{}

// shimmerMsg drives the animated holographic shimmer on the welcome screen.
type shimmerMsg struct{}

func shimmerTick() tea.Cmd {
	return tea.Tick(110*time.Millisecond, func(time.Time) tea.Msg { return shimmerMsg{} })
}

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
//
// key is the keystroke that does the same thing, where one exists. Most
// commands have none — they are the long tail you reach for by name — so the
// column is blank far more often than not, and that is the point: it marks the
// few worth learning rather than implying every command has a shortcut.
type slashCmd struct{ name, desc, key string }

// slashCommands is the set surfaced by autocomplete and handled on enter.
var slashCommands = []slashCmd{
	{"/model", "list and switch models", ""},
	{"/sessions", "switch between sessions", "←"},
	{"/mode", "cycle mode (auto/manual/accept-edits/plan)", "shift+tab"},
	{"/usage", "token and cost usage by model", ""},
	{"/mcp", "list, add, or remove MCP servers", ""},
	{"/clear", "start a fresh session", ""},
	{"/files", "list the files changed this session, with their diffstats", ""},
	{"/copy", "copy the whole transcript to the clipboard", ""},
	{"/exit", "quit dross", "ctrl+c ctrl+c"},
	{"/init", "generate an AGENTS.md for this repo", ""},
	{"/help", "show shortcuts", "?"},
	{"/theme", "switch color theme", ""},
	{"/dim", "cycle UI dim level (opacity)", ""},
	{"/welcome", "switch empty-state layout", ""},
	{"/mouse", "toggle mouse capture (drag-select+copy, wheel scroll)", ""},
	{"/config", "edit the config file in $EDITOR", ""},
	{"/skills", "list available skills", ""},
	{"/plugin", "manage plugins + marketplaces", ""},
	{"/login", "sign in via SSO", ""},
	{"/rc", "link this machine to the server", ""},
	{"/remote-control", "link this machine to the server", ""},
}

// SlashCommandNames is the set of "/command" names this client understands.
//
// Exported for the non-interactive path, which has none of them and has to say
// so: a slash command typed at `dross -p` is not a prompt, and sending it to a
// model as though it were spends a turn to be told that a machine already knew.
func SlashCommandNames() []string {
	out := make([]string, 0, len(slashCommands))
	for _, c := range slashCommands {
		out = append(out, c.name)
	}
	return out
}

// LooksLikeSlashCommand reports whether text is a "/command" rather than a path
// that happens to start with a slash. Exported for the same reason.
func LooksLikeSlashCommand(text string) bool { return looksLikeSlashCommand(text) }

// SkillItem is a discovered skill surfaced in the TUI (for /skills, /plugin, and
// user-invocable /name commands).
type SkillItem struct {
	Name          string
	Desc          string
	Body          string
	Source        string
	UserInvocable bool
	Foreign       bool // from a .claude/ dir — off until enabled
}

// Ask is the AskFunc wired into the agent: it posts the questions to the running
// TUI program and blocks until the user submits their answers.
func Ask(p *tea.Program, req agent.AskRequest) (string, error) {
	st := &askState{
		questions: req.Questions,
		sel:       make([][]bool, len(req.Questions)),
		cursor:    make([]int, len(req.Questions)),
		writeIn:   make([]string, len(req.Questions)),
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
	agentName      string
	model          string
	provider       string
	version        string                      // shown in the welcome header (SetMeta)
	workdir        string                      // shown in the welcome header (SetMeta)
	configPath     string                      // config file path for /config (SetConfigPath)
	mashWarn       string                      // prompt already flagged as a keyboard mash (enter again sends it)
	mouseOn        bool                        // mouse capture (click-to-expand); off lets the terminal select text
	checkpoint     func(context.Context) error // save a handoff brief on exit (SetCheckpoint)
	quitting       bool                        // a checkpoint-then-quit is in flight
	onTheme        func(string)                // persist the chosen theme (SetThemeSaver)
	onWelcome      func(string)                // persist the welcome layout (SetWelcomeSaver)
	onMode         func(string)                // apply a permission-mode change to the live agent (SetModeSaver)
	mcp            func([]string) []string     // /mcp list|add|remove handler (SetMCPCommand)
	skillItems     []SkillItem                 // discovered skills (for /skills + /name; SetSkillItems)
	pluginReload   func() []SkillItem
	skillsViewOpen bool
	skillSel       int
	skillOff       map[string]bool           // session-disabled skills (by name)
	skillSeen      map[string]bool           // skills a default enable/disable was applied to
	onSkillsApply  func(off map[string]bool) // rebuild the agent with the new off-set

	// Live streaming of assistant prose: streamStart is the m.lines index where
	// the growing block begins (-1 = not streaming); streamBuf holds the raw text.
	streamStart int
	streamBuf   string

	// /mcp management modal (mirrors the skills modal), with live status probing.
	mcpViewOpen    bool
	mcpDetail      bool
	mcpSel         int
	mcpRows        []mcpRow
	mcpProvider    func() []MCPServerInfo
	pluginViewOpen bool
	pluginTab      int // 0 installed, 1 marketplaces, 2 browse
	pluginSel      int

	themeViewOpen bool
	themeSel      int
	themeOrig     int // theme active when the picker opened (restored on cancel)
	welcomeStyle  int // empty-state layout: 0 clean, 1 skills, 2 columns, 3 boxed
	cost          float64
	turns         int
	inTok         int64
	outTok        int64
	ctxTok        int64 // latest turn's total input-side tokens (context window fill)

	vp          viewport.Model
	input       textinput.Model
	sp          spinner.Model
	usageBar    progress.Model
	running     bool
	width       int
	height      int
	logoVariant int

	phrases   []string // whimsical status phrases shown while running
	phraseIdx int      // phrase for the current run (chosen once per run)
	phraseSeq int      // advances each run so consecutive runs pick different phrases
	tick      int

	queue      []queued           // messages sent while a run is in flight; drained on done
	scrolledUp bool               // user scrolled up: stop auto-following the bottom
	runCancel  context.CancelFunc // cancels the active run (first ctrl+c interrupts)

	// background shells started by the bash tool (background=true)
	shellOrder []string
	shells     map[string]*shellInfo

	// the foreground bash command currently executing (blank when none)
	bashCmd   string
	bashStart time.Time

	initialPrompt string

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

	lines      []string  // rendered log lines
	lastReply  string    // raw text of the last assistant reply (for ctrl+y copy)
	lastCtrlC  time.Time // for double-ctrl+c-to-exit
	copiedAt   time.Time // when the last copy happened (drives the "copied" toast)
	history    []string  // submitted prompts, for up/down recall
	histIdx    int       // cursor into history (== len(history) means "current draft")
	suggestion string    // a single contextual next-step (from the reply's [next] line); ghost placeholder, tab to use

	// Collapsed tool-call group: consecutive tool calls render as one dim summary
	// line ("Ran 3 shell commands, read 1 file"), expandable with ctrl+o.
	groupLine     int // index in m.lines of the open group summary, or -1
	groupFirstRec int // first toolRecs index in the group (expand/click target)
	groupCounts   map[string]int
	groupOrder    []string

	// Mouse text selection (drag to select, copies on release).
	selecting bool  // a left-drag is in progress
	selMoved  bool  // the drag actually moved (vs a plain click)
	hasSel    bool  // a selection exists (highlighted)
	selA      selPt // anchor (where the drag started)
	selB      selPt // cursor (current end)

	run  RunFunc
	msgs chan tea.Msg

	// remote device linking (/login + /rc). Nil when the TUI was started
	// without a --server, in which case those commands report as unavailable.
	remoteServer string
	remoteLogin  func(ctx context.Context) error                    // SSO sign-in, stores token
	remoteLink   func(ctx context.Context, hooks RemoteHooks) error // advertise this machine
	remoteLinked bool
	// remotePublish streams locally-driven work up to the linked session, so
	// the phone sees what is typed here. Nil until /rc links.
	remotePublish func(kind, data string)
	// remoteAsk puts a question to the device driving the current run. Set with
	// remotePublish; used only while a remote-origin run is in flight.
	remoteAsk func(payload string) (string, error)
	// remoteOrigin marks the run in flight as having come from the relay, so a
	// gate is answered by whoever asked for the work rather than by whoever
	// happens to be sitting here.
	remoteOrigin bool
	// askRouter is how the agent's asker, which runs off the Bubble Tea loop,
	// learns where to send a gate.
	askRouter *AskRouter

	// /model picker. modelList lists the configured provider's models;
	// modelSetup configures a provider (kind, base URL, key) and returns its
	// models; onModel switches the model used for subsequent runs.
	modelList       func() []ModelGroup
	modelSetup      func(kind, baseURL, apiKey string) ([]ModelGroup, error)
	onModel         func(provider, model string) error
	onLogin         func(ctx context.Context, provider string) error // runs the OAuth login for a provider
	oauthProviders  map[string]bool                                  // providers whose auth is a login flow (vertex, …)
	loginProv       string                                           // pending login target (provider)
	loginModel      string                                           // pending login target (model to set after)
	modelGroups     []ModelGroup
	modelFlat       []string // selectable ids, in display order
	modelFlatProv   []string // provider id parallel to modelFlat
	modelPickerOpen bool
	modelSel        int          // index into modelFiltered
	modelQuery      string       // /model search filter
	modelSearching  bool         // /model picker is in search mode (started with "/")
	modelFiltered   []int        // indices into modelFlat matching the query
	catalogGroups   []ModelGroup // provider→models catalog for the /model picker
	modelSlot       int          // /model picker target: 0 = lead, 1 = worker
	onWorker        func(provider, model string)
	workerModel     string // current worker model (for the picker "current" marker)
	workerProv      string // current worker provider

	slashSel     int    // highlighted row in slash-command autocomplete
	reset        func() // clears persistent conversation memory (/clear)
	backgrounder func() // ctrl+b: move the running foreground bash to background
	shimmer      int    // animation phase for the holographic welcome shimmer

	// full tool calls this session, for the /tools expandable viewer
	toolRecs []toolRec
	// Files this session has changed, in first-touch order, with running
	// diffstats — summarised in the bottom bar and listed by /files.
	changed    []changedFile
	toolLineOf map[int]int // feed line index → toolRecs index (for click-to-expand)
	// Inline expansion: how many detail lines sit under each expanded summary
	// line, and which lines those are, so a splice can be undone and the
	// positions kept correct as the feed grows.
	expandedLen map[int]int
	expandedAt  []int
}

// toolRec is a full tool call (untruncated command + output) for /tools.
type toolRec struct {
	name, args, result string
	isErr              bool
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
	m.appendTurn(youGutter, "", prompt)
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
	ti.Placeholder = "ask " + agentName + " to build, edit, or run something…"
	ti.Prompt = ""
	ti.Focus()

	sp := spinner.New()
	// Dross as a one-eyed spirit orb: the eye pulses, darts side to side, then
	// blinks. These circle glyphs render widely (unlike braille/quadrant, which
	// box out in many fonts).
	sp.Spinner = spinner.Spinner{
		Frames: []string{"◉", "⊙", "◉", "◑", "◉", "◐", "◉", "⊝"},
		FPS:    time.Second / 5,
	}
	sp.Style = lipgloss.NewStyle().Foreground(holoViol)

	bar := progress.New(progress.WithGradient("#2B6C8F", "#8FC7E8"), progress.WithoutPercentage())

	return Model{
		agentName:    agentName,
		model:        model,
		provider:     provider,
		input:        ti,
		sp:           sp,
		usageBar:     bar,
		run:          run,
		msgs:         make(chan tea.Msg, 256),
		logoVariant:  1, // gradient fade (default)
		welcomeStyle: 0, // clean: Dross mascot + wordmark, dir, model (default)
		phrases:      DefaultPhrases,
		streamStart:  -1,   // not streaming until a text-delta arrives
		modeIdx:      2,    // default to Accept edits (auto edits, ask before commands)
		mouseOn:      true, // drag-to-select auto-copies; wheel scrolls
		groupLine:    -1,   // no open tool-call group
		sessions:     []session{{}},
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

// DefaultPhrases are the nerdy status lines shown while the agent works, in the
// spirit of Claude Code's rotating spinner words. Configurable via the config
// file (see SetPhrases).
// DefaultPhrases are in the voice of Dross — the dramatic, brilliant spirit
// companion the project is named for.
var DefaultPhrases = []string{
	"Running the simulations",
	"Consulting my vast intellect",
	"Being brilliant, obviously",
	"Calculating the odds",
	"Devising a cunning plan",
	"Simulating ten thousand outcomes",
	"Predicting your next move",
	"Cross-referencing the archives",
	"Composing a suitably epic reply",
	"Rehearsing the dramatic reveal",
	"Weighing every possibility",
	"Applying spirit-speed cognition",
	"Narrating for effect",
	"Sharpening the pattern",
	"Preparing to impress you",
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
func (m *Model) SetRemote(server string, login func(ctx context.Context) error, link func(ctx context.Context, hooks RemoteHooks) error) {
	m.remoteServer = server
	m.remoteLogin = login
	m.remoteLink = link
}

// SetAskRouter gives the model the router the agent's asker consults, so it can
// keep it in step with which device started the run in flight.
func (m *Model) SetAskRouter(r *AskRouter) { m.askRouter = r }

// routeAsks points the router at the current run's origin.
func (m *Model) routeAsks(remote bool) {
	if m.askRouter != nil {
		m.askRouter.SetRemote(remote, m.remoteAsk)
	}
}

// SetReset wires /clear to drop persistent conversation memory.
func (m *Model) SetReset(fn func()) { m.reset = fn }

// SetMeta sets the version and working directory shown in the welcome header.
func (m *Model) SetMeta(version, workdir string) { m.version, m.workdir = version, workdir }

// SetConfigPath sets the config file /config opens in $EDITOR.
func (m *Model) SetConfigPath(path string) { m.configPath = path }

func (m *Model) SetInitialPrompt(prompt string) {
	m.initialPrompt = prompt
	if prompt != "" {
		m.input.SetValue(prompt)
		m.input.CursorEnd()
	}
}

// SetThemeSaver wires a callback that persists the theme chosen via /theme.
func (m *Model) SetThemeSaver(fn func(string)) { m.onTheme = fn }

// SetModeSaver wires a callback that applies a permission-mode change to the
// live agent immediately (so shift+tab takes effect on the current run).
func (m *Model) SetModeSaver(fn func(string)) { m.onMode = fn }

// SetMCPCommand wires the /mcp slash command (list/add/remove MCP servers). The
// handler receives the args after "/mcp" and returns lines to print.
func (m *Model) SetMCPCommand(fn func(args []string) []string) { m.mcp = fn }

// SetWelcomeSaver wires a callback that persists the layout chosen via /welcome.
func (m *Model) SetWelcomeSaver(fn func(string)) { m.onWelcome = fn }

// SetCheckpoint wires a save-on-exit callback that persists a handoff brief so
// the session can be resumed later.
func (m *Model) SetCheckpoint(fn func(context.Context) error) { m.checkpoint = fn }

// quitMsg is delivered after an exit-time checkpoint completes.
type quitMsg struct{}

// quitCmd saves a handoff brief (if there's a conversation to save and no run is
// active) before quitting; otherwise it quits immediately.
func (m *Model) quitCmd() tea.Cmd {
	if m.checkpoint == nil || m.quitting || m.running || (m.turns == 0 && len(m.lines) == 0) {
		return tea.Quit
	}
	m.quitting = true
	m.append(metaStyle.Render("saving session…"))
	fn := m.checkpoint
	return func() tea.Msg {
		_ = fn(context.Background())
		return quitMsg{}
	}
}

// SetSkillItems sets the discovered skills (for /skills and /name commands).
// Foreign (.claude/) skills default to OFF the first time they're seen, so they
// must be explicitly enabled in the /skills modal before use.
func (m *Model) SetSkillItems(items []SkillItem) {
	m.skillItems = items
	if m.skillOff == nil {
		m.skillOff = map[string]bool{}
	}
	if m.skillSeen == nil {
		m.skillSeen = map[string]bool{}
	}
	for _, s := range items {
		if s.Foreign && !m.skillSeen[s.Name] {
			m.skillOff[s.Name] = true
		}
		m.skillSeen[s.Name] = true
	}
}

// SetPluginReload wires a callback that re-scans skills/plugins after a /plugin
// change and returns the refreshed list.
func (m *Model) SetPluginReload(fn func() []SkillItem) { m.pluginReload = fn }

// SetSkillsApply wires a callback invoked when the /skills modal toggles skills
// on/off, so the agent is rebuilt with the disabled set excluded.
func (m *Model) SetSkillsApply(fn func(off map[string]bool)) { m.onSkillsApply = fn }

// applyReload refreshes the skill list after a /plugin change.
func (m *Model) applyReload() {
	if m.pluginReload == nil {
		return
	}
	m.skillItems = m.pluginReload()
}

// isBuiltinSlash reports whether name is a reserved built-in command. Built-ins
// always win over skills so a plugin can't hijack /help, /clear, etc.
func isBuiltinSlash(name string) bool {
	for _, c := range slashCommands {
		if c.name == name {
			return true
		}
	}
	return false
}

// userSlash returns the built-in commands plus one entry per user-invocable
// skill (excluding any that collide with a built-in name), for autocomplete and
// resolution.
func (m Model) userSlash() []slashCmd {
	out := append([]slashCmd(nil), slashCommands...)
	// Count short names so a plugin skill "plugin:skill" can also be invoked as
	// "/skill" when that short name is unambiguous.
	shortCount := map[string]int{}
	for _, s := range m.skillItems {
		if s.UserInvocable {
			shortCount[shortSkillName(s.Name)]++
		}
	}
	for _, s := range m.skillItems {
		if !s.UserInvocable || isBuiltinSlash("/"+s.Name) {
			continue
		}
		out = append(out, slashCmd{name: "/" + s.Name, desc: s.Desc})
		// Add a short alias (plugin:skill -> skill) when unambiguous and not a
		// built-in or a name a skill already uses verbatim.
		short := shortSkillName(s.Name)
		if short != s.Name && shortCount[short] == 1 && !isBuiltinSlash("/"+short) {
			out = append(out, slashCmd{name: "/" + short, desc: s.Desc})
		}
	}
	return out
}

// findSkill returns the skill for a "/name" command, if user-invocable and not
// shadowed by a built-in.
func (m Model) findSkill(slash string) (SkillItem, bool) {
	if isBuiltinSlash(slash) {
		return SkillItem{}, false
	}
	name := strings.TrimPrefix(slash, "/")
	// Exact match on the (possibly fully-qualified) name. A disabled skill (e.g.
	// a foreign .claude skill not yet enabled) is not invocable.
	for _, s := range m.skillItems {
		if s.UserInvocable && s.Name == name && !m.skillOff[s.Name] {
			return s, true
		}
	}
	// Short name: a plugin skill registered as "plugin:skill" can also be invoked
	// as just "skill", as long as that short name is unambiguous.
	var match SkillItem
	count := 0
	for _, s := range m.skillItems {
		if s.UserInvocable && !m.skillOff[s.Name] && shortSkillName(s.Name) == name {
			match, count = s, count+1
		}
	}
	if count == 1 {
		return match, true
	}
	return SkillItem{}, false
}

// shortSkillName is the segment after the last ':' (e.g. "plugin:skill" -> "skill").
func shortSkillName(name string) string {
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// SetWelcomeStyle picks the empty-state layout variant (see welcome()).
func (m *Model) SetWelcomeStyle(n int) { m.welcomeStyle = n }

// SetBackgrounder wires ctrl+b to move the currently-running foreground bash
// command into the background (see agent.Agent.BackgroundForeground).
func (m *Model) SetBackgrounder(fn func()) { m.backgrounder = fn }

// SetProviders gives /model the catalog as provider→models groups. The picker
// shows each provider as a subtitle with its models underneath.
func (m *Model) SetProviders(groups []ModelGroup) { m.catalogGroups = groups }

// SetModelPicker wires /model: list returns the configured provider's models
// (nil if none), setup configures a provider from (kind, baseURL, apiKey) and
// returns its models, onPick switches the model for subsequent runs.
func (m *Model) SetModelPicker(
	list func() []ModelGroup,
	setup func(kind, baseURL, apiKey string) ([]ModelGroup, error),
	onPick func(provider, model string) error,
) {
	m.modelList = list
	m.modelSetup = setup
	m.onModel = onPick
}

// SetLogin wires the OAuth login flow: when a model from one of the named
// providers is picked and its provider can't be configured (no credentials),
// dross runs fn (a browser login) and then applies the model.
func (m *Model) SetLogin(fn func(ctx context.Context, provider string) error, providers ...string) {
	m.onLogin = fn
	m.oauthProviders = map[string]bool{}
	for _, p := range providers {
		m.oauthProviders[p] = true
	}
}

// SetWorker wires the "Worker" slot of the /model picker: onPick assigns the
// cheaper worker model the lead delegates bounded subtasks to, and cur* seed the
// "current" marker. Optional — without it the picker only offers the lead slot.
func (m *Model) SetWorker(onPick func(provider, model string), curProvider, curModel string) {
	m.onWorker = onPick
	m.workerProv, m.workerModel = curProvider, curModel
}

// openModelPicker loads groups into the model picker (provider subtitles +
// models) and builds the flattened selection with a parallel provider list.
func (m *Model) openModelPicker(groups []ModelGroup) {
	m.modelGroups = groups
	m.modelFlat = m.modelFlat[:0]
	m.modelFlatProv = m.modelFlatProv[:0]
	for _, g := range groups {
		for _, id := range g.Models {
			m.modelFlat = append(m.modelFlat, id)
			m.modelFlatProv = append(m.modelFlatProv, g.Provider)
		}
	}
	if len(m.modelFlat) == 0 {
		return
	}
	m.modelQuery, m.modelSearching, m.modelSlot = "", false, 0
	m.filterModels()
	m.selectCurrentModel()
	m.modelPickerOpen = true
}

// selectCurrentModel moves the picker cursor to the model currently assigned to
// the active slot (lead or worker).
func (m *Model) selectCurrentModel() {
	curModel, curProv := m.model, m.provider
	if m.modelSlot == 1 {
		curModel, curProv = m.workerModel, m.workerProv
	}
	for i, idx := range m.modelFiltered {
		if m.modelFlat[idx] == curModel && m.modelFlatProv[idx] == curProv {
			m.modelSel = i
			return
		}
	}
}

// filterModels rebuilds modelFiltered from the current query (case-insensitive
// substring on model id or provider) and clamps the selection.
func (m *Model) filterModels() {
	q := strings.ToLower(strings.TrimSpace(m.modelQuery))
	m.modelFiltered = m.modelFiltered[:0]
	for i := range m.modelFlat {
		if q == "" ||
			strings.Contains(strings.ToLower(m.modelFlat[i]), q) ||
			strings.Contains(strings.ToLower(m.modelFlatProv[i]), q) {
			m.modelFiltered = append(m.modelFiltered, i)
		}
	}
	if m.modelSel >= len(m.modelFiltered) {
		m.modelSel = len(m.modelFiltered) - 1
	}
	if m.modelSel < 0 {
		m.modelSel = 0
	}
}

// matchSlash returns autocomplete suggestions for the current input: command
// names for a bare "/frag", or argument values once a "/cmd " has a space (e.g.
// "/theme em" → "/theme ember"). Empty when nothing applies.
func (m Model) matchSlash() []slashCmd {
	raw := m.input.Value()
	v := strings.TrimSpace(raw)
	if v == "" || v[0] != '/' {
		return nil
	}
	// Argument completion: input has a space after the command.
	if strings.Contains(raw, " ") {
		fields := strings.Fields(v)
		opts := m.argOptions(fields[0])
		if len(opts) == 0 {
			return nil
		}
		partial := ""
		if !strings.HasSuffix(raw, " ") && len(fields) > 1 {
			partial = fields[len(fields)-1]
		}
		var out []slashCmd
		for _, o := range opts {
			if strings.HasPrefix(o, partial) {
				out = append(out, slashCmd{name: fields[0] + " " + o})
			}
		}
		if len(out) == 1 && out[0].name == v { // already complete
			return nil
		}
		return out
	}
	// Command-name completion.
	var out []slashCmd
	for _, c := range m.userSlash() {
		if strings.HasPrefix(c.name, v) {
			out = append(out, c)
		}
	}
	if len(out) == 1 && out[0].name == v { // already complete
		return nil
	}
	return out
}

// argOptions returns the completable argument values for a slash command, or nil
// if it takes no completable arguments.
func (m Model) argOptions(cmd string) []string {
	switch cmd {
	case "/theme":
		return ThemeNames()
	case "/welcome":
		return welcomeNames
	case "/plugin":
		return []string{"marketplace", "install", "uninstall", "enable", "disable", "list"}
	}
	return nil
}

// resolveSlash maps a possibly-partial "/cmd" to a full command name by exact or
// prefix match (first match wins on ties, matching the autocomplete Tab target).
// Returns "" when nothing matches.
func (m Model) resolveSlash(name string) string {
	cmds := m.userSlash()
	for _, c := range cmds {
		if c.name == name {
			return c.name
		}
	}
	for _, c := range cmds {
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
	name := m.resolveSlash(strings.Fields(text)[0])
	if name == "" {
		m.append(errStyle.Render("unknown command: " + strings.Fields(text)[0]))
		m.input.Reset()
		return true, nil
	}
	// User-invocable skill: run its body as a prompt ($ARGUMENTS substituted).
	if s, ok := m.findSkill(name); ok {
		m.input.Reset()
		if m.running {
			m.append(metaStyle.Render("busy — wait for the current run to finish"))
			return true, nil
		}
		argStr := strings.TrimSpace(strings.TrimPrefix(text, strings.Fields(text)[0]))
		prompt := strings.ReplaceAll(s.Body, "$ARGUMENTS", argStr)
		if m.title == "" {
			m.title = name
		}
		m.turnGap()
		m.appendTurn(youGutter, "", name)
		m.running = true
		m.runStart = time.Now()
		m.tick, m.phraseIdx, m.phraseSeq = 0, m.phraseSeq, m.phraseSeq+1
		m.workerOrder, m.workerAct, m.workerLog = nil, nil, nil
		m.workerSel, m.workerFocus = 0, ""
		m.shellOrder, m.shells = nil, nil
		m.bashCmd = ""
		return true, tea.Batch(m.beginRun(prompt), m.sp.Tick, m.pump())
	}
	switch name {
	case "/mcp":
		m.input.Reset()
		args := strings.Fields(text)[1:]
		// Bare "/mcp" opens the management modal; subcommands stay text.
		if len(args) == 0 && m.mcpProvider != nil {
			return true, m.openMCPView()
		}
		if m.mcp == nil {
			m.append(metaStyle.Render("mcp is not available here"))
			return true, nil
		}
		for _, l := range m.mcp(args) {
			m.append(l)
		}
		return true, nil
	case "/theme":
		m.input.Reset()
		args := strings.Fields(text)[1:]
		if len(args) > 0 { // "/theme <name>" sets directly
			if !SetTheme(args[0]) {
				m.append(errStyle.Render("unknown theme: " + args[0] + " · try: " + strings.Join(ThemeNames(), " ")))
				return true, nil
			}
			if m.onTheme != nil {
				m.onTheme(args[0])
			}
			m.append(startGlyph.Render("▸ ") + metaStyle.Render("theme ") + crystalStyle.Render(args[0]))
			return true, nil
		}
		// Bare "/theme" opens the picker (live-previews as you move).
		m.themeViewOpen = true
		m.themeOrig = currentTheme
		m.themeSel = currentTheme
		return true, nil
	case "/dim":
		m.input.Reset()
		pct := cycleDim()
		label := fmt.Sprintf("%d%%", pct)
		if pct == 0 {
			label = "off"
		}
		m.append(startGlyph.Render("▸ ") + metaStyle.Render("dim ") + crystalStyle.Render(label))
		return true, nil
	case "/welcome":
		m.input.Reset()
		args := strings.Fields(text)[1:]
		var name string
		if len(args) > 0 {
			idx := welcomeIndex(args[0])
			if idx < 0 {
				m.append(errStyle.Render("unknown layout: " + args[0] + " · try: " + strings.Join(welcomeNames, " ")))
				return true, nil
			}
			m.welcomeStyle, name = idx, args[0]
		} else {
			m.welcomeStyle = (m.welcomeStyle + 1) % len(welcomeNames)
			name = welcomeNames[m.welcomeStyle]
		}
		if m.onWelcome != nil {
			m.onWelcome(name)
		}
		// On a fresh session the empty-state welcome re-renders with the new layout
		// immediately — appending a confirmation line would hide it. Only note the
		// change once a conversation has started (where the welcome isn't shown).
		if len(m.lines) > 0 {
			m.append(startGlyph.Render("▸ ") + metaStyle.Render("welcome ") + crystalStyle.Render(name) +
				metaStyle.Render("  · "+strings.Join(welcomeNames, " ")))
		}
		return true, nil
	case "/mouse":
		m.input.Reset()
		m.mouseOn = !m.mouseOn
		if m.mouseOn {
			m.append(metaStyle.Render("mouse on — click tool lines to expand (terminal text selection is disabled)"))
			return true, tea.EnableMouseCellMotion
		}
		m.append(metaStyle.Render("mouse off — select and copy text with your terminal; scroll natively"))
		return true, tea.DisableMouse
	case "/config":
		m.input.Reset()
		return true, m.editConfig()
	case "/skills":
		m.input.Reset()
		if len(m.skillItems) == 0 {
			m.showSkills()
			return true, nil
		}
		if m.skillOff == nil {
			m.skillOff = map[string]bool{}
		}
		m.skillsViewOpen, m.skillSel = true, 0
		return true, nil
	case "/plugin":
		return true, m.handlePlugin(text)
	case "/usage":
		m.input.Reset()
		m.renderUsage()
		return true, nil
	case "/help":
		m.input.Reset()
		m.helpOpen = true
		return true, nil
	case "/sessions":
		m.input.Reset()
		if m.running || m.pendingAsk != nil {
			m.append(metaStyle.Render("busy — wait for the current run to finish"))
			return true, nil
		}
		m.saveCurrent()
		m.pickOrder = m.buildPickOrder()
		m.pickerOpen = true
		m.pickIdx = 0
		return true, nil
	case "/mode":
		m.input.Reset()
		m.modeIdx = (m.modeIdx + 1) % len(modeLabels)
		if m.onMode != nil {
			m.onMode(modeLabels[m.modeIdx])
		}
		m.append(startGlyph.Render("▸ ") + metaStyle.Render("mode ") +
			crystalStyle.Render(strings.ToLower(modeLabels[m.modeIdx])))
		return true, nil
	case "/files":
		m.input.Reset()
		if len(m.changed) == 0 {
			m.append(metaStyle.Render("nothing changed yet this session"))
			return true, nil
		}
		m.append(startGlyph.Render("▸ ") + metaStyle.Render("files changed this session"))
		for _, c := range m.changed {
			verb := "edited "
			if c.created {
				verb = "created"
			}
			line := "  " + resultStyle.Render(verb) + " " + toolName.Render(c.path) + " " +
				diffAddStyle.Render(fmt.Sprintf("+%d", c.added))
			if c.removed > 0 {
				line += " " + diffDelStyle.Render(fmt.Sprintf("-%d", c.removed))
			}
			m.append(line)
		}
		return true, nil
	case "/copy":
		m.input.Reset()
		if len(m.lines) == 0 {
			m.append(metaStyle.Render("nothing to copy yet"))
			return true, nil
		}
		copyToClipboard(stripANSI(strings.Join(m.lines, "\n")))
		return true, m.noteCopied()
	case "/exit", "/quit":
		m.input.Reset()
		return true, m.quitCmd()
	case "/clear":
		m.input.Reset()
		if m.running {
			m.append(metaStyle.Render("busy — wait for the current run to finish"))
			return true, nil
		}
		m.lines = nil
		m.title, m.lastText = "", ""
		m.turns, m.cost, m.inTok, m.outTok = 0, 0, 0, 0
		m.changed = nil
		m.workerOrder, m.workerAct, m.workerLog = nil, nil, nil
		m.shellOrder, m.shells = nil, nil
		m.toolRecs, m.toolLineOf = nil, nil
		m.closeToolGroup()
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
		m.turnGap()
		m.appendTurn(youGutter, "", "/init")
		m.running = true
		m.runStart = time.Now()
		m.tick, m.phraseIdx, m.phraseSeq = 0, m.phraseSeq, m.phraseSeq+1
		m.workerOrder, m.workerAct, m.workerLog = nil, nil, nil
		m.workerSel, m.workerFocus = 0, ""
		m.shellOrder, m.shells = nil, nil
		m.bashCmd = ""
		return true, tea.Batch(m.beginRun(prompt), m.sp.Tick, m.pump())
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
		// No args: open the grouped picker — every catalog provider as a subtitle
		// with its models underneath, plus the current provider's live models on
		// top when available.
		groups := m.catalogGroups
		if m.modelList != nil {
			if live := m.modelList(); len(live) > 0 {
				// The live list already shows the configured provider; drop its
				// catalog duplicate so it isn't listed twice.
				shown := map[string]bool{}
				for _, g := range live {
					shown[g.Provider] = true
				}
				groups = append([]ModelGroup(nil), live...)
				for _, g := range m.catalogGroups {
					if !shown[g.Provider] {
						groups = append(groups, g)
					}
				}
			}
		}
		if len(groups) == 0 {
			m.append(metaStyle.Render("no providers — set one up in config, or /model <kind> <baseURL> [key]"))
			return true, nil
		}
		m.openModelPicker(groups)
		return true, nil
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
			err := m.remoteLink(context.Background(), RemoteHooks{
				Log: func(s string) { m.msgs <- remoteStatusMsg{s} },
				Ready: func(pub func(kind, data string), ask func(string) (string, error)) {
					m.msgs <- remoteReadyMsg{pub, ask}
				},
				// The relay hands prompts here rather than running them itself,
				// so there is one agent on this machine and it is the one the
				// user configured.
				Prompt:    func(sessionID, text string) { m.msgs <- remotePromptMsg{sessionID, text} },
				Interrupt: func(string) { m.msgs <- remoteInterruptMsg{} },
			})
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

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, shimmerTick()}
	if m.initialPrompt != "" {
		cmds = append(cmds, func() tea.Msg {
			return tea.KeyMsg{Type: tea.KeyEnter}
		})
	}
	return tea.Batch(cmds...)
}

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
		// Skills modal captures keys: space toggles a skill on/off (plugin skills
		// are locked); esc/enter closes and applies the change.
		if m.skillsViewOpen {
			switch msg.String() {
			case "esc", "q", "enter":
				m.skillsViewOpen = false
				if m.onSkillsApply != nil {
					m.onSkillsApply(m.skillOff)
				}
			case "up", "k":
				if m.skillSel > 0 {
					m.skillSel--
				}
			case "down", "j":
				if m.skillSel < len(m.skillItems)-1 {
					m.skillSel++
				}
			case "g", "home":
				m.skillSel = 0
			case "G", "end":
				m.skillSel = len(m.skillItems) - 1
			case " ":
				if m.skillSel < len(m.skillItems) {
					s := m.skillItems[m.skillSel]
					if s.Source == "project" || s.Source == "user" { // plugin skills are locked
						m.skillOff[s.Name] = !m.skillOff[s.Name]
					}
				}
			}
			return m, nil
		}
		// MCP management modal captures keys.
		if m.handleMCPKey(msg.String()) {
			return m, nil
		}
		// Theme picker captures keys; moving the selection live-previews the theme.
		if m.themeViewOpen {
			switch msg.String() {
			case "esc", "q":
				applyTheme(m.themeOrig) // cancel: restore
				m.themeViewOpen = false
			case "enter":
				applyTheme(m.themeSel)
				if m.onTheme != nil {
					m.onTheme(themes[m.themeSel].Name)
				}
				m.themeViewOpen = false
			case "up", "k":
				if m.themeSel > 0 {
					m.themeSel--
					applyTheme(m.themeSel)
				}
			case "down", "j":
				if m.themeSel < len(themes)-1 {
					m.themeSel++
					applyTheme(m.themeSel)
				}
			case "g", "home":
				m.themeSel = 0
				applyTheme(m.themeSel)
			case "G", "end":
				m.themeSel = len(themes) - 1
				applyTheme(m.themeSel)
			}
			return m, nil
		}
		// Plugin browser modal captures keys.
		if m.pluginViewOpen {
			return m.updatePluginView(msg)
		}
		// Model picker overlay. Navigate by default; "/" starts a search.
		if m.modelPickerOpen {
			pick := func() tea.Cmd {
				if m.modelSel >= len(m.modelFiltered) {
					m.modelPickerOpen = false
					return nil
				}
				idx := m.modelFiltered[m.modelSel]
				id, prov := m.modelFlat[idx], m.modelFlatProv[idx]
				m.modelPickerOpen = false
				if m.modelSlot == 1 { // worker slot
					m.workerModel, m.workerProv = id, prov
					if m.onWorker != nil {
						m.onWorker(prov, id)
					}
					m.append(startGlyph.Render("▸ ") + metaStyle.Render("worker set to ") + toolName.Render(id) + metaStyle.Render("  "+prov))
					return nil
				}
				// Lead slot: configure the provider first; only commit the model if
				// it succeeds. For OAuth-only providers (e.g. vertex) with no
				// credentials, start the login flow instead of failing.
				if m.onModel != nil {
					if err := m.onModel(prov, id); err != nil {
						if m.oauthProviders[prov] && m.onLogin != nil {
							m.loginProv, m.loginModel = prov, id
							m.append(startGlyph.Render("▸ ") + metaStyle.Render("signing in to "+prov+" — opening your browser…"))
							return m.loginCmd()
						}
						m.append(errStyle.Render("✗ " + err.Error()))
						return nil
					}
				}
				m.model, m.provider = id, prov
				m.append(startGlyph.Render("▸ ") + metaStyle.Render("model set to ") + toolName.Render(id) + metaStyle.Render("  "+prov))
				return nil
			}
			toggleSlot := func() {
				if m.onWorker == nil {
					return // worker slot not wired
				}
				m.modelSlot = 1 - m.modelSlot
				m.selectCurrentModel()
			}
			if m.modelSearching {
				switch msg.Type {
				case tea.KeyEsc: // leave search, keep the picker open
					m.modelSearching, m.modelQuery = false, ""
					m.filterModels()
				case tea.KeyEnter:
					return m, pick()
				case tea.KeyUp:
					if m.modelSel > 0 {
						m.modelSel--
					}
				case tea.KeyDown:
					if m.modelSel < len(m.modelFiltered)-1 {
						m.modelSel++
					}
				case tea.KeyBackspace:
					if m.modelQuery != "" {
						m.modelQuery = m.modelQuery[:len(m.modelQuery)-1]
						m.filterModels()
					}
				case tea.KeySpace:
					m.modelQuery += " "
					m.filterModels()
				case tea.KeyRunes:
					m.modelQuery += string(msg.Runes)
					m.filterModels()
				}
				return m, nil
			}
			switch msg.String() {
			case "/":
				m.modelSearching = true
			case "esc", "q":
				m.modelPickerOpen = false
			case "tab", "left", "right", "h", "l":
				toggleSlot()
			case "up", "k":
				if m.modelSel > 0 {
					m.modelSel--
				}
			case "down", "j":
				if m.modelSel < len(m.modelFiltered)-1 {
					m.modelSel++
				}
			case "enter":
				return m, pick()
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
		// A contextual next-step suggestion (from the reply's [next] line) shows as
		// ghost placeholder in an empty input; tab drops it in to edit/send.
		if strings.TrimSpace(m.input.Value()) == "" && m.suggestion != "" {
			switch msg.String() {
			case "tab", "right":
				m.input.SetValue(m.suggestion)
				m.input.CursorEnd()
				m.suggestion = ""
				return m, nil
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
		// digits pick an option, enter advances / submits. The last cursor
		// position per question is the write-in row.
		if m.pendingAsk != nil && !m.pendingAsk.freeText {
			a := m.pendingAsk
			nq := len(a.questions)
			// writeInRow is the cursor position for the write-in field.
			writeInRow := func() int {
				if a.tab >= nq {
					return 0
				}
				return len(a.questions[a.tab].Options)
			}
			onWriteIn := func() bool {
				return a.tab < nq && a.cursor[a.tab] == writeInRow()
			}
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
			// While editing the write-in field, route printable keys there.
			if a.editing {
				switch msg.String() {
				case "ctrl+c":
					return m, tea.Quit
				case "esc":
					a.editing = false
					return m, nil
				case "enter":
					a.editing = false
					// If write-in has text, treat it as answered and advance.
					if strings.TrimSpace(a.writeIn[a.tab]) != "" {
						if nq == 1 && !a.questions[0].MultiSelect {
							m.submitAsk()
							return m, nil
						}
						a.tab++
					}
					return m, nil
				case "backspace":
					if w := a.writeIn[a.tab]; len(w) > 0 {
						a.writeIn[a.tab] = w[:len(w)-1]
					}
					return m, nil
				case "left", "right", "up", "down", "tab":
					// Let navigation keys exit editing and fall through.
					a.editing = false
				default:
					// Printable character — append to write-in.
					if r := msg.String(); len(r) == 1 && r[0] >= ' ' {
						a.writeIn[a.tab] += r
						return m, nil
					}
					return m, nil
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
				if a.tab < nq && a.cursor[a.tab] < writeInRow() {
					a.cursor[a.tab]++
				}
				return m, nil
			case " ":
				if a.tab < nq {
					if onWriteIn() {
						a.editing = true
					} else {
						pick(a.cursor[a.tab])
					}
				}
				return m, nil
			case "enter":
				if a.tab < nq {
					if onWriteIn() {
						// Start editing the write-in field.
						a.editing = true
						return m, nil
					}
					if !a.questions[a.tab].MultiSelect && !a.answered(a.tab) {
						pick(a.cursor[a.tab]) // single-select: enter chooses the highlighted option
					}
					// A lone single-select question (e.g. a permission prompt)
					// submits immediately — no Submit-tab round-trip.
					if nq == 1 && !a.questions[0].MultiSelect {
						m.submitAsk()
						return m, nil
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
					// Number key on a lone single-select question submits at once.
					if nq == 1 && !a.questions[0].MultiSelect {
						m.submitAsk()
					}
				}
				return m, nil
			}
		}
		// While a run is in flight: enter queues the typed message; ↑ pulls the
		// most recent queued message back into the input to edit it.
		if m.running {
			if msg.String() == "enter" && strings.TrimSpace(m.input.Value()) != "" {
				// Slash commands run immediately even mid-run; other text queues.
				if tp := strings.TrimSpace(m.input.Value()); looksLikeSlashCommand(tp) {
					if handled, cmd := m.handleSlash(tp); handled {
						return m, cmd
					}
				}
				m.history = append(m.history, m.input.Value()) // recall with up/down
				m.histIdx = len(m.history)
				m.queue = append(m.queue, queued{text: m.input.Value()})
				m.input.Reset()
				return m, nil
			}
			if msg.String() == "up" && m.input.Value() == "" && len(m.queue) > 0 {
				m.input.SetValue(m.queue[len(m.queue)-1].text)
				m.input.CursorEnd()
				m.queue = m.queue[:len(m.queue)-1]
				return m, nil
			}
		}
		// Subagent tree navigation: while workers run, ↑/↓ move the selection
		// (main + each worker) and enter opens/closes the selected worker's detail.
		// Only capture nav keys for the subagent tree when the input is empty —
		// otherwise typing (and slash-command nav) while running gets stolen.
		if m.running && len(m.workerOrder) > 0 && strings.TrimSpace(m.input.Value()) == "" {
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
		case "ctrl+c":
			// While running, ctrl+c interrupts the prompt. When idle, exit requires
			// a confirming second ctrl+c within a second (no accidental quit).
			if m.running && m.runCancel != nil {
				m.runCancel()
				m.append(metaStyle.Render("interrupting…"))
				return m, nil
			}
			if time.Since(m.lastCtrlC) < time.Second {
				return m, m.quitCmd()
			}
			m.lastCtrlC = time.Now()
			// Hint shows under the prompt (footer), not in the transcript; the tick
			// re-renders to clear it after the window closes.
			return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return copyClearedMsg{} })
		case "esc":
			// Esc no longer quits (exit is double-ctrl+c or /exit); it clears the
			// input if there's anything typed.
			if strings.TrimSpace(m.input.Value()) != "" {
				m.input.Reset()
			}
			return m, nil
		case "ctrl+y":
			// Copy the last assistant reply to the system clipboard (no mouse
			// selection needed).
			if strings.TrimSpace(m.lastReply) != "" {
				copyToClipboard(m.lastReply)
				return m, m.noteCopied()
			}
			return m, nil
		case "ctrl+o":
			// Expand/collapse the most recent tool output, in place.
			if line := m.lastToolLine(); line >= 0 {
				m.toggleInlineTool(line)
			}
			return m, nil
		case "pgup":
			m.vp.ViewUp()
			m.scrolledUp = !m.vp.AtBottom()
			return m, nil
		case "pgdown":
			m.vp.ViewDown()
			m.scrolledUp = !m.vp.AtBottom()
			return m, nil
		case "ctrl+u":
			m.vp.HalfViewUp()
			m.scrolledUp = !m.vp.AtBottom()
			return m, nil
		case "ctrl+d":
			m.vp.HalfViewDown()
			m.scrolledUp = !m.vp.AtBottom()
			return m, nil
		case "ctrl+b":
			// Move the running foreground bash command to the background so the
			// agent loop continues; a no-op when no foreground command is running.
			if m.running && m.bashCmd != "" && m.backgrounder != nil {
				m.backgrounder()
			}
			return m, nil
		case "up":
			// Recall previous prompts (slash/worker/picker nav already returned above).
			if len(m.history) > 0 {
				if m.histIdx > 0 {
					m.histIdx--
				}
				m.input.SetValue(m.history[m.histIdx])
				m.input.CursorEnd()
			}
			return m, nil
		case "down":
			if len(m.history) > 0 {
				if m.histIdx < len(m.history) {
					m.histIdx++
				}
				if m.histIdx >= len(m.history) {
					m.input.SetValue("")
				} else {
					m.input.SetValue(m.history[m.histIdx])
					m.input.CursorEnd()
				}
			}
			return m, nil
		case "shift+tab":
			m.modeIdx = (m.modeIdx + 1) % len(modeLabels)
			// Apply immediately to the live agent so it takes effect this run, not
			// just the next one.
			if m.onMode != nil {
				m.onMode(modeLabels[m.modeIdx])
			}
			return m, nil
		case "enter":
			if m.pendingAsk != nil && m.pendingAsk.freeText && strings.TrimSpace(m.input.Value()) != "" {
				ans := m.input.Value()
				m.appendTurn(youGutter, "", ans)
				m.pendingAsk.reply <- ans
				m.pendingAsk = nil
				m.input.Reset()
				m.input.Placeholder = "ask " + m.agentName + " to build, edit, or run something…"
				return m, nil
			}
			if !m.running && strings.TrimSpace(m.input.Value()) != "" {
				m.scrolledUp = false // a new prompt jumps back to following output
				m.suggestion = ""
				prompt := m.input.Value()
				m.history = append(m.history, prompt) // recall with up/down
				m.histIdx = len(m.history)
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
				if looksLikeSlashCommand(strings.TrimSpace(prompt)) {
					if handled, cmd := m.handleSlash(strings.TrimSpace(prompt)); handled {
						return m, cmd
					}
				}
				// A path dragged into the terminal arrives shell-escaped;
				// hand the model something its tools can actually open.
				prompt = unescapeDropped(prompt)
				// Keyboard-mash guard: a cat on the keyboard shouldn't cost a model
				// turn. Warn once and keep the text; pressing enter again sends it.
				if looksLikeMash(prompt) && m.mashWarn != prompt {
					m.mashWarn = prompt
					m.append(metaStyle.Render("that looks like a keyboard mash — press enter again to send it anyway"))
					return m, nil
				}
				m.mashWarn = ""
				if m.title == "" {
					m.title = truncate(prompt, 40)
				}
				m.input.Reset()
				m.remoteOrigin = false
				m.routeAsks(false)
				return m, m.startPrompt("you", prompt)
			}
		}
	case EventMsg:
		m.applyEvent(agent.Event(msg))
		cmds = append(cmds, m.pump())
	case doneMsg:
		m.running = false
		m.runCancel = nil
		if m.askRouter != nil {
			m.askRouter.SetRun(nil)
		}
		// A run cancelled while parked on a question leaves the form on screen
		// with nothing behind it: the asker has already given up on the answer,
		// so submitting it would do nothing. Take it down with the run.
		m.pendingAsk = nil
		m.streamStart, m.streamBuf = -1, "" // clear any partial stream state
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				m.append(metaStyle.Render("⊘ interrupted"))
			} else {
				for _, l := range wrap("error: "+msg.err.Error(), m.width-2) {
					m.append(errStyle.Render(l))
				}
			}
		} else if msg.res != nil {
			m.cost = msg.res.Cost()
			m.turns = msg.res.Turns
			// No completion/recap line: the final message is already in the
			// transcript, and cost/turns show in the footer. Any next-step
			// suggestion came from the reply's [next] line (see applyEvent).
		}
		// A dim "worked for Ns" line closes out the run, so each prompt shows how
		// long it took.
		if !m.runStart.IsZero() {
			m.append(metaStyle.Render("◇ " + fmtElapsed(time.Since(m.runStart))))
		}
		// Fire the next queued message, if any.
		if len(m.queue) > 0 {
			next := m.queue[0]
			m.queue = m.queue[1:]
			m.remoteOrigin = next.remote
			m.routeAsks(next.remote)
			origin := "you"
			if next.remote {
				origin = "phone"
			}
			cmds = append(cmds, m.startPrompt(origin, next.text))
		}
	case tea.MouseMsg:
		overBody := !m.modelPickerOpen && !m.pickerOpen &&
			!m.helpOpen && !m.skillsViewOpen && !m.pluginViewOpen && !m.themeViewOpen &&
			!m.mcpViewOpen && m.pendingAsk == nil && m.workerFocus == ""
		switch msg.Button {
		case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
			// Wheel scrolls the viewport; a scroll cancels any selection.
			m.clearSelection()
			var vc tea.Cmd
			m.vp, vc = m.vp.Update(msg)
			m.scrolledUp = !m.vp.AtBottom()
			return m, vc
		case tea.MouseButtonLeft:
			if !overBody {
				return m, nil
			}
			switch msg.Action {
			case tea.MouseActionPress:
				if pt, ok := m.mouseToContent(msg.X, msg.Y); ok {
					m.selA, m.selB = pt, pt
					m.selecting, m.selMoved, m.hasSel = true, false, false
				}
				return m, nil
			case tea.MouseActionMotion:
				if m.selecting {
					if pt, ok := m.mouseToContent(msg.X, msg.Y); ok {
						m.selB = pt
						if pt != m.selA {
							m.selMoved, m.hasSel = true, true
						}
					}
				}
				return m, nil
			case tea.MouseActionRelease:
				if m.selecting && m.selMoved && m.hasSel {
					// A drag: copy the selection (auto-copy on release).
					m.selecting = false
					if txt := m.selectionText(); strings.TrimSpace(txt) != "" {
						copyToClipboard(txt)
						return m, m.noteCopied()
					}
					return m, nil
				}
				// A plain click (no drag): expand the clicked tool line, if any.
				m.clearSelection()
				line := m.vp.YOffset + msg.Y - 1
				m.toggleInlineTool(line)
				return m, nil
			}
		}
		return m, nil
	case shimmerMsg:
		m.shimmer++
		return m, shimmerTick()
	case remotePromptMsg:
		// Queued rather than started when a run is already going, exactly as a
		// prompt typed during a run is. The agent behind both is one shared
		// conversation, so a second concurrent run would race it.
		if m.running {
			m.queue = append(m.queue, queued{text: msg.text, remote: true})
			cmds = append(cmds, m.pump())
			break
		}
		// No mash guard: this was sent deliberately from another device, and
		// there is nobody here to press enter a second time.
		if m.title == "" {
			m.title = truncate(msg.text, 40)
		}
		m.remoteOrigin = true
		m.routeAsks(true)
		cmds = append(cmds, m.startPrompt("phone", msg.text))
	case remoteInterruptMsg:
		if m.running && m.runCancel != nil {
			m.runCancel()
			m.append(metaStyle.Render("interrupted from the remote"))
		}
		cmds = append(cmds, m.pump())
	case remoteReadyMsg:
		m.remotePublish, m.remoteAsk = msg.publish, msg.ask
		cmds = append(cmds, m.pump())
	case remoteStatusMsg:
		if strings.HasPrefix(msg.text, "link ended") || msg.text == "link closed" {
			m.remoteLinked, m.remotePublish, m.remoteAsk = false, nil, nil
		}
		m.append(metaStyle.Render(msg.text))
		cmds = append(cmds, m.pump())
	case copyClearedMsg:
		return m, nil // just re-render so the "copied" toast disappears
	case mcpProbeMsg:
		m.applyMCPProbe(msg)
		return m, nil
	case loginDoneMsg:
		if msg.err != nil {
			m.append(errStyle.Render("✗ login failed: " + msg.err.Error()))
			return m, nil
		}
		// Credentials exist now — apply the model that triggered the login.
		prov, id := m.loginProv, m.loginModel
		if m.onModel != nil {
			if err := m.onModel(prov, id); err != nil {
				m.append(errStyle.Render("✗ " + err.Error()))
				return m, nil
			}
		}
		m.model, m.provider = id, prov
		m.append(startGlyph.Render("▸ ") + metaStyle.Render("signed in — model set to ") + toolName.Render(id) + metaStyle.Render("  "+prov))
		return m, nil
	case bangMsg:
		for _, l := range strings.Split(strings.TrimRight(msg.out, "\n"), "\n") {
			m.append(resultStyle.Render(l))
		}
		if msg.err != nil {
			m.append(errStyle.Render(msg.err.Error()))
		}
	case pluginOpMsg:
		m.append(metaStyle.Render(msg.text))
		if msg.reload {
			m.applyReload()
		}
	case quitMsg:
		return m, tea.Quit
	case editorDoneMsg:
		if msg.err != nil {
			m.append(errStyle.Render("editor: " + msg.err.Error()))
		} else {
			m.append(metaStyle.Render("config saved — provider/model changes apply on restart"))
		}
	case modelsMsg:
		if msg.err != nil {
			m.append(errStyle.Render(msg.err.Error()))
			break
		}
		m.openModelPicker(msg.groups)
		if !m.modelPickerOpen {
			m.append(metaStyle.Render("no models available"))
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
			// Spinner animation only; the phrase is chosen once per run (see run
			// start) and held for the whole loading time, not rotated.
			m.tick++
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
	bodyH := m.height - lipgloss.Height(m.header()) - lipgloss.Height(footer)
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
	} else if m.skillsViewOpen {
		body = m.skillsBox(bodyH)
	} else if m.mcpViewOpen {
		if m.mcpDetail {
			body = m.mcpDetailBox(bodyH)
		} else {
			body = m.mcpBox(bodyH)
		}
	} else if m.pluginViewOpen {
		body = m.pluginBox(bodyH)
	} else if m.themeViewOpen {
		body = m.themeBox(bodyH)
	} else if m.pickerOpen {
		m.vp.SetContent(m.renderSessions())
		m.vp.GotoBottom()
		body = m.vp.View()
	} else if m.modelPickerOpen {
		body = m.modelBox(bodyH)
	} else if len(m.lines) == 0 && !m.running && m.pendingAsk == nil {
		body = m.welcome()
	} else if m.pendingAsk != nil && !m.pendingAsk.freeText {
		content := m.transcriptContent()
		if content != "" {
			content += "\n"
		}
		m.vp.SetContent(content + m.renderPicker())
		if !m.scrolledUp {
			m.vp.GotoBottom()
		}
		body = m.vp.View()
	} else if m.running {
		// While working, show the spinner + rotating phrase as a live line at the
		// bottom of the conversation, the way Claude Code does.
		content := m.transcriptContent()
		work := m.bashBlock() + m.sp.View() + " " + gradientText(m.phrase()+"…", holoRamp) +
			metaStyle.Render("  "+m.runStats()) + m.queueStrip()
		if content != "" {
			content += "\n"
		}
		m.vp.SetContent(content + work)
		// Don't yank to the bottom mid-selection (it would fight the drag).
		if !m.scrolledUp && !m.hasSel {
			m.vp.GotoBottom()
		}
		body = m.vp.View()
	} else if len(m.lines) > 0 {
		// Idle with a conversation: render the transcript, following the bottom
		// unless the user has scrolled up or is selecting text.
		m.vp.SetContent(m.transcriptContent())
		if !m.scrolledUp && !m.hasSel {
			m.vp.GotoBottom()
		}
		body = m.vp.View()
	}
	return strings.Join([]string{
		m.header(),
		body,
		footer,
	}, "\n")
}

// welcome is the empty-state splash. Layout variants (SetWelcomeStyle):
//
//	0 clean    — logo top-left, directory, model (default)
//	1 boxed    — identity inside a rounded panel
//	2 centered — the original big centered wordmark + tagline
func (m Model) welcome() string {
	bodyH := m.height - lipgloss.Height(m.header()) - 4 // header (2 lines) + footer
	var rows []string
	if m.welcomeStyle == 1 { // boxed
		inner := lipgloss.JoinVertical(lipgloss.Left, m.wLogo(), "", m.wDir(), m.wModel())
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(holoViol).Padding(0, 2).Render(inner)
		rows = []string{box, "", m.wTagline()}
	} else { // clean
		rows = []string{m.wLogo(), "", m.wDir(), "", m.wModel(), "", m.wTagline()}
	}
	content := lipgloss.JoinVertical(lipgloss.Left, rows...)
	return lipgloss.NewStyle().Width(m.width).Height(bodyH).Padding(1, 2).Render(content)
}

// welcomeNames are the selectable empty-state layouts, index-aligned with
// welcomeStyle.
var welcomeNames = []string{"clean", "boxed"}

// welcomeIndex maps a layout name to its welcomeStyle index, or -1 if unknown.
func welcomeIndex(name string) int {
	for i, n := range welcomeNames {
		if n == name {
			return i
		}
	}
	return -1
}

// themeBox renders the /theme picker: each row is drawn in its own theme's
// gradient (a swatch + the name), and the selected theme is applied live so the
// whole UI previews it.
func (m Model) themeBox(bodyH int) string {
	boxW := 44
	if maxW := m.width - 8; boxW > maxW {
		boxW = maxW
	}
	if boxW < 28 {
		boxW = 28
	}
	inner := boxW - 2
	title := crystalStyle.Render("◈ ") + gradientText("theme", holoRamp)
	rule := gradientText(strings.Repeat("─", inner), holoLoop)

	// Plain list — the whole UI (border, header, footer, input) recolors to the
	// highlighted theme live, so the selection itself is the preview.
	var lines []string
	for i, t := range themes {
		if i == m.themeSel {
			lines = append(lines, crystalStyle.Render("◈ "+t.Name))
		} else {
			lines = append(lines, "  "+metaStyle.Render(t.Name))
		}
	}
	foot := metaStyle.Render("↑/↓ preview live · enter apply · esc cancel")
	parts := append([]string{title, rule, ""}, lines...)
	parts = append(parts, "", foot)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(holoCyan).
		Padding(0, 2).Width(boxW).Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, box)
}

// welcomeCentered is the original splash: the big wordmark centered with a
// shimmering tagline beneath it.
func (m Model) wLogo() string {
	logo := renderLogoAt(m.logoVariant, float64(m.shimmer)*0.03)
	if m.version != "" {
		// Sit the version beside the wordmark (vertically centered), not tacked
		// onto the bottom line.
		logo = lipgloss.JoinHorizontal(lipgloss.Center, logo, "  "+metaStyle.Render(m.version))
	}
	return logo
}

func (m Model) wDir() string { return metaStyle.Render(collapseHome(m.workdir)) }

func (m Model) wModel() string {
	if m.model == "" {
		return crystalStyle.Render("◇ ") + metaStyle.Render("no model — /model to configure")
	}
	line := crystalStyle.Render("◇ ") + agentText.Render(m.model)
	if m.provider != "" {
		line += metaStyle.Render(" via " + m.provider)
	}
	if m.workerModel != "" {
		line += metaStyle.Render("   ⋮ worker ") + agentText.Render(m.workerModel)
	}
	return line
}

func (m Model) wTagline() string {
	return metaStyle.Render("a pluggable multi-model coding agent") + "   " +
		crystalStyle.Render("/help") + metaStyle.Render(" commands · ") +
		crystalStyle.Render("/model") + metaStyle.Render(" switch model")
}

// collapseHome shortens $HOME to ~ for display.
func collapseHome(p string) string {
	if p == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(os.PathSeparator)) {
			return "~" + p[len(home):]
		}
	}
	return p
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
	left := crystalStyle.Render("◈  ") + gradientText(strings.ToLower(logoWord), holoRamp)
	if m.model != "" {
		left += metaStyle.Render("      " + m.model)
	}
	bar := headerBar.Width(m.width).Render(left)
	// A slim cyan→violet hairline under the bar frames the session (the
	// framed-minimal direction) without boxing the transcript.
	w := m.width
	if w < 1 {
		w = 1
	}
	return bar + "\n" + m.headerRule(w)
}

// headerRule is the hairline under the header: static when idle, and a lit
// segment sweeping back and forth while a run is in flight.
//
// The same signal the app shows on a session's card and at the top of its
// window. Indeterminate, because a run has no measurable progress — the agent
// takes as many turns as it needs — so a bar that filled end to end would be
// inventing one. This says "still going", which is all that is known.
func (m Model) headerRule(w int) string {
	if !m.running {
		return gradientText(strings.Repeat("─", w), holoLoop)
	}
	pos, seg, ok := sweepSegment(w, m.tick)
	if !ok {
		return gradientText(strings.Repeat("─", w), holoLoop)
	}
	return ruleStyle.Render(strings.Repeat("─", pos)) +
		gradientText(strings.Repeat("─", seg), holoLoop) +
		ruleStyle.Render(strings.Repeat("─", w-pos-seg))
}

// sweepSegment places the lit part of the running rule: how far along it starts
// and how wide it is, ping-ponging with the tick. ok is false when the window
// is too narrow to show a moving segment at all.
//
// Split out from the rendering because the rendering cannot be asserted on: in
// a test there is no terminal, lipgloss emits no escapes, and every position
// draws the same run of dashes.
func sweepSegment(w, tick int) (pos, seg int, ok bool) {
	seg = w / 4
	if seg < 6 {
		seg = 6
	}
	span := w - seg
	if span < 1 {
		return 0, 0, false
	}
	if tick < 0 {
		tick = -tick
	}
	// Walk to the far end over `span` ticks, then back.
	pos = tick % (2 * span)
	if pos > span {
		pos = 2*span - pos
	}
	return pos, seg, true
}

// statsSegment is the context meter (when the window is known) + running cost,
// shown at the bottom-right.
// changedFile is one file this session has touched, with its running diffstat.
type changedFile struct {
	path           string
	added, removed int
	created        bool
}

// noteChange folds an edit into the session's running list of changed files.
//
// Accumulated rather than replaced: a file edited four times has one entry
// whose counts are the whole session's work on it, which is the question
// "what has this changed" actually asks. Kept in first-touch order, because
// that is the order the work happened in and it does not reshuffle under you
// while you read it.
func (m *Model) noteChange(path string, added, removed int, created bool) {
	if path == "" {
		return
	}
	for i := range m.changed {
		if m.changed[i].path == path {
			m.changed[i].added += added
			m.changed[i].removed += removed
			return
		}
	}
	m.changed = append(m.changed, changedFile{path: path, added: added, removed: removed, created: created})
}

// changedSummary is the one-line "this session has touched N files" for the
// bottom bar.
//
// A count and a total, not a list: the bar has room for a fact, and the list
// is a keystroke away in /files. It is the number you want continuously — how
// much is uncommitted — rather than the detail you want occasionally.
func (m Model) changedSummary() string {
	if len(m.changed) == 0 {
		return ""
	}
	var added, removed int
	for _, c := range m.changed {
		added += c.added
		removed += c.removed
	}
	s := metaStyle.Render(fmt.Sprintf("%d file%s ", len(m.changed), plural(len(m.changed)))) +
		diffAddStyle.Render(fmt.Sprintf("+%d", added))
	if removed > 0 {
		s += " " + diffDelStyle.Render(fmt.Sprintf("-%d", removed))
	}
	return s
}

func (m Model) statsSegment() string {
	cost := costStyle.Render(fmt.Sprintf("$%.4f", m.cost))
	// The meter tracks how full the model's context window is: the latest turn's
	// total input-side tokens (fresh + cache), not the cumulative session input.
	ctx := m.ctxTok
	if ctx == 0 {
		ctx = m.inTok // fall back to cumulative input if no per-turn context reported
	}
	seg := cost
	if win, ok := contextWindow(m.model); ok && ctx > 0 {
		frac := float64(ctx) / float64(win)
		if frac > 1 {
			frac = 1
		}
		seg = crystalMeter(frac, 6) + metaStyle.Render(fmt.Sprintf(" %d%%", int(frac*100+0.5))) +
			metaStyle.Render("   ·   ") + cost
	}
	if files := m.changedSummary(); files != "" {
		seg = files + metaStyle.Render("   ·   ") + seg
	}
	return seg
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
		logLine = startGlyph.Render("▸ ") + toolName.Render(prettyToolName(name)) + " " + toolArgs.Render(truncate(args, 60))
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
	case "task":
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
	hint := ""
	if m.backgrounder != nil {
		hint = " · ⌃b bg"
	}
	b.WriteString(checkStyle.Render(dotsFrame(m.tick)+" ") +
		agentText.Render(fmt.Sprintf("Running %d shell command%s ", len(lines), plural(len(lines)))) +
		metaStyle.Render(fmt.Sprintf("· %ds…%s", elapsed, hint)))
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
		b.WriteString("\n" + metaStyle.Render("  ⏱ "+truncate(oneLine(q.text), 64)))
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

// isConfigured reports whether prov has been set up (it is the active lead or
// worker provider), for the "configured" marker in the picker.
func (m Model) isConfigured(prov string) bool {
	return prov != "" && (prov == m.provider || prov == m.workerProv)
}

// modelBox draws the /model picker as a centered, bordered box (crush-style): a
// title with a hatch rule and a Lead/Worker toggle, provider group headers with
// a "configured" marker, a windowed model list, and a footer hint.
func (m Model) modelBox(bodyH int) string {
	boxW := 64
	if maxW := m.width - 8; boxW > maxW {
		boxW = maxW
	}
	if boxW < 32 {
		boxW = 32
	}
	inner := boxW - 2 // Padding(0,1)

	// Title row: a holographic "switch model" wordmark with a crystal Lead/Worker
	// toggle on the right, over a gradient rule — the dross look, not a hatch bar.
	title := crystalStyle.Render("◈ ") + gradientText("switch model", holoRamp)
	var toggle string
	if m.onWorker != nil {
		lead, worker := "◇ lead", "◇ worker"
		if m.modelSlot == 0 {
			lead = "◈ lead"
		} else {
			worker = "◈ worker"
		}
		leadTab, workerTab := metaStyle.Render(lead), metaStyle.Render(worker)
		if m.modelSlot == 0 {
			leadTab = crystalStyle.Render(lead)
		} else {
			workerTab = crystalStyle.Render(worker)
		}
		toggle = leadTab + "  " + workerTab
	}
	gap := inner - lipgloss.Width(title) - lipgloss.Width(toggle)
	if gap < 1 {
		gap = 1
	}
	titleRow := title + strings.Repeat(" ", gap) + toggle
	rule := gradientText(strings.Repeat("─", inner), holoLoop)

	// Subtitle / search line.
	var subRow string
	switch {
	case m.modelSearching:
		subRow = metaStyle.Render("search ") + agentText.Render(m.modelQuery) + crystalStyle.Render("▏")
	case m.modelQuery != "":
		subRow = metaStyle.Render("search " + m.modelQuery)
	case m.modelSlot == 1:
		subRow = metaStyle.Render("› choose the worker model for cheap, bounded subtasks")
	default:
		subRow = metaStyle.Render("› choose the lead model for planning and hard tasks")
	}

	// Windowed model list around the selection: a small fixed viewport that
	// scrolls, not a full-height dump of every model.
	maxList := 12
	if h := bodyH - 10; h < maxList {
		maxList = h
	}
	if maxList < 4 {
		maxList = 4
	}
	start := 0
	if m.modelSel >= maxList {
		start = m.modelSel - maxList + 1
	}
	end := start + maxList
	if end > len(m.modelFiltered) {
		end = len(m.modelFiltered)
	}

	curM, curP := m.model, m.provider
	if m.modelSlot == 1 {
		curM, curP = m.workerModel, m.workerProv
	}

	var lines []string
	lastProv := ""
	for row := start; row < end; row++ {
		idx := m.modelFiltered[row]
		prov, id := m.modelFlatProv[idx], m.modelFlat[idx]
		if prov != lastProv {
			hdr := metaStyle.Render(strings.ToUpper(prov))
			if m.isConfigured(prov) {
				mark := crystalStyle.Render("◆ configured")
				pad := inner - lipgloss.Width(hdr) - lipgloss.Width(mark)
				if pad < 1 {
					pad = 1
				}
				hdr += strings.Repeat(" ", pad) + mark
			}
			lines = append(lines, hdr)
			lastProv = prov
		}
		if row == m.modelSel {
			// Selected: a crystal caret + the id in the holographic gradient — no
			// full-width highlight bar.
			label := gradientText(id, holoRamp)
			if id == curM && prov == curP {
				label += metaStyle.Render("  (current)")
			}
			lines = append(lines, crystalStyle.Render("◈ ")+label)
			continue
		}
		line := "  " + agentText.Render(id)
		if id == curM && prov == curP {
			line += metaStyle.Render("  (current)")
		}
		lines = append(lines, line)
	}
	if len(m.modelFiltered) == 0 {
		lines = append(lines, metaStyle.Render("  no matches"))
	} else if start > 0 || end < len(m.modelFiltered) {
		lines = append(lines, metaStyle.Render(fmt.Sprintf("  %d–%d of %d", start+1, end, len(m.modelFiltered))))
	}

	foot := "↑/↓ choose · tab lead/worker · / search · enter confirm · esc exit"
	if m.onWorker == nil {
		foot = "↑/↓ choose · / search · enter confirm · esc exit"
	}
	if m.modelSearching {
		foot = "type to filter · ↑/↓ choose · enter confirm · esc clear"
	}

	parts := append([]string{titleRow, rule, "", subRow, ""}, lines...)
	parts = append(parts, "", metaStyle.Render(foot))
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(holoCyan).
		Padding(0, 1).
		Width(boxW).
		Render(content)
	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, box)
}

// chosen returns the selected option strings for question i.
func (a *askState) chosen(i int) []string {
	var out []string
	for j, v := range a.sel[i] {
		if v {
			out = append(out, a.questions[i].Options[j])
		}
	}
	if i < len(a.writeIn) {
		if w := strings.TrimSpace(a.writeIn[i]); w != "" {
			out = append(out, w)
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
		m.appendTurn(youGutter, "", agentText.Render(label+" → "+ans))
	}
	// One question answers with the choice alone.
	//
	// The labelled "header: answer" form exists to tell several answers apart,
	// and with a single question there is nothing to tell apart. It also broke
	// permission gates outright: approve() matches the offered option exactly —
	// deliberately, so that prose containing the word "allow" cannot approve a
	// destructive command — and "bash: Allow" is not "Allow", so choosing Allow
	// denied the call.
	if len(a.questions) == 1 {
		a.reply <- strings.Join(a.chosen(0), ", ")
		m.pendingAsk = nil
		return
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

// renderHelp draws the shortcuts overlay (dross's own keybindings).
func renderHelp() string {
	rows := [][2]string{
		{"enter", "send message"},
		{"shift+tab", "cycle mode (auto/manual/accept-edits/plan)"},
		{"↑ ↓", "prompt history · move in menus"},
		{"tab", "accept suggestion / complete slash command"},
		{"←", "session switcher"},
		{"ctrl+c", "interrupt run · twice to exit"},
		{"esc", "clear input · close menus"},
		{"ctrl+y", "copy last reply to clipboard"},
		{"ctrl+o", "expand / collapse the last tool output"},
		{"ctrl+b", "background the running shell command"},
		{"! cmd", "run a shell command directly"},
		{"@path", "attach a file to your prompt"},
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
	b.WriteString(agentLabel.Render("dross sessions") + "  " +
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
		// Write-in row: always shown below options.
		writeInRow := len(q.Options)
		wiCur := "  "
		wiStyle := resultStyle
		if a.cursor[a.tab] == writeInRow {
			wiCur, wiStyle = promptMark.Render("› "), agentText
		}
		wiText := a.writeIn[a.tab]
		if a.editing && a.cursor[a.tab] == writeInRow {
			// Show a blinking cursor at the end of the write-in text.
			b.WriteString(wiCur + wiStyle.Render("✎ "+wiText) + agentText.Render("█") + "\n")
		} else if wiText != "" {
			b.WriteString(wiCur + wiStyle.Render("✎ "+wiText) + "\n")
		} else {
			b.WriteString(wiCur + metaStyle.Render("✎ write in…") + "\n")
		}
		hint := "←→ tabs · ↑↓ move · enter next/edit"
		if q.MultiSelect {
			hint = "←→ tabs · ↑↓ move · space toggle · enter next/edit"
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
	// A chevron caret marks the input line — distinct from the mode's ◆ glyph so
	// the "type here" prompt and the status indicator don't read as the same mark.
	mark, boxStyle := crystalStyle.Render("❯ "), inputBox
	if bang {
		mark, boxStyle = goldStyle.Render("! "), inputBox.BorderForeground(gold)
	}
	// Placeholder: the hint on a fresh session, empty once the conversation has
	// started, or the contextual next-step suggestion (ghost text) when there is
	// one. The ask flow sets its own placeholder, so leave it alone then.
	if m.pendingAsk == nil {
		switch {
		case m.suggestion != "":
			m.input.Placeholder = m.suggestion
		case len(m.lines) > 0:
			m.input.Placeholder = ""
		default:
			m.input.Placeholder = "ask " + m.agentName + " to build, edit, or run something…"
		}
	}
	line := mark + m.input.View()
	box := boxStyle.Width(m.width - 2).Render(line)
	// Slash-command autocomplete: matching commands listed above the input; the
	// first is the Tab target.
	var suggest string
	// Slash autocomplete is available anytime — even mid-run (read-only commands
	// dispatch while a run is in flight).
	if ms := m.matchSlash(); len(ms) > 0 {
		sel := m.slashSel
		if sel >= len(ms) {
			sel = len(ms) - 1
		}
		// Small scrolling window around the selection (the full list can be long
		// with skills), each row truncated to the terminal width.
		const maxRows = 8
		start := 0
		if sel >= maxRows {
			start = sel - maxRows + 1
		}
		end := start + maxRows
		if end > len(ms) {
			end = len(ms)
		}
		var rows []string
		for i := start; i < end; i++ {
			c := ms[i]
			cur, style := "  ", metaStyle
			if i == sel {
				cur, style = promptMark.Render("› "), agentText
			}
			// Truncate the (plain) description so the styled line fits the
			// width, leaving room for the keystroke when there is one.
			keyW := 0
			if c.key != "" {
				keyW = len([]rune(c.key)) + 2
			}
			descW := m.width - 2 - 2 - len([]rune(c.name)) - 2 - keyW
			desc := c.desc
			if descW > 1 {
				desc = truncate(desc, descW)
			} else {
				desc = ""
			}
			row := cur + style.Render(c.name) + metaStyle.Render("  "+desc)
			if c.key != "" {
				// Right-aligned, so the few commands with a shortcut form a
				// column instead of trailing off the end of their description.
				pad := m.width - 2 - lipgloss.Width(row) - len([]rune(c.key))
				if pad < 1 {
					pad = 1
				}
				row += strings.Repeat(" ", pad) + costStyle.Render(c.key)
			}
			rows = append(rows, row)
		}
		if start > 0 || end < len(ms) {
			rows = append(rows, metaStyle.Render(fmt.Sprintf("  %d–%d of %d", start+1, end, len(ms))))
		}
		suggest = strings.Join(rows, "\n") + "\n"
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
	// A brief "copied" confirmation after any copy (drag-select, ctrl+y, /copy).
	if !m.copiedAt.IsZero() && time.Since(m.copiedAt) < 2*time.Second {
		mode += checkStyle.Render("  · ✓ copied to clipboard")
	}
	// First ctrl+c (when idle) shows the exit hint here, under the prompt.
	if !m.running && !m.lastCtrlC.IsZero() && time.Since(m.lastCtrlC) < time.Second {
		mode += goldStyle.Render("  · press ctrl+c again to exit")
	}
	// Bottom bar: mode/hints on the left, context meter + cost on the right.
	modeLeft := "  " + mode
	stats := m.statsSegment()
	gap := m.width - lipgloss.Width(modeLeft) - lipgloss.Width(stats) - 2
	if gap < 2 {
		gap = 2
	}
	out := box + "\n" + modeLeft + strings.Repeat(" ", gap) + stats
	// The live subagent tree sits under the prompt while workers run, the way
	// Claude Code shows its subagents.
	if m.running {
		if tree := m.workerTree(); tree != "" {
			out += tree
		}
	}
	// A blank line above the input bar so the prompt isn't flush against the
	// last transcript line.
	return "\n" + out
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
	// A non-tool block (message, review, ask, …) ends any open tool group and gets
	// a blank line before it. Tool calls collapse into one summary line instead.
	if !worker {
		switch kind {
		case "text", "review", "answer", "ask", "checkpoint", "error", "edit":
			m.closeToolGroup()
			m.turnGap()
		}
	}
	switch kind {
	case "text-delta":
		// Live-stream assistant prose: a growing plain block under the orb,
		// finalized (markdown) by the "text" event. Main session only.
		if worker || e.Data == "" {
			break
		}
		if m.streamStart < 0 {
			m.closeToolGroup()
			m.turnGap()
			m.streamStart = len(m.lines)
			m.streamBuf = ""
		}
		m.streamBuf += e.Data
		m.renderStream()
	case "text":
		data := e.Data
		// If we were streaming this reply, drop the raw growing block; the final
		// text is re-rendered with markdown just below.
		if m.streamStart >= 0 {
			m.lines = m.lines[:m.streamStart]
			m.streamStart, m.streamBuf = -1, ""
		}
		if !worker {
			// The model may end with a "[next] <prompt>" line proposing a follow-up.
			// Pull it out (only when there's a real next step) to offer as a ghost
			// suggestion in the input; strip it from the shown text.
			var next string
			data, next = extractNext(data)
			if next != "" {
				m.suggestion = next
			}
			// Fold a lone "[Label]" first line onto its headline so the marker is
			// never a bare two-word line.
			data = hoistMarker(data)
			m.lastText = truncate(oneLine(data), 70)
			m.lastReply = data // full text, for ctrl+y copy
			m.bashCmd = ""     // a message means the foreground command finished
		}
		// Claude-Code style: the Dross orb leads the block, the summary sits on
		// that line, and the rest of the body aligns underneath the text.
		// Markdown is rendered to styled terminal output via glamour.
		width := m.width - 4 - len([]rune(indent))
		lines := renderMarkdown(data, width)
		if len(lines) == 0 {
			lines = []string{""}
		}
		bar := agentGutter.Render(gutterMark)
		m.append(indent + bar + " " + drossBullet.Render(drossOrbGlyph) + " " + lines[0])
		for _, l := range lines[1:] {
			m.append(indent + bar + "   " + l)
		}
	case "tool":
		name, args, _ := strings.Cut(e.Data, " ")
		if worker {
			// Worker tool calls stay in the subagent feed, indented.
			m.append(indent + orbTool.Render(drossOrbGlyph+" ") + toolName.Render(prettyToolName(name)) + " " + toolArgs.Render(truncate(toolArgLine(name, args), 80)))
			break
		}
		// Surface a foreground bash command live (the working line shows it);
		// background bash is a shell, tracked separately.
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
		m.toolRecs = append(m.toolRecs, toolRec{name: name, args: toolArgLine(name, args)})
		// write/edit are shown as their own line by the following "edit" event
		// (path + diff), so users can see file changes; don't collapse them.
		if name == "write" || name == "edit" {
			break
		}
		// Collapse the noisy read-only/shell tools into one dim summary line
		// (expand with ctrl+o or a click), instead of a line per call + result.
		if m.groupLine < 0 {
			m.turnGap()
			m.append("") // placeholder, filled by toolGroupSummary below
			m.groupLine = len(m.lines) - 1
			m.groupFirstRec = len(m.toolRecs) - 1
			m.groupCounts = map[string]int{}
			m.groupOrder = nil
		}
		if m.groupCounts[name] == 0 {
			m.groupOrder = append(m.groupOrder, name)
		}
		m.groupCounts[name]++
		m.lines[m.groupLine] = m.toolGroupSummary()
		if m.toolLineOf == nil {
			m.toolLineOf = map[int]int{}
		}
		m.toolLineOf[m.groupLine] = m.groupFirstRec
	case "tool-result":
		if worker {
			m.append(indent + resultStyle.Render("  └ ") + resultStyle.Render(truncate(oneLine(e.Data), 68)))
			break
		}
		// Collapsed into the group summary; the full result is kept on the rec and
		// shown when expanded (ctrl+o / click).
		m.bashCmd = ""
		if n := len(m.toolRecs); n > 0 {
			m.toolRecs[n-1].result = e.Data
		}
	case "edit":
		// Show file changes with the actual diff (context collapsed), so users see
		// exactly what dross changed.
		var ed struct {
			File    string `json:"file"`
			Added   int    `json:"added"`
			Removed int    `json:"removed"`
			Diff    string `json:"diff"`
			New     bool   `json:"new"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(e.Data), &ed) == nil {
			m.noteChange(ed.File, ed.Added, ed.Removed, ed.New)
			base := ed.File
			if i := strings.LastIndexByte(base, '/'); i >= 0 {
				base = base[i+1:]
			}
			bandW := m.width - len([]rune(indent)) - 2
			if ed.New {
				// New file: print its content, not a diff.
				m.append(indent + drossBullet.Render(drossOrbGlyph+" ") + resultStyle.Render("created ") + toolName.Render(base) + " " +
					diffAddStyle.Render(fmt.Sprintf("+%d", ed.Added)))
				for _, l := range renderFileContent(ed.Content, bandW, 200) {
					m.append(indent + "  " + l)
				}
			} else {
				m.append(indent + drossBullet.Render(drossOrbGlyph+" ") + resultStyle.Render("edited ") + toolName.Render(base) + " " +
					diffAddStyle.Render(fmt.Sprintf("+%d", ed.Added)) + " " +
					diffDelStyle.Render(fmt.Sprintf("-%d", ed.Removed)))
				for _, l := range renderDiff(ed.Diff, bandW, 200) {
					m.append(indent + "  " + l)
				}
			}
		}
	case "usage":
		var u struct {
			In  int64 `json:"in"`
			Out int64 `json:"out"`
			Ctx int64 `json:"ctx"`
		}
		if json.Unmarshal([]byte(e.Data), &u) == nil {
			m.inTok, m.outTok, m.ctxTok = u.In, u.Out, u.Ctx
		}
	case "review":
		width := m.width - 14
		for i, l := range wrap(e.Data, width) {
			label := "  "
			if i == 0 {
				label = orbDone.Render(drossOrbGlyph) + " " + resultStyle.Render("review ")
			}
			m.append(label + agentText.Render(l))
		}
	case "ask":
		width := m.width - 14
		lines := wrap(e.Data, width)
		m.append(drossBullet.Render(drossOrbGlyph) + " " + resultStyle.Render("asks ") + agentText.Render(lines[0]))
		for _, l := range lines[1:] {
			m.append("  " + agentText.Render(l))
		}
	case "answer":
		// The answer is shown immediately when the user hits enter; skip here.
	case "checkpoint":
		m.append(indent + orbDone.Render(drossOrbGlyph) + " " + resultStyle.Render(e.Data))
	case "error":
		// Wrap the full error (don't truncate) so it's readable and selectable.
		for i, l := range wrap(e.Data, m.width-4) {
			prefix := "  "
			if i == 0 {
				prefix = "✗ "
			}
			m.append(indent + errStyle.Render(prefix+l))
		}
	}
}

// closeToolGroup ends the current collapsed tool-call group so the next tool
// call starts a fresh summary line.
func (m *Model) closeToolGroup() {
	m.groupLine = -1
	m.groupCounts = nil
	m.groupOrder = nil
}

// toolGroupSummary renders the dim one-line summary of the open tool group, e.g.
// "Ran 3 shell commands, read 1 file  · ctrl+o to expand".
func (m Model) toolGroupSummary() string {
	var parts []string
	for _, name := range m.groupOrder {
		parts = append(parts, toolPhrase(name, m.groupCounts[name]))
	}
	s := strings.Join(parts, ", ")
	if s != "" {
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	return orbTool.Render(drossOrbGlyph) + " " + metaStyle.Render(s) + metaStyle.Render("  · ctrl+o to expand")
}

// toolPhrase describes n calls of a tool in Claude-style prose.
func toolPhrase(name string, n int) string {
	switch name {
	case "bash":
		return fmt.Sprintf("ran %d shell command%s", n, plural(n))
	case "read":
		return fmt.Sprintf("read %d file%s", n, plural(n))
	case "grep":
		return fmt.Sprintf("searched %d pattern%s", n, plural(n))
	case "glob":
		return fmt.Sprintf("globbed %d pattern%s", n, plural(n))
	case "write":
		return fmt.Sprintf("wrote %d file%s", n, plural(n))
	case "edit":
		return fmt.Sprintf("edited %d file%s", n, plural(n))
	case "web_fetch":
		return fmt.Sprintf("fetched %d URL%s", n, plural(n))
	case "web_search":
		return fmt.Sprintf("searched the web %d time%s", n, plural(n))
	case "task":
		return fmt.Sprintf("delegated %d task%s", n, plural(n))
	default:
		if server, _, ok := mcpToolParts(name); ok {
			return fmt.Sprintf("called %s via MCP %d time%s", server, n, plural(n))
		}
		return fmt.Sprintf("%d %s call%s", n, name, plural(n))
	}
}

// mcpToolParts splits an MCP tool id "server__tool" into its parts.
func mcpToolParts(name string) (server, tool string, ok bool) {
	if i := strings.Index(name, "__"); i > 0 && i+2 < len(name) {
		return name[:i], name[i+2:], true
	}
	return "", "", false
}

// prettyToolName renders a tool id for display: MCP tools show as
// "server (MCP): tool", others unchanged.
func prettyToolName(name string) string {
	if server, tool, ok := mcpToolParts(name); ok {
		return server + " (MCP): " + tool
	}
	return name
}

// humanTokens renders a token count compactly (e.g. 1234 → "1.2k").
func humanTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// fmtElapsed renders a run duration as "worked for 9s" / "worked for 5m 16s".
func fmtElapsed(d time.Duration) string {
	s := int(d.Seconds() + 0.5)
	if s < 60 {
		return fmt.Sprintf("worked for %ds", s)
	}
	return fmt.Sprintf("worked for %dm %ds", s/60, s%60)
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

// turnGap inserts a blank line before a new turn so the log breathes, unless the
// log is empty or already ends in a blank line.
func (m *Model) turnGap() {
	if len(m.lines) > 0 && m.lines[len(m.lines)-1] != "" {
		m.append("")
	}
}

// gutterMark is the rule drawn down the left of a person's turn.
const gutterMark = "▌"

// gutterFor picks the rule colour, and the label worth keeping beside it, for a
// turn's origin.
//
// Nothing is written next to a local prompt: the colour already says it, and
// "you" next to the thing you just typed is a word that tells you nothing. A
// remote one keeps its name, because which phone-shaped elsewhere it came from
// is information, and because a colour alone is a poor way to carry the one
// case that matters when you did not type it.
func gutterFor(label string) (lipgloss.Style, string) {
	if label == "you" {
		return youGutter, ""
	}
	return remoteGutter, label
}

// appendTurn writes a person's turn into the feed with a rule down its left.
//
// One feed line per line of text rather than a single multi-line entry, because
// the click-to-expand bookkeeping indexes m.lines and a wrapped block would put
// its arithmetic out.
func (m *Model) appendTurn(bar lipgloss.Style, label, text string) {
	mark := bar.Render(gutterMark)
	for i, line := range strings.Split(text, "\n") {
		if i == 0 && label != "" {
			line = bar.Render(label) + "  " + line
		}
		m.append(mark + " " + line)
	}
}

func (m *Model) append(line string) {
	m.lines = append(m.lines, line)
	m.vp.SetContent(strings.Join(m.lines, "\n"))
	m.vp.GotoBottom()
}

// renderStream re-renders the live streaming prose block (plain, growing) under
// the Dross orb, replacing the previous partial. The "text" event later swaps it
// for the markdown-rendered final.
func (m *Model) renderStream() {
	if m.streamStart < 0 {
		return
	}
	m.lines = m.lines[:m.streamStart]
	width := m.width - 4
	var wrapped []string
	for _, para := range strings.Split(m.streamBuf, "\n") {
		if para == "" {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, wrap(para, width)...)
	}
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	bar := agentGutter.Render(gutterMark)
	m.append(bar + " " + drossBullet.Render(drossOrbGlyph) + " " + proseStyle.Render(wrapped[0]))
	for _, l := range wrapped[1:] {
		m.append(bar + "   " + proseStyle.Render(l))
	}
}

func (m *Model) layout() {
	headerH, footerH := 2, 4 // header is a bar + a gradient hairline
	m.vp = viewport.New(m.width, m.height-headerH-footerH)
	m.vp.SetContent(strings.Join(m.lines, "\n"))
	m.vp.GotoBottom()
	m.input.Width = m.width - 6
}

// --- run plumbing ---------------------------------------------------------

// startRun spawns the agent run; events and the final doneMsg flow through msgs.
// beginRun sets up a cancelable context for a run (so ctrl+c can interrupt it)
// and returns the command that starts it.
func (m *Model) beginRun(prompt string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	// A question asked during this run dies with it, so ctrl+c reaches a run
	// parked on one.
	if m.askRouter != nil {
		m.askRouter.SetRun(ctx)
	}
	return m.startRun(ctx, prompt)
}

func (m Model) startRun(ctx context.Context, prompt string) tea.Cmd {
	return func() tea.Msg {
		mode := modeLabels[m.modeIdx]
		go func() {
			pub := m.remotePublish
			if pub != nil && !m.remoteOrigin {
				// The prompt first, so the phone sees the request before the
				// work it produced — but only when it was typed here. A prompt
				// that arrived over the relay was already recorded by the
				// server when it relayed it, and publishing it again put the
				// message in the transcript twice, on every client at once.
				// relay.Client's own run path has carried this guard from the
				// start; this path did not.
				pub("prompt", prompt)
			}
			res, err := m.run(ctx, prompt, mode, func(e agent.Event) {
				m.msgs <- EventMsg(e)
				if pub != nil {
					pub(e.Kind, e.Data)
				}
			})
			if pub != nil {
				// Close the run on the far end too, or the session sits
				// "running" on the phone forever.
				switch {
				case err != nil:
					pub("error", err.Error())
				case res != nil:
					pub("done", fmt.Sprintf("$%.4f · %d turns", res.Cost(), res.Turns))
				default:
					pub("done", "")
				}
			}
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

// toolArgLine renders a tool call's arguments as the thing it acts on — the
// command, path, or url — instead of raw JSON. Falls back to the JSON if it
// can't parse or the tool is unknown.
func toolArgLine(name, argsJSON string) string {
	var in map[string]any
	if json.Unmarshal([]byte(argsJSON), &in) != nil {
		return argsJSON
	}
	str := func(k string) string { v, _ := in[k].(string); return v }
	switch name {
	case "bash", "monitor":
		return oneLine(str("command"))
	case "read", "write", "edit", "publish":
		return str("path")
	case "web_fetch":
		return str("url")
	case "shell_output":
		return str("id")
	case "task":
		if t := str("task"); t != "" {
			return oneLine(t)
		}
		return argsJSON
	}
	return argsJSON
}

func oneLine(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ") }

func truncate(s string, n int) string {
	s = oneLine(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// --- inline tool output ------------------------------------------------------
//
// Expanding a "Ran 3 shell commands" line opens it where it sits, rather than
// switching the body to a separate tool view. What ran is part of reading the
// conversation: it belongs beside the reply it produced, and swapping the whole
// pane out to show a few lines of output loses your place to do it.
//
// The feed is a flat slice of already-rendered lines, so expanding splices the
// detail in and collapsing takes it back out. Every index stored into that
// slice has to move with it, which is what shiftLineIndexes does.

// toolDetailLines renders one tool record as indented output under its summary.
func (m *Model) toolDetailLines(rec int) []string {
	if rec < 0 || rec >= len(m.toolRecs) {
		return nil
	}
	t := m.toolRecs[rec]
	style := resultStyle
	if t.isErr {
		style = errStyle
	}
	var out []string
	head := "  └ " + t.name
	if t.args != "" {
		head += " " + truncate(oneLine(t.args), max(20, m.width-24))
	}
	out = append(out, style.Render(head))
	body := strings.TrimRight(t.result, "\n")
	if body == "" {
		return append(out, style.Render("    (no output)"))
	}
	for _, ln := range strings.Split(body, "\n") {
		out = append(out, style.Render("    "+truncate(ln, max(20, m.width-8))))
	}
	return out
}

// shiftLineIndexes moves every remembered position in m.lines that sits at or
// after `at` by delta, so splicing does not leave click targets and the stream
// cursor pointing at the wrong rows.
func (m *Model) shiftLineIndexes(at, delta int) {
	if delta == 0 {
		return
	}
	shifted := make(map[int]int, len(m.toolLineOf))
	for line, rec := range m.toolLineOf {
		if line >= at {
			line += delta
		}
		shifted[line] = rec
	}
	m.toolLineOf = shifted
	if m.groupLine >= at {
		m.groupLine += delta
	}
	if m.streamStart >= at {
		m.streamStart += delta
	}
	for i, ln := range m.expandedAt {
		if ln >= at {
			m.expandedAt[i] = ln + delta
		}
	}
}

// toggleInlineTool expands or collapses the tool output under the summary on
// the given feed line.
func (m *Model) toggleInlineTool(line int) {
	rec, ok := m.toolLineOf[line]
	if !ok || rec < 0 || rec >= len(m.toolRecs) {
		return
	}
	if n, open := m.expandedLen[line]; open {
		// Collapse: drop the lines we inserted and pull everything back up.
		end := line + 1 + n
		if end > len(m.lines) {
			end = len(m.lines)
		}
		m.lines = append(m.lines[:line+1], m.lines[end:]...)
		delete(m.expandedLen, line)
		m.dropExpandedAt(line)
		m.shiftLineIndexes(line+1, -n)
		m.vp.SetContent(strings.Join(m.lines, "\n"))
		return
	}
	detail := m.toolDetailLines(rec)
	if len(detail) == 0 {
		return
	}
	rest := append([]string(nil), m.lines[line+1:]...)
	m.lines = append(m.lines[:line+1], detail...)
	m.lines = append(m.lines, rest...)
	if m.expandedLen == nil {
		m.expandedLen = map[int]int{}
	}
	m.shiftLineIndexes(line+1, len(detail))
	m.expandedLen[line] = len(detail)
	m.expandedAt = append(m.expandedAt, line)
	m.vp.SetContent(strings.Join(m.lines, "\n"))
}

func (m *Model) dropExpandedAt(line int) {
	for i, ln := range m.expandedAt {
		if ln == line {
			m.expandedAt = append(m.expandedAt[:i], m.expandedAt[i+1:]...)
			return
		}
	}
}

// lastToolLine is the most recent expandable summary line, which is what ctrl+o
// acts on when nothing was clicked.
func (m *Model) lastToolLine() int {
	best := -1
	for line := range m.toolLineOf {
		if line > best && line < len(m.lines) {
			best = line
		}
	}
	return best
}

// startPrompt echoes a prompt and begins its run, clearing the per-run view
// state so the previous turn's worker and shell panels do not bleed into it.
//
// Three callers now: a prompt typed at the input, one drained from the queue
// when the run before it finished, and one arriving over the relay. The first
// two held identical copies of this and had already drifted apart once.
func (m *Model) startPrompt(label, text string) tea.Cmd {
	m.turnGap()
	bar, name := gutterFor(label)
	m.appendTurn(bar, name, text)
	m.running = true
	m.runStart = time.Now()
	m.tick, m.phraseIdx, m.phraseSeq = 0, m.phraseSeq, m.phraseSeq+1
	m.workerOrder, m.workerAct, m.workerLog = nil, nil, nil
	m.workerSel, m.workerFocus = 0, ""
	m.shellOrder, m.shells = nil, nil
	m.bashCmd = ""
	return tea.Batch(m.beginRun(text), m.sp.Tick, m.pump())
}

// looksLikeSlashCommand distinguishes a command from a path that merely starts
// the same way.
//
// Every "/"-prefixed input used to be routed to the command handler, which is
// right for /model and wrong for the thing a terminal pastes when you drag a
// file into it: an absolute path, reported back as "unknown command:
// /var/folders/...". A command is a single word — the further slashes in a
// path are what tell them apart. An unknown one still reports as unknown, so a
// mistyped /mdoel does not become a prompt.
func looksLikeSlashCommand(text string) bool {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "/") || t == "/" {
		return false
	}
	first := strings.Fields(t)[0]
	return !strings.Contains(first[1:], "/")
}

// unescapeDropped turns what a terminal pastes on drag-and-drop back into
// plain paths.
//
// A drop arrives shell-escaped — spaces as "\ ", a trailing space, sometimes
// quotes — because the terminal writes it as you would type it. Left alone it
// reaches the model as a path with stray backslashes that no tool can open.
//
// Applied only when every token really is a file that exists, so ordinary
// prose keeps its punctuation: a sentence about "C:\\Users" or an apostrophe
// is not silently rewritten.
func unescapeDropped(text string) string {
	t := strings.TrimSpace(text)
	if t == "" || !strings.ContainsAny(t, "\\'\"") {
		return text
	}
	fields, ok := splitDropped(t)
	if !ok || len(fields) == 0 {
		return text
	}
	for _, f := range fields {
		if st, err := os.Stat(f); err != nil || st.IsDir() {
			return text
		}
	}
	return strings.Join(fields, " ")
}

// splitDropped splits on unescaped spaces, honouring backslash escapes and
// single or double quotes the way a shell would.
func splitDropped(s string) ([]string, bool) {
	var (
		out   []string
		cur   strings.Builder
		quote rune
		esc   bool
	)
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\' && quote != '\'':
			esc = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if esc || quote != 0 {
		return nil, false // unterminated: not something we should rewrite
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, true
}
