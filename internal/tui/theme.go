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
	ramp     []string // gradient stops, start → end
}

// themes are the selectable variants (/theme). "crystal" is the default.
var themes = []themeDef{
	{"crystal", "#4B9CD3", "#8FC7E8", "#5CE1E6", "#A78BFA",
		[]string{"#5CE1E6", "#4FB8E8", "#6E9BF0", "#A78BFA", "#E06CE6"}},
	{"ember", "#E6934B", "#F0B87A", "#FF9E5C", "#E0566C",
		[]string{"#FFD36C", "#FFA14F", "#F07C4B", "#E0566C", "#C0417A"}},
	{"matrix", "#4BD37A", "#8FE8A6", "#5CE68A", "#3FB86E",
		[]string{"#9CF0B0", "#5CE68A", "#3FB86E", "#2E9E86", "#4FB8E8"}},
	{"grape", "#9B6BD3", "#C79BE8", "#B15CE6", "#E06CE6",
		[]string{"#C79BE8", "#A78BFA", "#B15CE6", "#E06CE6", "#F06CB0"}},
	{"mono", "#AFAFAF", "#E4E4E4", "#D0D0D0", "#9A9A9A",
		[]string{"#E4E4E4", "#C4C4C4", "#A4A4A4", "#8A8A8A", "#B4B4B4"}},
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

// applyTheme reassigns the accent/gradient palette and rebuilds every style that
// depends on it, so a switch takes effect on the next render.
func applyTheme(i int) {
	currentTheme = i
	t := themes[i]
	accent = lipgloss.Color(t.accent)
	accentHi = lipgloss.Color(t.accentHi)
	holoCyan = lipgloss.Color(t.cyan)
	holoViol = lipgloss.Color(t.viol)
	holoRamp = append([]string(nil), t.ramp...)
	holoLoop = mirrorRamp(t.ramp)

	logoStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	costStyle = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
	agentLabel = lipgloss.NewStyle().Foreground(accent).Bold(true)
	toolArrow = lipgloss.NewStyle().Foreground(accent)
	toolName = lipgloss.NewStyle().Foreground(accentHi).Bold(true)
	checkStyle = lipgloss.NewStyle().Foreground(accentHi)
	startGlyph = lipgloss.NewStyle().Foreground(accent)
	promptMark = lipgloss.NewStyle().Foreground(accent).Bold(true)
	crystalStyle = lipgloss.NewStyle().Foreground(holoCyan).Bold(true)
	violStyle = lipgloss.NewStyle().Foreground(holoViol)
	inputBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(holoCyan).Padding(0, 1)
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
