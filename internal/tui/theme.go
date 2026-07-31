package tui

import "github.com/charmbracelet/lipgloss"

// themeDef is a named color variant. Only the accent family and the holographic
// gradient change between themes; the neutral grays (fg/muted/base/panel) stay
// constant so contrast holds.
type themeDef struct {
	Name     string
	accent   string   // primary accent (labels, glyphs)
	accentHi string   // brighter accent (highlights, cost)
	cyan     string   // gradient start / crystal glyphs / input border
	viol     string   // gradient end / boxes
	bg       string   // background
	ramp     []string // gradient stops, start → end
}

// themes are the selectable variants (/theme). "aetheric" is the default: the
// spirit made visible, an iridescent violet -> cyan -> magenta aurora on void.
var themes = []themeDef{
	{"aetheric", "#A78BFA", "#22D3EE", "#22D3EE", "#8B5CF6", "#07070F",
		[]string{"#22D3EE", "#6E9BF0", "#A78BFA", "#8B5CF6", "#E879F9"}},
	{"crystal", "#4B9CD3", "#8FC7E8", "#5CE1E6", "#A78BFA", "#0C0F12",
		[]string{"#5CE1E6", "#4FB8E8", "#6E9BF0", "#A78BFA", "#E06CE6"}},
	{"ember", "#E6934B", "#F0B87A", "#FF9E5C", "#E0566C", "#17110D",
		[]string{"#FFD36C", "#FFA14F", "#F07C4B", "#E0566C", "#C0417A"}},
	{"matrix", "#4BD37A", "#8FE8A6", "#5CE68A", "#3FB86E", "#0A120D",
		[]string{"#9CF0B0", "#5CE68A", "#3FB86E", "#2E9E86", "#4FB8E8"}},
	{"grape", "#9B6BD3", "#C79BE8", "#B15CE6", "#E06CE6", "#120C17",
		[]string{"#C79BE8", "#A78BFA", "#B15CE6", "#E06CE6", "#F06CB0"}},
	{"mono", "#AFAFAF", "#E4E4E4", "#D0D0D0", "#9A9A9A", "#101010",
		[]string{"#E4E4E4", "#C4C4C4", "#A4A4A4", "#8A8A8A", "#B4B4B4"}},

	// Popular editor palettes (opencode-style).
	{"tokyonight", "#7AA2F7", "#7DCFFF", "#7DCFFF", "#BB9AF7", "#1A1B26",
		[]string{"#7DCFFF", "#7AA2F7", "#BB9AF7", "#FF9E64", "#F7768E"}},
	{"catppuccin", "#CBA6F7", "#F5C2E7", "#89DCEB", "#CBA6F7", "#1E1E2E",
		[]string{"#89DCEB", "#89B4FA", "#CBA6F7", "#F5C2E7", "#F38BA8"}},
	{"gruvbox", "#FABD2F", "#FE8019", "#8EC07C", "#D3869B", "#282828",
		[]string{"#8EC07C", "#B8BB26", "#FABD2F", "#FE8019", "#FB4934"}},
	{"dracula", "#BD93F9", "#FF79C6", "#8BE9FD", "#FF79C6", "#282A36",
		[]string{"#8BE9FD", "#BD93F9", "#FF79C6", "#FF5555"}},
	{"nord", "#88C0D0", "#8FBCBB", "#8FBCBB", "#B48EAD", "#2E3440",
		[]string{"#8FBCBB", "#88C0D0", "#81A1C1", "#5E81AC", "#B48EAD"}},
	{"onedark", "#61AFEF", "#56B6C2", "#56B6C2", "#C678DD", "#282C34",
		[]string{"#56B6C2", "#61AFEF", "#C678DD", "#E06C75"}},
	{"rose-pine", "#C4A7E7", "#EBBCBA", "#9CCFD8", "#C4A7E7", "#191724",
		[]string{"#9CCFD8", "#C4A7E7", "#EBBCBA", "#EB6F92"}},
	{"solarized", "#268BD2", "#2AA198", "#2AA198", "#6C71C4", "#002B36",
		[]string{"#2AA198", "#268BD2", "#6C71C4", "#D33682", "#B58900"}},
}

// currentTheme indexes themes; the active variant.
var currentTheme int

// ThemeNames returns the selectable theme names, in order.
func ThemeNames() []string {
	out := make([]string, len(themes))
	for i, t := range themes {
		out[i] = t.Name
	}
	return out
}

// CurrentThemeName returns the active theme's name.
func CurrentThemeName() string { return themes[currentTheme].Name }

// SetTheme applies the named theme; returns false if the name is unknown.
func SetTheme(name string) bool {
	for i, t := range themes {
		if t.Name == name {
			applyTheme(i)
			return true
		}
	}
	return false
}

// nextTheme cycles to the following variant and returns its name.
func nextTheme() string {
	applyTheme((currentTheme + 1) % len(themes))
	return CurrentThemeName()
}

// applyTheme reassigns the accent/gradient palette and rebuilds every style, so
// a switch takes effect on the next render.
func applyTheme(i int) {
	currentTheme = i
	t := themes[i]
	accent = lipgloss.Color(t.accent)
	accentHi = lipgloss.Color(t.accentHi)
	holoCyan = lipgloss.Color(t.cyan)
	holoViol = lipgloss.Color(t.viol)
	holoRamp = append([]string(nil), t.ramp...)
	holoLoop = mirrorRamp(t.ramp)
	rebuildStyles()
	// Note: the terminal background is intentionally not repainted — per-line
	// background fills leave artifacts in a scrolling TUI. `bg` is kept for
	// reference/config but not applied globally.
	_ = t.bg
}

// ApplyColors overrides individual palette colors by role (hex values), on top
// of the active theme, then rebuilds styles. Unknown roles are ignored. Roles:
// accent, accentHi, fg, prose, muted, dim, bad, ok, base, panel, gold, worker,
// cyan, viol.
func ApplyColors(overrides map[string]string) {
	set := func(dst *lipgloss.Color, key string) {
		if v, ok := overrides[key]; ok && v != "" {
			*dst = lipgloss.Color(v)
		}
	}
	set(&accent, "accent")
	set(&accentHi, "accentHi")
	set(&fg, "fg")
	set(&prose, "prose")
	set(&muted, "muted")
	set(&dim, "dim")
	set(&bad, "bad")
	set(&ok, "ok")
	set(&base, "base")
	set(&panel, "panel")
	set(&gold, "gold")
	set(&worker, "worker")
	set(&holoCyan, "cyan")
	set(&holoViol, "viol")
	rebuildStyles()
}

// SetGradient overrides the holographic gradient stops (start → end).
func SetGradient(stops []string) {
	if len(stops) < 2 {
		return
	}
	holoRamp = append([]string(nil), stops...)
	holoLoop = mirrorRamp(stops)
}

// mirrorRamp turns a start→end ramp into a looping start→end→start sequence for
// the flowing shimmer (…→viol→end→viol→…).
func mirrorRamp(ramp []string) []string {
	out := append([]string(nil), ramp...)
	for i := len(ramp) - 2; i >= 1; i-- {
		out = append(out, ramp[i])
	}
	return out
}
