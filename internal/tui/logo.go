package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// holoRamp is Dross's signature purple gradient (light lilac → deep violet →
// fuchsia) that gives the wordmark its shimmer.
var holoRamp = []string{"#E9D5FF", "#C084FC", "#A855F7", "#8B5CF6", "#E06CE6"}

// holoLoop is a seamless cyclic purple palette (returns to lilac) for the
// animated shimmer, so the flowing gradient has no hard seam.
var holoLoop = []string{"#E9D5FF", "#C084FC", "#A855F7", "#8B5CF6", "#E06CE6", "#8B5CF6", "#A855F7", "#C084FC"}

// purpleRamp is Dross's signature: a light-to-deep violet gradient, top to
// bottom, that gives the block wordmark its purple sheen.
var purpleRamp = []string{"#D8B4FE", "#C084FC", "#A855F7", "#8B5CF6", "#7C3AED"}

// rampColorCyclic samples stops cyclically at u (wrapped to [0,1)), interpolating
// from the last stop back to the first for a seamless loop.
func rampColorCyclic(stops []string, u float64) string {
	u -= math.Floor(u)
	seg := u * float64(len(stops))
	i := int(seg) % len(stops)
	j := (i + 1) % len(stops)
	f := seg - math.Floor(seg)
	r1, g1, b1 := parseHex(stops[i])
	r2, g2, b2 := parseHex(stops[j])
	lerp := func(a, b int) int { return int(math.Round(float64(a) + (float64(b)-float64(a))*f)) }
	return fmt.Sprintf("#%02x%02x%02x", lerp(r1, r2), lerp(g1, g2), lerp(b1, b2))
}

// gradientTextAt colors each rune along the cyclic palette, offset by phase so
// the gradient flows left-to-right as phase advances (animated shimmer).
func gradientTextAt(s string, stops []string, phase float64) string {
	runes := []rune(s)
	n := len(runes)
	var b strings.Builder
	for i, r := range runes {
		u := phase
		if n > 1 {
			u += 0.9 * float64(i) / float64(n-1)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(rampColorCyclic(stops, u))).Bold(true).Render(string(r)))
	}
	return b.String()
}

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
	'E': {"█████", "█    ", "████ ", "█    ", "█████"},
	'G': {" ████", "█    ", "█  ██", "█   █", " ████"},
	'D': {"████ ", "█   █", "█   █", "█   █", "████ "},
	'S': {" ████", "█    ", " ███ ", "    █", "████ "},
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
func renderLogo(variant int) string { return renderLogoAt(variant, -1) }

// logoWord is the wordmark rendered in the block font. Configurable so a fork or
// rename shows its own name (see SetLogoWord).
var logoWord = "DROSS"

// SetLogoWord overrides the wordmark (uppercased). Empty keeps the default.
func SetLogoWord(w string) {
	if w = strings.ToUpper(strings.TrimSpace(w)); w != "" {
		logoWord = w
	}
}

// hasGlyphs reports whether every rune of w has a block-font glyph.
func hasGlyphs(w string) bool {
	for _, r := range w {
		if _, ok := glyphs[r]; !ok {
			return false
		}
	}
	return len(w) > 0
}

// renderLogoAt renders the logo; phase >= 0 animates variant 1's holographic
// shimmer (the gradient flows down the block as phase advances). Words with a
// letter the block font lacks fall back to a one-line gradient wordmark.
func renderLogoAt(variant int, phase float64) string {
	if !hasGlyphs(logoWord) {
		if phase >= 0 {
			return gradientTextAt(logoWord, holoLoop, phase)
		}
		return gradientText(logoWord, holoRamp)
	}
	rows := bigLogoRows(logoWord)
	switch variant {
	case 1:
		// Holographic crystal: cyan → magenta down the block; when animated the
		// palette scrolls with phase for a living shimmer.
		for i := range rows {
			var c string
			if phase >= 0 {
				c = rampColorCyclic(holoLoop, phase+0.12*float64(i))
			} else {
				c = rampColor(holoRamp, float64(i)/float64(len(rows)-1))
			}
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
		wm := crystalStyle.Render("◈  ") + gradientText(logoWord, holoRamp) + crystalStyle.Render("  ◈")
		rule := lipgloss.NewStyle().Foreground(dim).Render(strings.Repeat("─", lipgloss.Width(wm)))
		return lipgloss.JoinVertical(lipgloss.Center, wm, rule)
	default:
		// Dross default: block letters in a light-to-deep purple vertical
		// gradient (animates with phase when driven).
		for i := range rows {
			var c string
			if phase >= 0 {
				c = rampColorCyclic(purpleRamp, phase+0.12*float64(i))
			} else {
				c = rampColor(purpleRamp, float64(i)/float64(len(rows)-1))
			}
			rows[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true).Render(rows[i])
		}
		return strings.Join(rows, "\n")
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
