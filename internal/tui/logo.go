package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
		grad := []string{"#BFE0F5", "#8FC7E8", "#5FB0DE", "#4B9CD3", "#3A7CA5"}
		for i := range rows {
			rows[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(grad[i])).Render(rows[i])
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
		wm := lipgloss.NewStyle().Foreground(accent).Bold(true).Render("◆  H O L O C R O N  ◆")
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
