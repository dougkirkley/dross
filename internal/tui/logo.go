package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// holoRamp is the holographic crystal gradient (cyan → violet) that gives the
// holocron its signature shimmer. Five stops so it also maps 1:1 onto the
// 5-row block logo.
var holoRamp = []string{"#5CE1E6", "#4FB8E8", "#6E9BF0", "#8C86F5", "#A78BFA"}

// parseHex returns the r,g,b of a "#rrggbb" color.
func parseHex(h string) (int, int, int) {
	h = strings.TrimPrefix(h, "#")
	r, _ := strconv.ParseInt(h[0:2], 16, 0)
	g, _ := strconv.ParseInt(h[2:4], 16, 0)
	b, _ := strconv.ParseInt(h[4:6], 16, 0)
	return int(r), int(g), int(b)
}

// rampColor samples a multi-stop gradient at t in [0,1], returning "#rrggbb".
func rampColor(stops []string, t float64) string {
	if len(stops) == 1 {
		return stops[0]
	}
	if t <= 0 {
		return stops[0]
	}
	if t >= 1 {
		return stops[len(stops)-1]
	}
	seg := t * float64(len(stops)-1)
	i := int(seg)
	f := seg - float64(i)
	r1, g1, b1 := parseHex(stops[i])
	r2, g2, b2 := parseHex(stops[i+1])
	lerp := func(a, b int) int { return int(math.Round(float64(a) + (float64(b)-float64(a))*f)) }
	return fmt.Sprintf("#%02x%02x%02x", lerp(r1, r2), lerp(g1, g2), lerp(b1, b2))
}

// gradientText colors each rune of s along the ramp, left to right (bold).
func gradientText(s string, stops []string) string {
	runes := []rune(s)
	n := len(runes)
	var b strings.Builder
	for i, r := range runes {
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(rampColor(stops, t))).Bold(true).Render(string(r)))
	}
	return b.String()
}

// glyphs is a 5-row block font. Assembling the logo from these guarantees
// alignment (no hand-concatenated ASCII to drift out of register).
var glyphs = map[rune][5]string{
	'H': {"█   █", "█   █", "█████", "█   █", "█   █"},
	'O': {" ███ ", "█   █", "█   █", "█   █", " ███ "},
	'L': {"█    ", "█    ", "█    ", "█    ", "█████"},
	'C': {" ████", "█    ", "█    ", "█    ", " ████"},
	'R': {"████ ", "█   █", "████ ", "█  █ ", "█   █"},
	'N': {"█   █", "██  █", "█ █ █", "█  ██", "█   █"},
}

// bigLogoRows renders word in the block font, one string per row.
func bigLogoRows(word string) []string {
	rows := make([]string, 5)
	for _, ch := range word {
		g, ok := glyphs[ch]
		if !ok {
			continue
		}
		for r := 0; r < 5; r++ {
			rows[r] += g[r] + "  "
		}
	}
	return rows
}

func bigLogo(word string) string { return strings.Join(bigLogoRows(word), "\n") }

// renderLogo returns the styled logo block for a variant (pre-centering):
//
//	0 filled     — solid Carolina-blue block letters (default)
//	1 gradient   — block letters, light-to-deep blue top to bottom
//	2 boxed      — block letters inside a rounded accent border
//	3 wordmark   — slim spaced one-line mark with a rule
func renderLogo(variant int) string {
	rows := bigLogoRows("HOLOCRON")
	switch variant {
	case 1:
		// Holographic crystal: cyan at the top fading to violet at the base.
		for i := range rows {
			c := rampColor(holoRamp, float64(i)/float64(len(rows)-1))
			rows[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true).Render(rows[i])
		}
		return strings.Join(rows, "\n")
	case 2:
		body := logoStyle.Render(strings.Join(rows, "\n"))
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(1, 4).
			Render(body)
	case 3:
		wm := crystalStyle.Render("◈  ") + gradientText("H O L O C R O N", holoRamp) + crystalStyle.Render("  ◈")
		rule := lipgloss.NewStyle().Foreground(dim).Render(strings.Repeat("─", lipgloss.Width(wm)))
		return lipgloss.JoinVertical(lipgloss.Center, wm, rule)
	default:
		return logoStyle.Render(strings.Join(rows, "\n"))
	}
}

// wrap word-wraps s to width w, returning the lines.
func wrap(s string, w int) []string {
	if w < 1 {
		return []string{s}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if len(line)+1+len(word) > w {
				out = append(out, line)
				line = word
			} else {
				line += " " + word
			}
		}
		out = append(out, line)
	}
	return out
}
